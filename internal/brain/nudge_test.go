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

const nudgePackYAML = `
name: generic
language: pt-BR
actors:
  business: agent
  customer: customer
facts:
  - key: Plan
    type: string
    description: Plano
signals:
  - key: has_new_question
    description: Nova pergunta
context:
  - key: last_action
    fields:
      - key: type
        type: enum
        values: [question, other]
      - key: actor
        type: enum
        values: [agent, customer]
prompts:
  analysis: prompts/analysis.tmpl
  reply: prompts/reply.tmpl
  nudge: prompts/nudge.tmpl
`

func setupNudgeEnv(t *testing.T, policy core.TenantPolicy, clock *fakes.FakeClock, llm core.LLM) (*brain.Pipeline, *fakes.MemoryStore, *fakes.MemoryChannel) {
	t.Helper()
	tmp := t.TempDir()
	packDir := filepath.Join(tmp, "packs", "generic")
	_ = os.MkdirAll(filepath.Join(packDir, "prompts"), 0o755)
	_ = os.WriteFile(filepath.Join(packDir, "pack.yaml"), []byte(nudgePackYAML), 0o644)
	_ = os.WriteFile(filepath.Join(packDir, "prompts", "analysis.tmpl"), []byte("Analyze\n{{.OutputSchema}}\n<!-- USER_PROMPT -->\n{{.NewMessage.Text}}"), 0o644)
	_ = os.WriteFile(filepath.Join(packDir, "prompts", "reply.tmpl"), []byte("Reply\n<!-- USER_PROMPT -->\n{{.NewMessage.Text}}"), 0o644)
	_ = os.WriteFile(filepath.Join(packDir, "prompts", "nudge.tmpl"), []byte("Generate nudge in {{.Pack.Language}}\n<!-- USER_PROMPT -->\n{{.Summary}}"), 0o644)

	kDir := filepath.Join(tmp, "tenants", "t1", "knowledge")
	_ = os.MkdirAll(kDir, 0o755)
	_ = os.WriteFile(filepath.Join(kDir, "info.md"), []byte("Informações gerais."), 0o644)

	st := fakes.NewMemoryStoreWithClock(clock)
	policy.ID = "t1"
	policy.Pack = "generic"
	if err := brain.SeedTenantPolicy(context.Background(), st, policy); err != nil {
		t.Fatal(err)
	}

	ch := fakes.NewMemoryChannel("simulator", 16)
	_ = ch.Start(context.Background())

	p := brain.NewPipeline(context.Background(), brain.PipelineConfig{
		Store:         st,
		Blobs:         fakes.NewMemoryBlobStore(),
		LLM:           llm,
		Bus:           core.NewInMemoryEventBus(32),
		Clock:         clock,
		Tenants:       brain.NewStoreTenantPolicyLoader(st, nil),
		Knowledge:     brain.NewLocalKnowledgeSource(filepath.Join(tmp, "tenants")),
		PackLoader:    func(name string) (*pack.Pack, error) { return pack.LoadFromDir(filepath.Join(tmp, "packs", name)) },
		AnalysisModel: "m-a",
		ResponseModel: "m-r",
		NudgeModel:    "m-n",
	})
	p.RegisterChannel(ch)
	return p, st, ch
}

