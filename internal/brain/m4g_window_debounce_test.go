package brain_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"whatshelper/internal/brain"
	"whatshelper/internal/core"
	"whatshelper/internal/core/fakes"
	"whatshelper/internal/pack"
)

const m4gTestPackYAML = `
name: realestate_m4g
language: pt-BR
actors:
  business: agent
  customer: customer
facts:
  - key: Goal
    type: string
    description: Objetivos do cliente
  - key: Budget
    type: number
    description: Orçamento máximo
signals:
  - key: has_new_question
    description: Nova pergunta
context:
  - key: last_action
    fields:
      - key: type
        type: enum
        values: [question, other]
prompts:
  analysis: prompts/analysis.tmpl
  reply: prompts/reply.tmpl
`

func TestM4g_FourFragmentedMessagesInBurst_SingleTurnAndTwoTurnsWhenAboveCap(t *testing.T) {
	ctx := context.Background()
	st := fakes.NewMemoryStore()
	llm := fakes.NewScriptedLLM(nil)

	// Turno 1 (4 mensagens agrupadas no mesmo lote, lidas de testdata/m4g_burst_ptbr.json)
	llm.EnqueueStage("analysis", core.LLMResponse{
		Content: `{"summary":"Cliente quer comprar, vender o próprio e alugar outro em março.","last_sender":"customer","facts":{"Goal":"comprar_vender_alugar"},"signals":{"has_new_question":true},"context":{"last_action":{"type":"question"}}}`,
	})
	llm.EnqueueStage("response", core.LLMResponse{
		Content: `{"reply":"Bom dia! Posso ajudar com a compra, a venda do seu atual e a locação para março. Qual região você prefere para a compra?","confidence":"high","used_facts":["Goal"]}`,
	})

	// Turno 2 (mensagem enviada 30s depois, acima do teto de 16s -> novo turno que vê o 1º no histórico)
	llm.EnqueueStage("analysis", core.LLMResponse{
		Content: `{"summary":"Cliente informou orçamento de 2000.","last_sender":"customer","facts":{"Budget":2000},"signals":{"has_new_question":true},"context":{"last_action":{"type":"other"}}}`,
	})
	llm.EnqueueStage("response", core.LLMResponse{
		Content: `{"reply":"Perfeito, registrei o valor de 2000 junto ao plano de compra, venda e locação em março.","confidence":"high","used_facts":["Goal","Budget"]}`,
	})

	fs := &fakeScheduler{}
	tmp := t.TempDir()
	packDir := filepath.Join(tmp, "packs", "sample")
	_ = os.MkdirAll(filepath.Join(packDir, "prompts"), 0o755)
	_ = os.WriteFile(filepath.Join(packDir, "pack.yaml"), []byte(m4gTestPackYAML), 0o644)
	_ = os.WriteFile(filepath.Join(packDir, "prompts", "analysis.tmpl"), []byte("Analyze:\n{{.OutputSchema}}\n<!-- USER_PROMPT -->\nHIST:{{range .RecentMessages}}[{{.Sender}}:{{.Text}}]{{end}}\nMSG: {{.NewMessage.Text}}"), 0o644)
	_ = os.WriteFile(filepath.Join(packDir, "prompts", "reply.tmpl"), []byte("Reply\n<!-- USER_PROMPT -->\nMsg: {{.NewMessage.Text}}"), 0o644)

	kDir := filepath.Join(tmp, "tenants", "demo", "knowledge")
	_ = os.MkdirAll(kDir, 0o755)
	_ = os.WriteFile(filepath.Join(tmp, "tenants", "demo", "tenant.yaml"), []byte("id: demo\nname: Demo\npack: sample\n"), 0o644)
	_ = os.WriteFile(filepath.Join(kDir, "info.md"), []byte("Atendemos compra, venda, locação em março e planos de 2000."), 0o644)

	blobs := fakes.NewMemoryBlobStore()
	bus := core.NewInMemoryEventBus(64)
	clock := fakes.NewFakeClock(time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC))
	ch := fakes.NewMemoryChannel("simulator", 16)
	_ = ch.Start(ctx)

	p := brain.NewPipeline(ctx, brain.PipelineConfig{
		Store:         st,
		Blobs:         blobs,
		LLM:           llm,
		Bus:           bus,
		Clock:         clock,
		Tenants:       brain.NewLocalTenantLoader(filepath.Join(tmp, "tenants")),
		Knowledge:     brain.NewLocalKnowledgeSource(filepath.Join(tmp, "tenants")),
		PackLoader:    func(name string) (*pack.Pack, error) { return pack.LoadFromDir(filepath.Join(tmp, "packs", name)) },
		AnalysisModel: "model-a",
		ResponseModel: "model-r",
		Debounce:      8 * time.Second,
		TypingMaxWait: 16 * time.Second,
		AfterFunc:     fs.AfterFunc,
	})
	p.RegisterChannel(ch)
	defer p.Stop()

	t0 := clock.Now()
	fixtureRaw, err := os.ReadFile(filepath.Join("testdata", "m4g_burst_ptbr.json"))
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}
	var fixture struct {
		Fragments []string `json:"fragments"`
	}
	if err := json.Unmarshal(fixtureRaw, &fixture); err != nil {
		t.Fatalf("failed to parse fixture: %v", err)
	}
	fragments := fixture.Fragments
	for i, txt := range fragments {
		msg := core.NormalizedMessage{
			TenantID:  "demo",
			Channel:   "simulator",
			SourceKey: "client-burst",
			MessageID: "frag-" + string(rune('1'+i)),
			Timestamp: t0.Add(time.Duration(i*2) * time.Second),
			Direction: core.DirectionInbound,
			Origin:    core.OriginCustomer,
			Type:      core.MessageTypeText,
			Text:      txt,
		}
		if _, err := p.IngestWithStatus(ctx, msg); err != nil {
			t.Fatalf("ingest %d failed: %v", i, err)
		}
	}

	// Dispara o debounce após as 4 mensagens -> deve gerar 1 único turno e 1 única resposta
	fs.FireAllActive()

	sent := waitForSentCount(t, ch, 1)
	if len(sent) != 1 {
		t.Fatalf("expected 1 unified reply for 4 fragments, got %d", len(sent))
	}
	if !strings.Contains(sent[0].Text, "compra") || !strings.Contains(sent[0].Text, "venda") || !strings.Contains(sent[0].Text, "março") {
		t.Fatalf("reply did not cover all 3 objectives: %q", sent[0].Text)
	}

	// Agora 30s depois (acima do teto de 16s), chega nova mensagem -> 2º turno vendo o 1º no histórico
	clock.Advance(30 * time.Second)
	msgTurn2 := core.NormalizedMessage{
		TenantID:  "demo",
		Channel:   "simulator",
		SourceKey: "client-burst",
		MessageID: "turn2-1",
		Timestamp: clock.Now(),
		Direction: core.DirectionInbound,
		Origin:    core.OriginCustomer,
		Type:      core.MessageTypeText,
		Text:      "Meu orçamento é 2000",
	}
	if _, err := p.IngestWithStatus(ctx, msgTurn2); err != nil {
		t.Fatalf("ingest turn2 failed: %v", err)
	}
	fs.FireAllActive()

	sent2 := waitForSentCount(t, ch, 2)
	if len(sent2) != 2 {
		t.Fatalf("expected 2 total replies after second turn, got %d", len(sent2))
	}

	state, err := st.GetSourceState(ctx, "demo", "simulator", "client-burst")
	if err != nil {
		t.Fatalf("GetSourceState failed: %v", err)
	}
	if state.Facts["Goal"] != "comprar_vender_alugar" || state.Facts["Budget"] != "2000" {
		t.Fatalf("expected accumulated facts from both turns, got %+v", state.Facts)
	}
}

