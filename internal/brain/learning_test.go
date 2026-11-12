package brain_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"whatshelper/internal/brain"
	"whatshelper/internal/core"
	"whatshelper/internal/core/fakes"
	"whatshelper/internal/pack"
	"whatshelper/internal/store"
)

const learningPackYAML = `
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
intents:
  - name: ask_info
    description: Pergunta geral
    action: reply
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
`

func setupLearningEnv(t *testing.T, clock *fakes.FakeClock, llm core.LLM) (*brain.Pipeline, *fakes.MemoryStore, *fakes.MemoryBlobStore, *core.InMemoryEventBus, *fakes.MemoryChannel, *fakes.MemoryChannel) {
	t.Helper()
	tmp := t.TempDir()
	packDir := filepath.Join(tmp, "packs", "generic")
	_ = os.MkdirAll(filepath.Join(packDir, "prompts"), 0o755)
	_ = os.WriteFile(filepath.Join(packDir, "pack.yaml"), []byte(learningPackYAML), 0o644)
	_ = os.WriteFile(filepath.Join(packDir, "prompts", "analysis.tmpl"), []byte("Analyze\n{{.OutputSchema}}\n<!-- USER_PROMPT -->\n{{.NewMessage.Text}}"), 0o644)
	_ = os.WriteFile(filepath.Join(packDir, "prompts", "reply.tmpl"), []byte("Reply using QA:\n{{.QAContent}}\n<!-- USER_PROMPT -->\n{{.NewMessage.Text}}"), 0o644)

	st := fakes.NewMemoryStoreWithClock(clock)
	blobs := fakes.NewMemoryBlobStore()
	bus := core.NewInMemoryEventBus(64)

	_ = brain.SeedTenantPolicy(context.Background(), st, core.TenantPolicy{
		ID:         "t1",
		Pack:       "generic",
		Mode:       core.ModeAuto,
		BotEnabled: true,
	}.WithDefaults())
	_ = blobs.Put(context.Background(), store.S3KnowledgeKey("t1", "QA.md"), []byte("# QA\n"), "text/markdown")

	simCh := fakes.NewMemoryChannel("simulator", 16)
	adminCh := fakes.NewMemoryChannel("admin", 16)
	_ = simCh.Start(context.Background())
	_ = adminCh.Start(context.Background())

	ks := brain.NewBlobKnowledgeSource(blobs, clock, 60*time.Second)

	p := brain.NewPipeline(context.Background(), brain.PipelineConfig{
		Store:         st,
		Blobs:         blobs,
		LLM:           llm,
		Bus:           bus,
		Clock:         clock,
		Tenants:       brain.NewStoreTenantPolicyLoader(st, nil),
		Knowledge:     ks,
		PackLoader:    func(name string) (*pack.Pack, error) { return pack.LoadFromDir(filepath.Join(tmp, "packs", name)) },
		AnalysisModel: "m-a",
		ResponseModel: "m-r",
		LearningModel: "m-l",
		Debounce:      time.Hour,
	})
	p.RegisterChannel(simCh)
	p.RegisterChannel(adminCh)
	return p, st, blobs, bus, simCh, adminCh
}

