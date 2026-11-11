package brain_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"whatshelper/internal/brain"
	"whatshelper/internal/core"
	"whatshelper/internal/core/fakes"
	"whatshelper/internal/pack"
)

const gatePackYAML = `
name: generic
language: pt-BR
actors:
  business: agent
  customer: customer
facts:
  - key: Budget
    type: number
    description: Orçamento
signals:
  - key: has_new_question
    description: Pergunta nova
context:
  - key: last_action
    fields:
      - key: type
        type: enum
        values: [question, other]
      - key: actor
        type: enum
        values: [agent, customer]
      - key: description
        type: string
opt_out_keywords:
  - sair
  - parar
  - "não quero mais"
prompts:
  analysis: prompts/analysis.tmpl
  reply: prompts/reply.tmpl
`

func setupGatesPipeline(t *testing.T, policy core.TenantPolicy, clock *fakes.FakeClock, llm core.LLM) (*brain.Pipeline, *fakes.MemoryStore, *fakes.MemoryChannel) {
	t.Helper()
	tmp := t.TempDir()
	packDir := filepath.Join(tmp, "packs", "generic")
	_ = os.MkdirAll(filepath.Join(packDir, "prompts"), 0o755)
	_ = os.WriteFile(filepath.Join(packDir, "pack.yaml"), []byte(gatePackYAML), 0o644)
	_ = os.WriteFile(filepath.Join(packDir, "prompts", "analysis.tmpl"), []byte("Analyze\n{{.OutputSchema}}\n<!-- USER_PROMPT -->\n{{.NewMessage.Text}}"), 0o644)
	_ = os.WriteFile(filepath.Join(packDir, "prompts", "reply.tmpl"), []byte("Reply\n<!-- USER_PROMPT -->\n{{.NewMessage.Text}}"), 0o644)

	kDir := filepath.Join(tmp, "tenants", "t1", "knowledge")
	_ = os.MkdirAll(kDir, 0o755)
	_ = os.WriteFile(filepath.Join(kDir, "info.md"), []byte("Plano custa 100."), 0o644)

	memStore := fakes.NewMemoryStoreWithClock(clock)
	policy.ID = "t1"
	policy.Pack = "generic"
	if err := brain.SeedTenantPolicy(context.Background(), memStore, policy); err != nil {
		t.Fatalf("SeedTenantPolicy failed: %v", err)
	}

	ch := fakes.NewMemoryChannel("simulator", 16)
	_ = ch.Start(context.Background())

	p := brain.NewPipeline(context.Background(), brain.PipelineConfig{
		Store:         memStore,
		Blobs:         fakes.NewMemoryBlobStore(),
		LLM:           llm,
		Bus:           core.NewInMemoryEventBus(32),
		Clock:         clock,
		Tenants:       brain.NewStoreTenantPolicyLoader(memStore, nil),
		Knowledge:     brain.NewLocalKnowledgeSource(filepath.Join(tmp, "tenants")),
		PackLoader:    func(name string) (*pack.Pack, error) { return pack.LoadFromDir(filepath.Join(tmp, "packs", name)) },
		AnalysisModel: "m-a",
		ResponseModel: "m-r",
	})
	p.RegisterChannel(ch)
	return p, memStore, ch
}

func TestMatchesOptOutKeyword(t *testing.T) {
	kws := []string{"sair", "parar", "não quero mais"}
	if !brain.MatchesOptOutKeyword("Por favor, quero SAIR agora", kws) {
		t.Fatalf("expected SAIR to match")
	}
	if !brain.MatchesOptOutKeyword("não   quero mais receber isso", kws) {
		t.Fatalf("expected normalized phrase to match")
	}
	if brain.MatchesOptOutKeyword("Preciso reparar meu computador", kws) {
		t.Fatalf("did not expect 'reparar' to match 'parar'")
	}
}

func TestTokenBucketLimiter_ExhaustionAndRefill(t *testing.T) {
	clock := fakes.NewFakeClock(time.Date(2026, 11, 11, 14, 0, 0, 0, time.UTC))
	lim := brain.NewTokenBucketLimiter(clock)
	pol := core.RateLimitPolicy{GlobalPerMinute: 4, PerSourcePerMinute: 2}

	// Fonte u1 consome 2 fichas
	for i := 0; i < 2; i++ {
		if ok, reason := lim.Allow("t1", "simulator", "u1", pol); !ok {
			t.Fatalf("expected u1 call %d allowed, got %s", i, reason)
		}
	}
	// 3ª chamada de u1 falha por limite por conversa, sem gastar ficha global!
	if ok, _ := lim.Allow("t1", "simulator", "u1", pol); ok {
		t.Fatalf("expected u1 3rd call blocked by per-source limit")
	}
	// Fonte u2 ainda consegue usar 2 fichas globais restantes
	for i := 0; i < 2; i++ {
		if ok, reason := lim.Allow("t1", "simulator", "u2", pol); !ok {
			t.Fatalf("expected u2 call %d allowed, got %s", i, reason)
		}
	}
	// Agora limite global de 4/min acabou (u3 bloqueado)
	if ok, _ := lim.Allow("t1", "simulator", "u3", pol); ok {
		t.Fatalf("expected u3 blocked by global limit")
	}

	// Avança 30 segundos -> recarrega metade (2 globais, 1 por conversa)
	clock.Advance(30 * time.Second)
	if ok, reason := lim.Allow("t1", "simulator", "u1", pol); !ok {
		t.Fatalf("expected u1 allowed after refill: %s", reason)
	}
}

