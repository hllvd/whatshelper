package brain_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"whatshelper/internal/brain"
	"whatshelper/internal/core"
	"whatshelper/internal/core/fakes"
	"whatshelper/internal/pack"
)

func TestQA06_OutboxIdempotency_Backoff_DLQ_And_Reprocess(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 12, 19, 0, 0, 0, time.UTC)
	clock := fakes.NewFakeClock(now)
	st := fakes.NewMemoryStoreWithClock(clock)
	bus := core.NewInMemoryEventBus(32)

	var execCount atomic.Int32
	var shouldFail atomic.Bool
	shouldFail.Store(true)

	worker := brain.NewOutboxWorker(st, nil, bus, clock.Now)
	worker.RegisterHandler("custom_webhook", func(context.Context, core.OutboxItem) error {
		execCount.Add(1)
		if shouldFail.Load() {
			return errors.New("upstream 503")
		}
		return nil
	})

	item := core.OutboxItem{
		ID:            "run-1#0",
		TenantID:      "t1",
		Channel:       "whatsapp",
		SourceKey:     "5511999990010",
		RunID:         "run-1",
		Index:         0,
		ActionType:    "custom_webhook",
		Status:        core.OutboxStatusPending,
		MaxAttempts:   2,
		NextAttemptAt: now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := st.CommitJourneyTurn(ctx, core.CommitJourneyTurnInput{
		TenantID:  "t1",
		Channel:   "whatsapp",
		SourceKey: "5511999990010",
		Journey: core.JourneyState{
			TenantID:  "t1",
			Channel:   "whatsapp",
			SourceKey: "5511999990010",
			State:     "start",
			EnteredAt: now,
			Version:   1,
			UpdatedAt: now,
		},
		OutboxItems: []core.OutboxItem{item},
	}); err != nil {
		t.Fatalf("CommitJourneyTurn: %v", err)
	}

	// Tentativa 1: falha -> status=failed com backoff
	if _, err := worker.ProcessDue(ctx, "t1", 10); err != nil {
		t.Fatalf("ProcessDue attempt 1: %v", err)
	}
	got, _ := st.GetOutboxItem(ctx, "t1", "whatsapp", "5511999990010", "run-1#0")
	if got.Status != core.OutboxStatusFailed || got.Attempts != 1 || !got.NextAttemptAt.After(now) {
		t.Fatalf("expected failed with backoff after attempt 1, got %+v", got)
	}

	// Antes do backoff expirar, não deve reexecutar
	_, _ = worker.ProcessDue(ctx, "t1", 10)
	if execCount.Load() != 1 {
		t.Fatalf("expected 1 execution before backoff expires, got %d", execCount.Load())
	}

	// Avança relógio -> Tentativa 2: falha -> status=dead (DLQ)
	clock.Advance(10 * time.Second)
	_, _ = worker.ProcessDue(ctx, "t1", 10)
	got, _ = st.GetOutboxItem(ctx, "t1", "whatsapp", "5511999990010", "run-1#0")
	if got.Status != core.OutboxStatusDead || got.Attempts != 2 {
		t.Fatalf("expected dead after max_attempts=2, got %+v", got)
	}

	// Reprocessamento manual após correção do upstream -> status=done
	shouldFail.Store(false)
	if err := worker.ReprocessItem(ctx, "t1", "whatsapp", "5511999990010", "run-1#0"); err != nil {
		t.Fatalf("ReprocessItem: %v", err)
	}
	got, _ = st.GetOutboxItem(ctx, "t1", "whatsapp", "5511999990010", "run-1#0")
	if got.Status != core.OutboxStatusDone {
		t.Fatalf("expected done after ReprocessItem, got %+v", got)
	}

	// Idempotência: despachar novamente item já "done" não executa o handler de novo
	before := execCount.Load()
	if err := worker.DispatchItem(ctx, *got); err != nil {
		t.Fatalf("DispatchItem on done: %v", err)
	}
	if execCount.Load() != before {
		t.Fatalf("expected idempotent skip on done item")
	}
}

func TestQA06_CommitJourneyTurn_ExpectedVersionConflictIsAtomic(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 12, 19, 30, 0, 0, time.UTC)
	st := fakes.NewMemoryStore()

	// Commit inicial v1
	if err := st.CommitJourneyTurn(ctx, core.CommitJourneyTurnInput{
		TenantID:        "t1",
		Channel:         "whatsapp",
		SourceKey:       "u1",
		ExpectedVersion: 0,
		Journey: core.JourneyState{
			TenantID:  "t1",
			Channel:   "whatsapp",
			SourceKey: "u1",
			State:     "start",
			EnteredAt: now,
			Version:   1,
			UpdatedAt: now,
		},
	}); err != nil {
		t.Fatalf("initial CommitJourneyTurn: %v", err)
	}

	// Commit com ExpectedVersion desatualizado (0 em vez de 1) deve falhar sem gravar outbox
	err := st.CommitJourneyTurn(ctx, core.CommitJourneyTurnInput{
		TenantID:        "t1",
		Channel:         "whatsapp",
		SourceKey:       "u1",
		ExpectedVersion: 0,
		Journey: core.JourneyState{
			TenantID:  "t1",
			Channel:   "whatsapp",
			SourceKey: "u1",
			State:     "qualified",
			EnteredAt: now,
			Version:   2,
			UpdatedAt: now,
		},
		OutboxItems: []core.OutboxItem{{
			ID: "bad-run#0", TenantID: "t1", Channel: "whatsapp", SourceKey: "u1",
			ActionType: "create_task", Status: core.OutboxStatusPending,
		}},
	})
	if !errors.Is(err, core.ErrVersionConflict) {
		t.Fatalf("expected ErrVersionConflict, got %v", err)
	}
	if obx, _ := st.GetOutboxItem(ctx, "t1", "whatsapp", "u1", "bad-run#0"); obx != nil {
		t.Fatalf("outbox item must NOT be persisted on version conflict")
	}
}

