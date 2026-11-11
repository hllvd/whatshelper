package brain_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"whatshelper/internal/brain"
	"whatshelper/internal/core"
	"whatshelper/internal/core/fakes"
	"whatshelper/internal/pack"
)

func setupPipelineTestEnv(t *testing.T, store core.Store, llm core.LLM) (*brain.Pipeline, *fakes.MemoryBlobStore, *core.InMemoryEventBus, *fakes.MemoryChannel) {
	t.Helper()
	tmp := t.TempDir()

	packDir := filepath.Join(tmp, "packs", "sample")
	if err := os.MkdirAll(filepath.Join(packDir, "prompts"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(packDir, "pack.yaml"), []byte(testPackYAML), 0o644)
	_ = os.WriteFile(filepath.Join(packDir, "prompts", "analysis.tmpl"), []byte("Analyze:\n{{.OutputSchema}}\n<!-- USER_PROMPT -->\nMSG: {{.NewMessage.Text}}"), 0o644)
	_ = os.WriteFile(filepath.Join(packDir, "prompts", "reply.tmpl"), []byte("Reply using:\n{{range .Knowledge}}{{.Content}}\n{{end}}\nQA: {{.QAContent}}\n<!-- USER_PROMPT -->\nSummary: {{.Summary}}\nMsg: {{.NewMessage.Text}}"), 0o644)

	kDir := filepath.Join(tmp, "tenants", "demo", "knowledge")
	if err := os.MkdirAll(kDir, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(tmp, "tenants", "demo", "tenant.yaml"), []byte("id: demo\nname: Demo\npack: sample\n"), 0o644)
	_ = os.WriteFile(filepath.Join(kDir, "info.md"), []byte("Plano A custa 2000."), 0o644)
	_ = os.WriteFile(filepath.Join(kDir, "QA.md"), []byte("P: Prazo? R: Imediato."), 0o644)

	blobs := fakes.NewMemoryBlobStore()
	bus := core.NewInMemoryEventBus(64)
	clock := fakes.NewFakeClock(time.Date(2026, 11, 11, 14, 0, 0, 0, time.UTC))
	ch := fakes.NewMemoryChannel("simulator", 16)
	_ = ch.Start(context.Background())

	p := brain.NewPipeline(context.Background(), brain.PipelineConfig{
		Store:         store,
		Blobs:         blobs,
		LLM:           llm,
		Bus:           bus,
		Clock:         clock,
		Tenants:       brain.NewLocalTenantLoader(filepath.Join(tmp, "tenants")),
		Knowledge:     brain.NewLocalKnowledgeSource(filepath.Join(tmp, "tenants")),
		PackLoader:    func(name string) (*pack.Pack, error) { return pack.LoadFromDir(filepath.Join(tmp, "packs", name)) },
		AnalysisModel: "model-a",
		ResponseModel: "model-r",
		Debounce:      0,
	})
	p.RegisterChannel(ch)
	return p, blobs, bus, ch
}

func TestPipeline_HappyPathAndDedupe(t *testing.T) {
	ctx := context.Background()
	store := fakes.NewMemoryStore()
	llm := fakes.NewScriptedLLM(nil)
	llm.EnqueueStage("analysis", core.LLMResponse{
		Content:          `{"summary":"Cliente quer plano de 2000.","last_sender":"customer","facts":{"Budget":2000},"signals":{"has_new_question":true},"context":{"last_action":{"type":"question"}}}`,
		PromptTokens:     50,
		CompletionTokens: 25,
		DurationMs:       10,
	})
	llm.EnqueueStage("response", core.LLMResponse{
		Content:          "**Olá!** Temos o Plano A por 2000.\n- Entrega imediata!",
		PromptTokens:     70,
		CompletionTokens: 15,
		DurationMs:       12,
	})

	p, blobs, bus, ch := setupPipelineTestEnv(t, store, llm)
	defer p.Stop()

	sub := bus.Subscribe(core.EventFilter{TenantID: "demo"})
	defer sub.Cancel()

	msg := core.NormalizedMessage{
		TenantID:  "demo",
		Channel:   "simulator",
		SourceKey: "user-1",
		MessageID: "m-1",
		Timestamp: time.Date(2026, 11, 11, 14, 0, 0, 0, time.UTC),
		Direction: core.DirectionInbound,
		Type:      core.MessageTypeText,
		Text:      "Tenho orçamento de 2000, qual o plano?",
	}

	if err := p.Ingest(ctx, msg); err != nil {
		t.Fatalf("Ingest failed: %v", err)
	}

	// Aguarda evento reply.sent
	var eventTypes []core.EventType
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-sub.Events():
			eventTypes = append(eventTypes, ev.Type)
			if ev.Type == core.EventReplySent {
				goto done
			}
		case <-deadline:
			t.Fatalf("timeout aguardando EventReplySent; eventos recebidos: %v", eventTypes)
		}
	}