func TestPipeline_PreLLMGates_ZeroLLMCalls(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 11, 14, 0, 0, 0, time.UTC)

	t.Run("bot_enabled false blocks LLM", func(t *testing.T) {
		clock := fakes.NewFakeClock(now)
		llm := fakes.NewOfflineDemoLLM()
		p, _, ch := setupGatesPipeline(t, core.TenantPolicy{Mode: core.ModeAuto, BotEnabled: false}.WithDefaults(), clock, llm)

		msg := core.NormalizedMessage{
			TenantID: "t1", Channel: "simulator", SourceKey: "u1",
			MessageID: "m1", Timestamp: now, Direction: core.DirectionInbound, Type: core.MessageTypeText, Text: "Oi",
		}
		if err := p.ProcessBatch(ctx, []core.NormalizedMessage{msg}); err != nil {
			t.Fatal(err)
		}
		if len(llm.Calls()) != 0 || len(ch.SentMessages()) != 0 {
			t.Fatalf("expected 0 LLM calls and 0 sent messages, got llm=%d sent=%d", len(llm.Calls()), len(ch.SentMessages()))
		}
	})

	t.Run("paused source blocks LLM", func(t *testing.T) {
		clock := fakes.NewFakeClock(now)
		llm := fakes.NewOfflineDemoLLM()
		p, st, ch := setupGatesPipeline(t, core.TenantPolicy{Mode: core.ModeAuto, BotEnabled: true}.WithDefaults(), clock, llm)
		_ = brain.UpdateSourceStatus(ctx, st, now, "t1", "simulator", "u-paused", core.SourceStatusPaused)

		msg := core.NormalizedMessage{
			TenantID: "t1", Channel: "simulator", SourceKey: "u-paused",
			MessageID: "m1", Timestamp: now, Direction: core.DirectionInbound, Type: core.MessageTypeText, Text: "Oi",
		}
		if err := p.ProcessBatch(ctx, []core.NormalizedMessage{msg}); err != nil {
			t.Fatal(err)
		}
		if len(llm.Calls()) != 0 || len(ch.SentMessages()) != 0 {
			t.Fatalf("expected 0 LLM calls when paused, got llm=%d sent=%d", len(llm.Calls()), len(ch.SentMessages()))
		}
	})

	t.Run("opt_out keyword marks source opted_out without LLM", func(t *testing.T) {
		clock := fakes.NewFakeClock(now)
		llm := fakes.NewOfflineDemoLLM()
		p, st, ch := setupGatesPipeline(t, core.TenantPolicy{Mode: core.ModeAuto, BotEnabled: true}.WithDefaults(), clock, llm)

		msg := core.NormalizedMessage{
			TenantID: "t1", Channel: "simulator", SourceKey: "u-opt",
			MessageID: "m1", Timestamp: now, Direction: core.DirectionInbound, Type: core.MessageTypeText, Text: "SAIR",
		}
		if err := p.ProcessBatch(ctx, []core.NormalizedMessage{msg}); err != nil {
			t.Fatal(err)
		}
		if len(llm.Calls()) != 0 || len(ch.SentMessages()) != 0 {
			t.Fatalf("expected 0 LLM calls on keyword opt-out, got llm=%d sent=%d", len(llm.Calls()), len(ch.SentMessages()))
		}
		state, _ := st.GetSourceState(ctx, "t1", "simulator", "u-opt")
		if state.Meta.Status != core.SourceStatusOptedOut {
			t.Fatalf("expected opted_out status, got %s", state.Meta.Status)
		}
	})
}

func TestPipeline_OutOfHours_StaticReplyAndTaskWithoutReplyLLM(t *testing.T) {
	ctx := context.Background()
	// 2026-11-11 23:00 UTC == 20:00 America/Sao_Paulo (fora de 09:00-18:00)
	now := time.Date(2026, 11, 11, 23, 0, 0, 0, time.UTC)
	clock := fakes.NewFakeClock(now)
	llm := fakes.NewOfflineDemoLLM()

	pol := core.TenantPolicy{
		Mode:            core.ModeAuto,
		BotEnabled:      true,
		Timezone:        "America/Sao_Paulo",
		OutOfHoursReply: "Voltamos amanhã às 09h!",
		BusinessHours: core.BusinessHours{
			Enabled: true,
			Days:    []string{"mon", "tue", "wed", "thu", "fri"},
			Start:   "09:00",
			End:     "18:00",
		},
	}.WithDefaults()

	p, st, ch := setupGatesPipeline(t, pol, clock, llm)
	msg := core.NormalizedMessage{
		TenantID: "t1", Channel: "simulator", SourceKey: "u-night",
		MessageID: "m1", Timestamp: now, Direction: core.DirectionInbound, Type: core.MessageTypeText, Text: "Tem plano disponível?",
	}
	if err := p.ProcessBatch(ctx, []core.NormalizedMessage{msg}); err != nil {
		t.Fatal(err)
	}

	// Apenas 1 chamada ao LLM (analysis); nenhuma chamada para reply
	if len(llm.Calls()) != 1 || llm.Calls()[0].Stage != "analysis" {
		t.Fatalf("expected only analysis LLM call out of hours, got %+v", llm.Calls())
	}
	sent := ch.SentMessages()
	if len(sent) != 1 || sent[0].Text != "Voltamos amanhã às 09h!" {
		t.Fatalf("expected static out_of_hours reply, got %+v", sent)
	}
	tasks, err := st.ListSourceTasks(ctx, "t1", "simulator", "u-night")
	if err != nil || len(tasks) != 1 || tasks[0].Type != "out_of_hours_followup" {
		t.Fatalf("expected out_of_hours_followup task, got %+v err=%v", tasks, err)
	}
}
