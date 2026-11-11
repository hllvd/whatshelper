package pack_test

import (
	"path/filepath"
	"strings"
	"testing"

	"whatshelper/internal/pack"
)

func TestLoadRealestatePackAndRender(t *testing.T) {
	packDir := filepath.Join("..", "..", "packs", "realestate")
	p, err := pack.LoadFromDir(packDir)
	if err != nil {
		t.Fatalf("failed to load realestate pack: %v", err)
	}

	if p.Name != "realestate" || p.Language != "pt-BR" {
		t.Fatalf("unexpected pack metadata: %+v", p)
	}
	if len(p.Facts) != 16 {
		t.Fatalf("expected 16 facts in realestate pack, got %d", len(p.Facts))
	}
	if len(p.Signals) != 6 {
		t.Fatalf("expected 6 signals in realestate pack, got %d", len(p.Signals))
	}
	if len(p.Context) != 4 {
		t.Fatalf("expected 4 context sections in realestate pack, got %d", len(p.Context))
	}

	input := pack.PromptInput{
		Summary: "Cliente iniciou contato buscando opções.",
		Facts: map[string]string{
			"Budget": "500000",
		},
		RecentMessages: []pack.PromptMessage{
			{Sender: "customer", Text: "Oi, tudo bem?", Timestamp: "2026-11-11T10:00:00Z"},
		},
		NewMessage: pack.PromptMessage{
			Sender:    "customer",
			Text:      "Quais são as opções disponíveis até 500 mil?",
			Timestamp: "2026-11-11T10:01:00Z",
		},
		Knowledge: []pack.KnowledgeDoc{
			{Name: "catalogo.md", Content: "Catálogo de teste"},
		},
		QAContent:     "P: Horário? R: 9h às 18h.",
		PlainTextOnly: true,
	}

	// 1. RenderAnalysis
	analysisPrompt, err := pack.RenderAnalysis(p, input)
	if err != nil {
		t.Fatalf("RenderAnalysis failed: %v", err)
	}
	if !strings.Contains(analysisPrompt.System, `"Budget"`) {
		t.Fatalf("expected schema with Budget in analysis system prompt")
	}
	if !strings.Contains(analysisPrompt.User, "Budget: 500000") {
		t.Fatalf("expected known fact Budget in analysis user prompt")
	}
	if !strings.Contains(analysisPrompt.User, "Quais são as opções disponíveis") {
		t.Fatalf("expected new message in analysis user prompt")
	}

	// Determinismo: segunda execução deve gerar saída idêntica
	analysisPrompt2, err := pack.RenderAnalysis(p, input)
	if err != nil {
		t.Fatalf("second RenderAnalysis failed: %v", err)
	}
	if analysisPrompt.System != analysisPrompt2.System || analysisPrompt.User != analysisPrompt2.User {
		t.Fatalf("RenderAnalysis is not deterministic")
	}

	// 2. RenderReply
	replyPrompt, err := pack.RenderReply(p, input)
	if err != nil {
		t.Fatalf("RenderReply failed: %v", err)
	}
	if !strings.Contains(replyPrompt.System, "catalogo.md") {
		t.Fatalf("expected knowledge doc in reply system prompt")
	}
	if !strings.Contains(replyPrompt.System, "P: Horário? R: 9h às 18h.") {
		t.Fatalf("expected QA.md content in reply system prompt")
	}
	if !strings.Contains(replyPrompt.System, "sem formatação Markdown") {
		t.Fatalf("expected plain text instruction when PlainTextOnly=true")
	}

	// 3. RenderNudge
	nudgePrompt, err := pack.RenderNudge(p, input)
	if err != nil {
		t.Fatalf("RenderNudge failed: %v", err)
	}
	if !strings.Contains(nudgePrompt.User, "Cliente iniciou contato") {
		t.Fatalf("expected summary in nudge user prompt")
	}
}

func TestLoadSupportPackAndRender(t *testing.T) {
	p, err := pack.LoadFromDir(filepath.Join("..", "..", "packs", "support"))
	if err != nil {
		t.Fatalf("LoadFromDir(support) failed: %v", err)
	}
	if p.Name != "support" || p.Actors.Business != "agent" || p.Actors.Customer != "user" {
		t.Fatalf("unexpected support pack metadata: %+v", p)
	}
	if len(p.Intents) == 0 || len(p.TaskRules) == 0 {
		t.Fatalf("expected intents and task_rules on support pack")
	}

	input := pack.PromptInput{
		Summary: "Usuário relata erro 502 no plano pro.",
		Facts:   map[string]string{"Plan": "pro", "ErrorCode": "502"},
		Signals: map[string]bool{"has_pending_bug": true},
		NewMessage: pack.PromptMessage{
			Sender:    "user",
			Text:      "Ainda dá erro 502 na exportação.",
			Timestamp: "2026-11-11T14:00:00Z",
		},
		Knowledge:     []pack.KnowledgeDoc{{Name: "troubleshooting.md", Content: "Use /v2/exports acima de 50000 linhas."}},
		PlainTextOnly: true,
	}

	ap, err := pack.RenderAnalysis(p, input)
	if err != nil || !strings.Contains(ap.System, `"Plan"`) || !strings.Contains(ap.System, `"report_bug"`) {
		t.Fatalf("RenderAnalysis(support) unexpected: err=%v sys=%q", err, ap.System)
	}
	rp, err := pack.RenderReply(p, input)
	if err != nil || !strings.Contains(rp.System, "troubleshooting.md") {
		t.Fatalf("RenderReply(support) unexpected: err=%v", err)
	}
	np, err := pack.RenderNudge(p, input)
	if err != nil || !strings.Contains(np.User, "erro 502") {
		t.Fatalf("RenderNudge(support) unexpected: err=%v", err)
	}
}