func TestQA06_Pipeline_Journey_Directive_And_SystemTimeout(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 12, 20, 0, 0, 0, time.UTC)
	clock := fakes.NewFakeClock(now)
	st := fakes.NewMemoryStoreWithClock(clock)

	tmp := t.TempDir()
	packDir := filepath.Join(tmp, "packs", "generic")
	_ = os.MkdirAll(filepath.Join(packDir, "prompts"), 0o755)
	_ = os.WriteFile(filepath.Join(packDir, "pack.yaml"), []byte(gatePackYAML), 0o644)
	_ = os.WriteFile(filepath.Join(packDir, "prompts", "analysis.tmpl"), []byte("Analyze\n{{.OutputSchema}}\n<!-- USER_PROMPT -->\n{{.NewMessage.Text}}"), 0o644)
	_ = os.WriteFile(filepath.Join(packDir, "prompts", "reply.tmpl"), []byte("Reply\n<!-- USER_PROMPT -->\n{{.NewMessage.Text}}"), 0o644)
	_ = os.WriteFile(filepath.Join(packDir, "journey.yaml"), []byte(`
version: "1"
initial_state: start
states:
  start:
    rules:
      - id: qualify_budget
        priority: 10
        on_event: user_message
        guard: 'HasFact("Budget")'
        to: qualified
        actions:
          - type: create_task
            params:
              type: follow_up
              title: "Acompanhar orçamento"
        directive:
          goal: "Confirmar orçamento e avançar para próxima etapa."
          must_ask: ["Qual prazo você prefere?"]
          constraints: ["Não invente valores."]
  qualified:
    rules:
      - id: qualified_timeout
        priority: 10
        on_event: system_timeout
        to: dormant
  dormant:
    terminal: true
`), 0o644)

	kDir := filepath.Join(tmp, "tenants", "t1", "knowledge")
	_ = os.MkdirAll(kDir, 0o755)
	_ = os.WriteFile(filepath.Join(kDir, "info.md"), []byte("Plano custa 100 reais."), 0o644)

	if err := brain.SeedTenantPolicy(ctx, st, core.TenantPolicy{
		ID:         "t1",
		Pack:       "generic",
		BotEnabled: true,
	}); err != nil {
		t.Fatalf("SeedTenantPolicy: %v", err)
	}

	var capturedReplySystem string
	llm := fakes.NewScriptedLLM(func(_ context.Context, req core.LLMRequest) (*core.LLMResponse, error) {
		if req.Model == "m-a" {
			return &core.LLMResponse{
				Content: `{"summary":"Cliente informou orçamento 5000.","last_sender":"customer","facts":{"Budget":"5000"},"signals":{"has_new_question":false},"context":{}}`,
			}, nil
		}
		capturedReplySystem = req.System
		return &core.LLMResponse{Content: "Entendi seu orçamento! Qual prazo você prefere?"}, nil
	})

	ch := fakes.NewMemoryChannel("simulator", 8)
	_ = ch.Start(ctx)

	p := brain.NewPipeline(ctx, brain.PipelineConfig{
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
	})
	p.RegisterChannel(ch)

	err := p.ProcessBatch(ctx, []core.NormalizedMessage{{
		TenantID:  "t1",
		Channel:   "simulator",
		SourceKey: "u-jrn",
		MessageID: "m-1",
		Timestamp: now,
		Direction: core.DirectionInbound,
		Type:      core.MessageTypeText,
		Text:      "Meu orçamento é 5000",
	}})
	if err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}

	// 1. Verifica estado da jornada persistido em 'qualified'
	js, err := st.GetJourneyState(ctx, "t1", "simulator", "u-jrn")
	if err != nil || js == nil || js.State != "qualified" {
		t.Fatalf("expected journey state 'qualified', got %+v err=%v", js, err)
	}

	// 2. Verifica diretiva do FSM injetada no prompt de resposta
	if !strings.Contains(capturedReplySystem, "RESPONSE DIRECTIVE (FSM):") || !strings.Contains(capturedReplySystem, "Qual prazo você prefere?") {
		t.Fatalf("expected FSM ResponseDirective in reply system prompt, got:\n%s", capturedReplySystem)
	}

	// 3. Verifica tarefa criada pelo outbox
	tasks, err := st.ListSourceTasks(ctx, "t1", "simulator", "u-jrn")
	if err != nil || len(tasks) == 0 {
		t.Fatalf("expected outbox create_task to persist task, got len=%d err=%v", len(tasks), err)
	}

	// 4. EVT-02: Emite system_timeout -> transiciona para 'dormant'
	outSys, err := p.EmitSystemEvent(ctx, "t1", "simulator", "u-jrn", core.SystemEvent{
		Type: core.SysEventSystemTimeout,
	})
	if err != nil {
		t.Fatalf("EmitSystemEvent: %v", err)
	}
	if outSys == nil || outSys.NextState != "dormant" {
		t.Fatalf("expected transition to 'dormant' on system_timeout, got %+v", outSys)
	}
}