type hookLLM struct {
	mu      sync.Mutex
	onCall  func(callIdx int, req core.LLMRequest)
	callIdx int
	inner   *fakes.ScriptedLLM
}

func (h *hookLLM) Name() string { return "hook" }

func (h *hookLLM) Generate(ctx context.Context, req core.LLMRequest) (*core.LLMResponse, error) {
	h.mu.Lock()
	h.callIdx++
	idx := h.callIdx
	fn := h.onCall
	h.mu.Unlock()
	if fn != nil {
		fn(idx, req)
	}
	return h.inner.Generate(ctx, req)
}

func waitForSentCount(t *testing.T, ch *fakes.MemoryChannel, count int) []core.NormalizedMessage {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		sent := ch.SentMessages()
		if len(sent) >= count {
			return sent
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %d sent messages, got %d", count, len(ch.SentMessages()))
	return nil
}

func TestM4g_StaleReplyGuard_DiscardsAndReprocessesWhenMessageArrivesDuringLLM(t *testing.T) {
	ctx := context.Background()
	st := fakes.NewMemoryStore()
	scripted := fakes.NewScriptedLLM(nil)

	// 1ª análise (apenas m1) -> será descartada porque m2 chega durante a chamada ao LLM!
	scripted.EnqueueStage("analysis", core.LLMResponse{
		Content: `{"summary":"Parcial m1","last_sender":"customer","facts":{"Goal":"comprar"},"signals":{"has_new_question":true},"context":{"last_action":{"type":"question"}}}`,
	})
	// 2ª análise (reprocessamento com lote unificado [m1, m2])
	scripted.EnqueueStage("analysis", core.LLMResponse{
		Content: `{"summary":"Completo m1 e m2","last_sender":"customer","facts":{"Goal":"comprar_e_alugar","Budget":2000},"signals":{"has_new_question":true},"context":{"last_action":{"type":"question"}}}`,
	})
	scripted.EnqueueStage("response", core.LLMResponse{
		Content: `{"reply":"Resposta unificada para compra e aluguel no plano de 2000.","confidence":"high","used_facts":["Goal","Budget"]}`,
	})

	var p *brain.Pipeline
	fs := &fakeScheduler{}
	hLLM := &hookLLM{
		inner: scripted,
		onCall: func(callIdx int, req core.LLMRequest) {
			if callIdx == 1 && req.Stage == "analysis" {
				// Enquanto o LLM está analisando m1, chega m2!
				_, _ = p.IngestWithStatus(ctx, core.NormalizedMessage{
					TenantID:  "demo",
					Channel:   "simulator",
					SourceKey: "stale-user",
					MessageID: "m2-late",
					Timestamp: time.Now().UTC(),
					Direction: core.DirectionInbound,
					Origin:    core.OriginCustomer,
					Type:      core.MessageTypeText,
					Text:      "E também quero alugar pelo plano de 2000",
				})
			}
		},
	}

	var blobs *fakes.MemoryBlobStore
	var bus *core.InMemoryEventBus
	var ch *fakes.MemoryChannel
	p, blobs, bus, ch = setupPipelineTestEnvWithAfterFunc(t, st, hLLM, 8*time.Second, fs.AfterFunc)
	defer p.Stop()

	sub := bus.Subscribe(core.EventFilter{TenantID: "demo"})
	defer sub.Cancel()

	// Envia m1 e dispara o timer para entrar em ProcessBatch
	_, err := p.IngestWithStatus(ctx, core.NormalizedMessage{
		TenantID:  "demo",
		Channel:   "simulator",
		SourceKey: "stale-user",
		MessageID: "m1-first",
		Timestamp: time.Now().UTC(),
		Direction: core.DirectionInbound,
		Origin:    core.OriginCustomer,
		Type:      core.MessageTypeText,
		Text:      "Quero comprar agora",
	})
	if err != nil {
		t.Fatal(err)
	}
	fs.FireAllActive()

	sent := waitForSentCount(t, ch, 1)
	if len(sent) != 1 {
		t.Fatalf("expected exactly 1 reply after stale discard + reprocess, got %d", len(sent))
	}
	if !strings.Contains(sent[0].Text, "compra e aluguel") {
		t.Fatalf("expected unified reply, got %q", sent[0].Text)
	}

	// Verifica que o evento reply_discarded_stale foi emitido
	foundStaleEvent := false
drainLoop:
	for {
		select {
		case ev := <-sub.Events():
			if ev.Type == core.EventReplyDiscardedStale {
				foundStaleEvent = true
			}
		default:
			break drainLoop
		}
	}
	if !foundStaleEvent {
		t.Fatal("expected EventReplyDiscardedStale to be published")
	}

	// Verifica no RunTrace persistido que BatchSize=2, ReprocessCount=1 e DiscardedReplies=1
	runs, err := st.ListRuns(ctx, "demo", "simulator", "stale-user", 5)
	if err != nil || len(runs) == 0 {
		t.Fatalf("ListRuns failed: %v", err)
	}
	rawTrace, err := blobs.Get(ctx, runs[0].TraceBlobRef)
	if err != nil {
		t.Fatalf("Get trace failed: %v", err)
	}
	var trace brain.RunTrace
	if err := json.Unmarshal(rawTrace, &trace); err != nil {
		t.Fatalf("Unmarshal trace failed: %v", err)
	}
	if trace.BatchSize != 2 || trace.ReprocessCount != 1 || trace.DiscardedReplies != 1 {
		t.Fatalf("unexpected trace counters: batch_size=%d reprocess=%d discarded=%d", trace.BatchSize, trace.ReprocessCount, trace.DiscardedReplies)
	}
}

func TestM4g_Regression_TimerExpiresDuringLLMCall_StillSingleReply(t *testing.T) {
	ctx := context.Background()
	st := fakes.NewMemoryStore()
	scripted := fakes.NewScriptedLLM(nil)

	// 1ª análise (m1) -> deve ser descartada mesmo que o timer de debounce de m2 já tenha disparado durante a chamada ao LLM!
	scripted.EnqueueStage("analysis", core.LLMResponse{
		Content: `{"summary":"Parcial m1","last_sender":"customer","facts":{"Goal":"comprar"},"signals":{"has_new_question":true},"context":{"last_action":{"type":"question"}}}`,
	})
	// 2ª análise (reprocessamento unificado [m1, m2])
	scripted.EnqueueStage("analysis", core.LLMResponse{
		Content: `{"summary":"Unificado m1 e m2","last_sender":"customer","facts":{"Goal":"comprar_e_vender","Budget":2000},"signals":{"has_new_question":true},"context":{"last_action":{"type":"question"}}}`,
	})
	scripted.EnqueueStage("response", core.LLMResponse{
		Content: `{"reply":"Resposta única consolidada para compra e aluguel no plano de 2000.","confidence":"high","used_facts":["Goal","Budget"]}`,
	})

	var p *brain.Pipeline
	fs := &fakeScheduler{}
	hLLM := &hookLLM{
		inner: scripted,
		onCall: func(callIdx int, req core.LLMRequest) {
			if callIdx == 1 && req.Stage == "analysis" {
				// m2 chega E seu timer de debounce vence enquanto m1 ainda está dentro da chamada ao LLM!
				_, _ = p.IngestWithStatus(ctx, core.NormalizedMessage{
					TenantID:  "demo",
					Channel:   "simulator",
					SourceKey: "race-user",
					MessageID: "m2-race",
					Timestamp: time.Now().UTC(),
					Direction: core.DirectionInbound,
					Origin:    core.OriginCustomer,
					Type:      core.MessageTypeText,
					Text:      "eu queria saber o que fazer nesse caso",
				})
				go fs.FireAllActive()
				// Pequena janela para qualquer goroutine de flush tentar rodar
				time.Sleep(20 * time.Millisecond)
			}
		},
	}

	p, _, _, ch := setupPipelineTestEnvWithAfterFunc(t, st, hLLM, 1500*time.Millisecond, fs.AfterFunc)
	defer p.Stop()

	_, err := p.IngestWithStatus(ctx, core.NormalizedMessage{
		TenantID:  "demo",
		Channel:   "simulator",
		SourceKey: "race-user",
		MessageID: "m1-race",
		Timestamp: time.Now().UTC(),
		Direction: core.DirectionInbound,
		Origin:    core.OriginCustomer,
		Type:      core.MessageTypeText,
		Text:      "um consultor saiu da nossa equipe",
	})
	if err != nil {
		t.Fatal(err)
	}
	fs.FireAllActive()

	sent := waitForSentCount(t, ch, 1)
	time.Sleep(50 * time.Millisecond)
	if len(ch.SentMessages()) != 1 {
		t.Fatalf("expected strictly 1 reply, got %d (%+v)", len(ch.SentMessages()), ch.SentMessages())
	}
	if !strings.Contains(sent[0].Text, "Resposta única consolidada") {
		t.Fatalf("expected consolidated reply, got %q", sent[0].Text)
	}
}

func setupPipelineTestEnvWithAfterFunc(t *testing.T, st core.Store, llm core.LLM, debounce time.Duration, afterFn brain.AfterFunc) (*brain.Pipeline, *fakes.MemoryBlobStore, *core.InMemoryEventBus, *fakes.MemoryChannel) {
	t.Helper()
	tmp := t.TempDir()

	packDir := filepath.Join(tmp, "packs", "sample")
	_ = os.MkdirAll(filepath.Join(packDir, "prompts"), 0o755)
	_ = os.WriteFile(filepath.Join(packDir, "pack.yaml"), []byte(m4gTestPackYAML), 0o644)
	_ = os.WriteFile(filepath.Join(packDir, "prompts", "analysis.tmpl"), []byte("Analyze:\n{{.OutputSchema}}\n<!-- USER_PROMPT -->\nMSG: {{.NewMessage.Text}}"), 0o644)
	_ = os.WriteFile(filepath.Join(packDir, "prompts", "reply.tmpl"), []byte("Reply\n<!-- USER_PROMPT -->\nMsg: {{.NewMessage.Text}}"), 0o644)

	kDir := filepath.Join(tmp, "tenants", "demo", "knowledge")
	_ = os.MkdirAll(kDir, 0o755)
	_ = os.WriteFile(filepath.Join(tmp, "tenants", "demo", "tenant.yaml"), []byte("id: demo\nname: Demo\npack: sample\n"), 0o644)
	_ = os.WriteFile(filepath.Join(kDir, "info.md"), []byte("Atendemos compra e aluguel no plano de 2000."), 0o644)

	blobs := fakes.NewMemoryBlobStore()
	bus := core.NewInMemoryEventBus(64)
	clock := fakes.NewFakeClock(time.Date(2026, 11, 15, 14, 0, 0, 0, time.UTC))
	ch := fakes.NewMemoryChannel("simulator", 16)
	_ = ch.Start(context.Background())

	p := brain.NewPipeline(context.Background(), brain.PipelineConfig{
		Store:         st,
		Blobs:         blobs,
		LLM:           llm,
		Bus:           bus,
		Clock:         clock,
		Tenants:       brain.NewLocalTenantLoader(filepath.Join(tmp, "tenants")),
		Knowledge:     brain.NewLocalKnowledgeSource(filepath.Join(tmp, "tenants")),
		PackLoader:    func(name string) (*pack.Pack, error) { return pack.LoadFromDir(filepath.Join(tmp, "packs", name)) },
		AnalysisModel: "model-a",
		ResponseModel: "model-r",
		Debounce:      debounce,
		AfterFunc:     afterFn,
	})
	p.RegisterChannel(ch)
	return p, blobs, bus, ch
}