func TestNudge_DisabledByDefault_And_LifecycleWhenEnabled(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 11, 14, 0, 0, 0, time.UTC)

	t.Run("does not schedule nudge when proactive_enabled is false", func(t *testing.T) {
		clock := fakes.NewFakeClock(now)
		llm := fakes.NewOfflineDemoLLM()
		pol := core.TenantPolicy{Mode: core.ModeAuto, BotEnabled: true, ProactiveEnabled: false}.WithDefaults()
		p, st, _ := setupNudgeEnv(t, pol, clock, llm)
		defer p.Stop()

		msg := core.NormalizedMessage{
			TenantID: "t1", Channel: "simulator", SourceKey: "u1",
			MessageID: "m1", Timestamp: now, Direction: core.DirectionInbound, Type: core.MessageTypeText, Text: "Oi",
		}
		if err := p.ProcessBatch(ctx, []core.NormalizedMessage{msg}); err != nil {
			t.Fatal(err)
		}
		tasks, _ := st.ListSourceTasks(ctx, "t1", "simulator", "u1")
		if len(tasks) != 0 {
			t.Fatalf("expected 0 nudges when proactive_enabled=false, got %+v", tasks)
		}
	})

	t.Run("schedules, dispatches after delay, and cancels on inbound", func(t *testing.T) {
		clock := fakes.NewFakeClock(now)
		llm := fakes.NewOfflineDemoLLM()
		pol := core.TenantPolicy{
			Mode:             core.ModeAuto,
			BotEnabled:       true,
			ProactiveEnabled: true,
			Timezone:         "America/Sao_Paulo",
			BusinessHours: core.BusinessHours{
				Enabled: true,
				Days:    []string{"mon", "tue", "wed", "thu", "fri"},
				Start:   "09:00",
				End:     "18:00",
			},
			Nudge: core.NudgePolicy{DelayMinutes: 30, MaxAttempts: 2},
		}.WithDefaults()

		p, st, ch := setupNudgeEnv(t, pol, clock, llm)
		defer p.Stop()

		msg := core.NormalizedMessage{
			TenantID: "t1", Channel: "simulator", SourceKey: "u-nudge",
			MessageID: "m1", Timestamp: now, Direction: core.DirectionInbound, Type: core.MessageTypeText, Text: "Oi",
		}
		if err := p.ProcessBatch(ctx, []core.NormalizedMessage{msg}); err != nil {
			t.Fatal(err)
		}

		// Antes de vencer (15 min): nada enviado
		clock.Advance(15 * time.Minute)
		count, err := p.ProcessDueNudges(ctx, "t1")
		if err != nil || count != 0 || len(ch.SentMessages()) != 1 {
			t.Fatalf("expected 0 nudges sent before delay, count=%d err=%v sent=%d", count, err, len(ch.SentMessages()))
		}

		// Após vencer (+20 min = 35 min total): envia 1º nudge e agenda tentativa 2
		clock.Advance(20 * time.Minute)
		count, err = p.ProcessDueNudges(ctx, "t1")
		if err != nil || count != 1 || len(ch.SentMessages()) != 2 {
			t.Fatalf("expected 1 nudge sent, count=%d err=%v sent=%d", count, err, len(ch.SentMessages()))
		}
		tasks, _ := st.ListSourceTasks(ctx, "t1", "simulator", "u-nudge")
		if len(tasks) != 1 || tasks[0].Attempt != 2 || tasks[0].Status != core.TaskStatusPending {
			t.Fatalf("expected pending nudge attempt 2, got %+v", tasks)
		}

		// Se cliente envia nova mensagem, nudge pendente é cancelado
		replyIn := core.NormalizedMessage{
			TenantID: "t1", Channel: "simulator", SourceKey: "u-nudge",
			MessageID: "m2", Timestamp: clock.Now(), Direction: core.DirectionInbound, Type: core.MessageTypeText, Text: "Voltei!",
		}
		if err := p.Ingest(ctx, replyIn); err != nil {
			t.Fatal(err)
		}
		tasks, _ = st.ListSourceTasks(ctx, "t1", "simulator", "u-nudge")
		if len(tasks) != 1 || tasks[0].Status != core.TaskStatusCancelled {
			t.Fatalf("expected cancelled nudge after inbound, got %+v", tasks)
		}
	})

	t.Run("respects business hours and skips paused source", func(t *testing.T) {
		clock := fakes.NewFakeClock(now)
		llm := fakes.NewOfflineDemoLLM()
		pol := core.TenantPolicy{
			Mode:             core.ModeAuto,
			BotEnabled:       true,
			ProactiveEnabled: true,
			Timezone:         "America/Sao_Paulo",
			BusinessHours: core.BusinessHours{
				Enabled: true,
				Days:    []string{"mon", "tue", "wed", "thu", "fri"},
				Start:   "09:00",
				End:     "18:00",
			},
			Nudge: core.NudgePolicy{DelayMinutes: 30, MaxAttempts: 1},
		}.WithDefaults()

		p, st, ch := setupNudgeEnv(t, pol, clock, llm)
		defer p.Stop()

		if err := p.ScheduleNudge(ctx, "t1", "simulator", "u-paused", ""); err != nil {
			t.Fatal(err)
		}
		// Avança 9h -> 23:00 UTC (20:00 America/Sao_Paulo - fora do horário comercial)
		clock.Advance(9 * time.Hour)
		count, err := p.ProcessDueNudges(ctx, "t1")
		if err != nil || count != 0 {
			t.Fatalf("expected 0 nudges sent out of hours, count=%d err=%v", count, err)
		}

		// Volta para próximo dia 14:00 UTC (11:00 SP) mas pausa a fonte
		clock.Advance(15 * time.Hour)
		_ = brain.UpdateSourceStatus(ctx, st, clock.Now(), "t1", "simulator", "u-paused", core.SourceStatusPaused)
		count, err = p.ProcessDueNudges(ctx, "t1")
		if err != nil || count != 0 || len(ch.SentMessages()) != 0 {
			t.Fatalf("expected 0 nudges on paused source, count=%d sent=%d", count, len(ch.SentMessages()))
		}
		tasks, _ := st.ListSourceTasks(ctx, "t1", "simulator", "u-paused")
		if len(tasks) != 1 || tasks[0].Status != core.TaskStatusCancelled {
			t.Fatalf("expected paused nudge cancelled, got %+v", tasks)
		}
	})
}