func TestLearningLifecycle_EscalateProposeRejectApproveResume(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 11, 14, 0, 0, 0, time.UTC)
	clock := fakes.NewFakeClock(now)

	llm := fakes.NewScriptedLLM(nil)
	// 1. Cliente faz pergunta sem cobertura -> resposta indica answerable=false -> escalona
	llm.EnqueueStage("analysis", core.LLMResponse{
		Content: `{"summary":"Cliente perguntou sobre estacionamento.","last_sender":"customer","intent":"ask_info","facts":{},"signals":{"has_new_question":true},"context":{}}`,
	})
	llm.EnqueueStage("response", core.LLMResponse{
		Content: `{"answerable":false,"sources":[],"reply":"Vou confirmar com a equipe."}`,
	})

	p, _, blobs, bus, simCh, adminCh := setupLearningEnv(t, clock, llm)
	defer p.Stop()

	sub := bus.Subscribe(core.EventFilter{TenantID: "t1"})
	defer sub.Cancel()

	inMsg := core.NormalizedMessage{
		TenantID: "t1", Channel: "simulator", SourceKey: "u-learn",
		MessageID: "m1", Timestamp: now, Direction: core.DirectionInbound, Type: core.MessageTypeText,
		Text: "Vocês têm estacionamento gratuito?",
	}
	if err := p.Ingest(ctx, inMsg); err != nil {
		t.Fatal(err)
	}
	if err := p.ProcessBatch(ctx, []core.NormalizedMessage{inMsg}); err != nil {
		t.Fatal(err)
	}

	if len(simCh.SentMessages()) != 1 || simCh.SentMessages()[0].Text != brain.NeutralFallbackReply {
		t.Fatalf("expected neutral fallback to customer, got %+v", simCh.SentMessages())
	}
	if len(adminCh.SentMessages()) != 1 || !strings.Contains(adminCh.SentMessages()[0].Text, "Vocês têm estacionamento gratuito?") {
		t.Fatalf("expected escalation notice in admin channel, got %+v", adminCh.SentMessages())
	}

	// 2. Dono responde no canal admin -> LLM gera proposta de QA.md (LRN-02)
	llm.EnqueueStage("learning", core.LLMResponse{
		Content: `{"question":"Vocês têm estacionamento gratuito?","answer":"Sim, clientes têm 2 horas grátis no subsolo.","action":"add","target_question":""}`,
	})
	clock.Advance(10 * time.Second)
	adminReply := core.NormalizedMessage{
		TenantID: "t1", Channel: "admin", SourceKey: "owner",
		MessageID: "adm-in-1", Timestamp: clock.Now(), Direction: core.DirectionInbound, Type: core.MessageTypeText,
		Text: "Sim, 2 horas grátis no subsolo para clientes.",
	}
	if err := p.Ingest(ctx, adminReply); err != nil {
		t.Fatal(err)
	}

	pending, err := p.ListPendingLearningProposals(ctx, "t1")
	if err != nil || len(pending) != 1 {
		t.Fatalf("expected 1 pending learning proposal, got %+v err=%v", pending, err)
	}

	// Antes da aprovação, QA.md ainda não contém o novo conteúdo
	rawBefore, _ := blobs.Get(ctx, store.S3KnowledgeKey("t1", "QA.md"))
	if strings.Contains(string(rawBefore), "subsolo") {
		t.Fatalf("QA.md must not be modified before owner approval")
	}

	// 3. Testa rejeição sem gravar em QA.md (LRN-03)
	if _, err := p.RejectLearningProposal(ctx, "t1", pending[0].ID); err != nil {
		t.Fatalf("RejectLearningProposal failed: %v", err)
	}
	rawAfterReject, _ := blobs.Get(ctx, store.S3KnowledgeKey("t1", "QA.md"))
	if strings.Contains(string(rawAfterReject), "subsolo") {
		t.Fatalf("QA.md must remain unchanged after rejection")
	}

	// 4. Novo escalonamento + proposta + aprovação com edição e retomada ao cliente (LRN-03, LRN-04, LRN-05)
	// Simula cliente ainda esperando resposta, dono cria outra proposta via correção
	llm.EnqueueStage("learning", core.LLMResponse{
		Content: `{"question":"Vocês têm estacionamento gratuito?","answer":"Sim, 2 horas grátis no subsolo.","action":"add","target_question":""}`,
	})
	prop2, err := p.ProposeLearningCorrection(ctx, brain.CorrectReplyRequest{
		TenantID:   "t1",
		Channel:    "simulator",
		SourceKey:  "u-learn",
		Correction: "Sim, oferecemos 2 horas grátis no subsolo.",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Resposta retomada usando o QA.md recarregado (hot reload!)
	llm.EnqueueStage("response", core.LLMResponse{
		Content: `{"answerable":true,"sources":["QA.md"],"reply":"Confirmando: temos estacionamento gratuito por 2 horas no subsolo!"}`,
	})

	clock.Advance(10 * time.Second)
	approved, err := p.ApproveLearningProposal(ctx, brain.ApproveLearningRequest{
		TenantID:       "t1",
		ProposalID:     prop2.ID,
		Answer:         "Sim, oferecemos 2 horas de estacionamento gratuito no subsolo mediante validação na recepção.",
		ResumeCustomer: true,
	})
	if err != nil {
		t.Fatalf("ApproveLearningProposal failed: %v", err)
	}
	if approved.Status != "approved" || approved.MergeOutcome != "added" {
		t.Fatalf("unexpected approved proposal: %+v", approved)
	}
	if approved.ResumedReply == "" || len(simCh.SentMessages()) != 2 {
		t.Fatalf("expected resumed reply sent to customer, got resumed=%q sent=%+v", approved.ResumedReply, simCh.SentMessages())
	}

	// Confirma versão gravada em QA.md e BlobStore versionado
	rawAfterApprove, _ := blobs.Get(ctx, store.S3KnowledgeKey("t1", "QA.md"))
	if !strings.Contains(string(rawAfterApprove), "validação na recepção") {
		t.Fatalf("expected edited answer saved in QA.md, got:\n%s", rawAfterApprove)
	}
	versions, err := blobs.ListVersions(ctx, store.S3KnowledgeKey("t1", "QA.md"))
	if err != nil || len(versions) < 2 {
		t.Fatalf("expected multiple S3 versions for QA.md, got %+v err=%v", versions, err)
	}

	// Verifica que EventLearned foi publicado
	foundLearned := false
	for len(sub.Events()) > 0 {
		ev := <-sub.Events()
		if ev.Type == core.EventLearned {
			foundLearned = true
		}
	}
	if !foundLearned {
		t.Fatalf("expected EventLearned published")
	}
}

func TestAdminCommand_CorrectAndApproveFlow(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 11, 15, 0, 0, 0, time.UTC)
	clock := fakes.NewFakeClock(now)
	llm := fakes.NewOfflineDemoLLM()

	p, _, blobs, _, simCh, adminCh := setupLearningEnv(t, clock, llm)
	defer p.Stop()

	// Cliente pergunta e bot responde errado primeiro
	msgIn := core.NormalizedMessage{
		TenantID: "t1", Channel: "simulator", SourceKey: "u-corr",
		MessageID: "m1", Timestamp: now, Direction: core.DirectionInbound, Type: core.MessageTypeText,
		Text: "Qual o prazo de entrega?",
	}
	if err := p.Ingest(ctx, msgIn); err != nil {
		t.Fatal(err)
	}
	if err := p.ProcessBatch(ctx, []core.NormalizedMessage{msgIn}); err != nil {
		t.Fatal(err)
	}
	if len(simCh.SentMessages()) != 1 {
		t.Fatalf("expected 1 initial reply")
	}

	// Operador usa /corrigir no canal admin (LRN-06)
	clock.Advance(5 * time.Second)
	corrCmd := core.NormalizedMessage{
		TenantID: "t1", Channel: "admin", SourceKey: "owner",
		MessageID: "adm-corr", Timestamp: clock.Now(), Direction: core.DirectionInbound, Type: core.MessageTypeText,
		Text: "/corrigir simulator:u-corr | O prazo padrão é de 5 dias úteis.",
	}
	if err := p.Ingest(ctx, corrCmd); err != nil {
		t.Fatal(err)
	}

	pending, _ := p.ListPendingLearningProposals(ctx, "t1")
	if len(pending) != 1 || !strings.Contains(pending[0].WrongReply, "modo offline") {
		t.Fatalf("expected proposal capturing wrong reply context, got %+v", pending)
	}

	// Operador aprova via comando /aprovar (sem precisar digitar o ID)
	clock.Advance(5 * time.Second)
	apprCmd := core.NormalizedMessage{
		TenantID: "t1", Channel: "admin", SourceKey: "owner",
		MessageID: "adm-appr", Timestamp: clock.Now(), Direction: core.DirectionInbound, Type: core.MessageTypeText,
		Text: "/aprovar",
	}
	if err := p.Ingest(ctx, apprCmd); err != nil {
		t.Fatal(err)
	}

	qaBytes, _ := blobs.Get(ctx, store.S3KnowledgeKey("t1", "QA.md"))
	if !strings.Contains(string(qaBytes), "5 dias úteis") {
		t.Fatalf("expected QA.md updated after /aprovar, got %q", string(qaBytes))
	}
	if len(adminCh.SentMessages()) < 2 {
		t.Fatalf("expected confirmation reply in admin channel")
	}
}