done:
	// Verifica estado atualizado no Store
	state, err := store.GetSourceState(ctx, "demo", "simulator", "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if state.Version != 1 || state.Facts["Budget"] != "2000" {
		t.Fatalf("unexpected state: %+v", state)
	}

	// Verifica sanitização PlainTextOnly na resposta enviada pelo canal
	sent := ch.SentMessages()
	if len(sent) != 1 {
		t.Fatalf("expected 1 sent message, got %d", len(sent))
	}
	if sent[0].Text != "Olá! Temos o Plano A por 2000.\nEntrega imediata!" {
		t.Fatalf("unexpected sanitized reply: %q", sent[0].Text)
	}

	// Verifica trace salvo no BlobStore
	runs, err := store.ListRuns(ctx, "demo", "simulator", "user-1", 10)
	if err != nil || len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d (err=%v)", len(runs), err)
	}
	traceBytes, err := blobs.Get(ctx, runs[0].TraceBlobRef)
	if err != nil {
		t.Fatalf("expected trace blob at %s: %v", runs[0].TraceBlobRef, err)
	}
	var trace brain.RunTrace
	if err := json.Unmarshal(traceBytes, &trace); err != nil {
		t.Fatal(err)
	}
	if trace.TotalTokens != 160 || len(trace.FactsDiff) != 1 || trace.FactsDiff[0].Action != "added" {
		t.Fatalf("unexpected trace details: %+v", trace)
	}

	// Deduplicação: reenvio do mesmo message_id deve emitir EventMessageDuplicate e não chamar LLM novamente
	if err := p.Ingest(ctx, msg); err != nil {
		t.Fatalf("duplicate Ingest failed: %v", err)
	}
	select {
	case ev := <-sub.Events():
		if ev.Type != core.EventMessageDuplicate {
			t.Fatalf("expected EventMessageDuplicate, got %s", ev.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for EventMessageDuplicate")
	}
	if len(llm.Calls()) != 2 {
		t.Fatalf("expected still 2 LLM calls after duplicate, got %d", len(llm.Calls()))
	}
}

func TestPipeline_SafeFallbackOnDoubleAnalysisFailure(t *testing.T) {
	ctx := context.Background()
	store := fakes.NewMemoryStore()
	llm := fakes.NewScriptedLLM(nil)
	llm.EnqueueStage("analysis", core.LLMResponse{Content: `invalid`})
	llm.EnqueueStage("analysis_repair", core.LLMResponse{Content: `still invalid`})

	p, _, bus, ch := setupPipelineTestEnv(t, store, llm)
	defer p.Stop()

	sub := bus.Subscribe(core.EventFilter{TenantID: "demo"})
	defer sub.Cancel()

	err := p.Ingest(ctx, core.NormalizedMessage{
		TenantID:  "demo",
		Channel:   "simulator",
		SourceKey: "user-2",
		MessageID: "m-fail",
		Timestamp: time.Now().UTC(),
		Direction: core.DirectionInbound,
		Type:      core.MessageTypeText,
		Text:      "Oi",
	})
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-sub.Events():
			if ev.Type == core.EventPipelineFailed {
				goto failedSeen
			}
		case <-deadline:
			t.Fatal("timeout waiting for EventPipelineFailed")
		}
	}
failedSeen:
	state, _ := store.GetSourceState(ctx, "demo", "simulator", "user-2")
	if state.Version != 0 {
		t.Fatalf("expected version 0 untouched on fallback, got %d", state.Version)
	}
	if len(ch.SentMessages()) != 0 {
		t.Fatalf("expected 0 replies sent on analysis fallback")
	}
	runs, _ := store.ListRuns(ctx, "demo", "simulator", "user-2", 10)
	if len(runs) != 1 || runs[0].Status != "failed" {
		t.Fatalf("expected 1 failed run, got %+v", runs)
	}
}

type conflictOnceStore struct {
	core.Store
	mu        sync.Mutex
	conflicted bool
}

func (c *conflictOnceStore) CommitAnalysis(ctx context.Context, input core.CommitAnalysisInput) error {
	c.mu.Lock()
	if !c.conflicted {
		c.conflicted = true
		c.mu.Unlock()
		return fmt.Errorf("%w: injected conflict", core.ErrVersionConflict)
	}
	c.mu.Unlock()
	return c.Store.CommitAnalysis(ctx, input)
}

func TestPipeline_VersionConflictRetries(t *testing.T) {
	ctx := context.Background()
	baseStore := fakes.NewMemoryStore()
	cs := &conflictOnceStore{Store: baseStore}

	llm := fakes.NewScriptedLLM(nil)
	validAnalysis := `{"summary":"Resumo ok.","last_sender":"customer","facts":{"Budget":1000},"signals":{},"context":{}}`
	llm.EnqueueStage("analysis",
		core.LLMResponse{Content: validAnalysis, PromptTokens: 10, CompletionTokens: 10},
		core.LLMResponse{Content: validAnalysis, PromptTokens: 10, CompletionTokens: 10},
	)
	llm.EnqueueStage("response", core.LLMResponse{Content: "Resposta ok", PromptTokens: 10, CompletionTokens: 10})

	p, _, bus, ch := setupPipelineTestEnv(t, cs, llm)
	defer p.Stop()

	sub := bus.Subscribe(core.EventFilter{TenantID: "demo"})
	defer sub.Cancel()

	_ = p.Ingest(ctx, core.NormalizedMessage{
		TenantID:  "demo",
		Channel:   "simulator",
		SourceKey: "user-conflict",
		MessageID: "m-c1",
		Timestamp: time.Now().UTC(),
		Direction: core.DirectionInbound,
		Type:      core.MessageTypeText,
		Text:      "Oi",
	})

	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-sub.Events():
			if ev.Type == core.EventReplySent {
				goto ok
			}
		case <-deadline:
			t.Fatal("timeout waiting for EventReplySent after version conflict retry")
		}
	}
ok:
	if len(ch.SentMessages()) != 1 {
		t.Fatalf("expected 1 sent reply after conflict retry, got %d", len(ch.SentMessages()))
	}
}
