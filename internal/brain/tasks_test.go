package brain_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"whatshelper/internal/brain"
	"whatshelper/internal/core"
	"whatshelper/internal/core/fakes"
	"whatshelper/internal/pack"
)

const taskPackYAML = `
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
  - key: has_pending_meeting
    description: Reunião pendente
intents:
  - name: ask_info
    description: Pergunta geral
    action: reply
  - name: book_meeting
    description: Agendar reunião
    action: create_task
task_rules:
  - type: schedule_meeting
    title: "Confirmar reunião solicitada"
    trigger_signal: has_pending_meeting
    trigger_intent: book_meeting
    context_section: meeting
    due_in_minutes: 30
context:
  - key: meeting
    fields:
      - key: proposed_date
        type: string
prompts:
  analysis: prompts/analysis.tmpl
  reply: prompts/reply.tmpl
`

func setupTasksPipeline(t *testing.T, mode core.OperationMode, clock *fakes.FakeClock, llm core.LLM) (*brain.Pipeline, *fakes.MemoryStore, *fakes.MemoryBlobStore, *fakes.MemoryChannel) {
	t.Helper()
	tmp := t.TempDir()
	packDir := filepath.Join(tmp, "packs", "generic")
	_ = os.MkdirAll(filepath.Join(packDir, "prompts"), 0o755)
	_ = os.WriteFile(filepath.Join(packDir, "pack.yaml"), []byte(taskPackYAML), 0o644)
	_ = os.WriteFile(filepath.Join(packDir, "prompts", "analysis.tmpl"), []byte("Analyze\n{{.OutputSchema}}\n<!-- USER_PROMPT -->\n{{.NewMessage.Text}}"), 0o644)
	_ = os.WriteFile(filepath.Join(packDir, "prompts", "reply.tmpl"), []byte("Reply\n<!-- USER_PROMPT -->\n{{.NewMessage.Text}}"), 0o644)

	kDir := filepath.Join(tmp, "tenants", "t1", "knowledge")
	_ = os.MkdirAll(kDir, 0o755)
	_ = os.WriteFile(filepath.Join(kDir, "info.md"), []byte("Atendemos reuniões de segunda a sexta."), 0o644)

	memStore := fakes.NewMemoryStoreWithClock(clock)
	blobs := fakes.NewMemoryBlobStore()
	_ = brain.SeedTenantPolicy(context.Background(), memStore, core.TenantPolicy{
		ID:         "t1",
		Pack:       "generic",
		Mode:       mode,
		BotEnabled: true,
	}.WithDefaults())

	ch := fakes.NewMemoryChannel("simulator", 16)
	_ = ch.Start(context.Background())

	p := brain.NewPipeline(context.Background(), brain.PipelineConfig{
		Store:         memStore,
		Blobs:         blobs,
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
	return p, memStore, blobs, ch
}

func TestDeriveTasks_AndDeduplication(t *testing.T) {
	pk, err := pack.ParsePackYAML([]byte(taskPackYAML))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 11, 14, 0, 0, 0, time.UTC)
	analysis := &pack.AnalysisResult{
		Intent:  "book_meeting",
		Signals: map[string]bool{"has_pending_meeting": true},
		Context: map[string]map[string]string{
			"meeting": {"proposed_date": "sexta"},
		},
	}

	tasks1 := brain.DeriveTasks(brain.DeriveTasksInput{
		Pack: pk, Analysis: analysis, TenantID: "t1", Channel: "simulator", SourceKey: "u1", MessageID: "m1", Now: now,
	})
	tasks2 := brain.DeriveTasks(brain.DeriveTasksInput{
		Pack: pk, Analysis: analysis, TenantID: "t1", Channel: "simulator", SourceKey: "u1", MessageID: "m1", Now: now,
	})
	if len(tasks1) != 1 || tasks1[0].ID != tasks2[0].ID {
		t.Fatalf("expected deterministic single task, got %+v vs %+v", tasks1, tasks2)
	}
	if tasks1[0].Metadata["meeting.proposed_date"] != "sexta" {
		t.Fatalf("expected context metadata preserved, got %+v", tasks1[0].Metadata)
	}

	filtered := brain.FilterNewTasks(tasks1, tasks1)
	if len(filtered) != 0 {
		t.Fatalf("expected existing pending task to deduplicate, got %+v", filtered)
	}
}

func TestPipeline_ApprovalMode_And_ApproveTask(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 11, 14, 0, 0, 0, time.UTC)
	clock := fakes.NewFakeClock(now)

	llm := fakes.NewScriptedLLM(nil)
	llm.EnqueueStage("analysis", core.LLMResponse{
		Content: `{"summary":"Quer agendar reunião na sexta.","last_sender":"customer","intent":"book_meeting","facts":{},"signals":{"has_pending_meeting":true},"context":{"meeting":{"proposed_date":"sexta"}}}`,
	})
	llm.EnqueueStage("response", core.LLMResponse{
		Content: `{"answerable":true,"sources":["info.md"],"reply":"Confirmado, vamos organizar a reunião para sexta!"}`,
	})

	p, st, blobs, ch := setupTasksPipeline(t, core.ModeApproval, clock, llm)
	defer p.Stop()

	msg := core.NormalizedMessage{
		TenantID: "t1", Channel: "simulator", SourceKey: "u-appr",
		MessageID: "m1", Timestamp: now, Direction: core.DirectionInbound, Type: core.MessageTypeText, Text: "Podemos reunir sexta?",
	}
	if err := p.ProcessBatch(ctx, []core.NormalizedMessage{msg}); err != nil {
		t.Fatal(err)
	}

	// Em modo approval, nada foi enviado ainda pelo canal
	if len(ch.SentMessages()) != 0 {
		t.Fatalf("expected 0 sent messages in approval mode, got %d", len(ch.SentMessages()))
	}

	tasks, err := st.ListSourceTasks(ctx, "t1", "simulator", "u-appr")
	if err != nil {
		t.Fatal(err)
	}
	var apprTask core.Task
	var ruleTasks int
	for _, tk := range tasks {
		if tk.Kind == "approval" {
			apprTask = tk
		} else if tk.Type == "schedule_meeting" {
			ruleTasks++
		}
	}
	if apprTask.ID == "" || apprTask.DraftBlobRef == "" || ruleTasks != 1 {
		t.Fatalf("expected 1 approval task with draft + 1 rule task, got tasks=%+v", tasks)
	}

	// Verifica rascunho no BlobStore
	rawDraft, err := blobs.Get(ctx, apprTask.DraftBlobRef)
	if err != nil {
		t.Fatal(err)
	}
	var draft brain.DraftPayload
	_ = json.Unmarshal(rawDraft, &draft)
	if draft.Text != "Confirmado, vamos organizar a reunião para sexta!" {
		t.Fatalf("unexpected draft text: %q", draft.Text)
	}

	// Operador aprova editando ligeiramente o texto
	approved, err := p.ApproveTask(ctx, "t1", "simulator", "u-appr", apprTask.ID, "Confirmado para sexta às 10h!")
	if err != nil {
		t.Fatalf("ApproveTask failed: %v", err)
	}
	if approved.Status != core.TaskStatusCompleted {
		t.Fatalf("expected completed status, got %s", approved.Status)
	}
	if len(ch.SentMessages()) != 1 || ch.SentMessages()[0].Text != "Confirmado para sexta às 10h!" {
		t.Fatalf("expected approved message delivered, got %+v", ch.SentMessages())
	}
}

func TestPipeline_DraftMode_DoesNotSendOrCreateApprovalTask(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 11, 14, 0, 0, 0, time.UTC)
	clock := fakes.NewFakeClock(now)
	llm := fakes.NewOfflineDemoLLM()

	p, st, _, ch := setupTasksPipeline(t, core.ModeDraft, clock, llm)
	defer p.Stop()

	msg := core.NormalizedMessage{
		TenantID: "t1", Channel: "simulator", SourceKey: "u-draft",
		MessageID: "m1", Timestamp: now, Direction: core.DirectionInbound, Type: core.MessageTypeText, Text: "Qual o horário?",
	}
	if err := p.ProcessBatch(ctx, []core.NormalizedMessage{msg}); err != nil {
		t.Fatal(err)
	}
	if len(ch.SentMessages()) != 0 {
		t.Fatalf("expected 0 sent messages in draft mode")
	}
	runs, err := st.ListRuns(ctx, "t1", "simulator", "u-draft", 5)
	if err != nil || len(runs) != 1 || runs[0].Status != "draft_only" {
		t.Fatalf("expected draft_only run, got %+v err=%v", runs, err)
	}
}
