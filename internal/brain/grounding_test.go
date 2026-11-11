package brain_test

import (
	"context"
	"testing"
	"time"

	"whatshelper/internal/brain"
	"whatshelper/internal/core"
	"whatshelper/internal/core/fakes"
	"whatshelper/internal/pack"
)

func TestParseReplyOutput_StructuredAndPlain(t *testing.T) {
	sr, structured := brain.ParseReplyOutput(`{"answerable": true, "sources": ["faq.md"], "reply": "Custa R$ 200."}`)
	if !structured || !sr.Answerable || len(sr.Sources) != 1 || sr.Reply != "Custa R$ 200." {
		t.Fatalf("unexpected structured parse: %+v structured=%v", sr, structured)
	}

	sr2, structured2 := brain.ParseReplyOutput("Olá! Como posso ajudar?")
	if structured2 || !sr2.Answerable || sr2.Reply != "Olá! Como posso ajudar?" {
		t.Fatalf("unexpected plain parse: %+v structured=%v", sr2, structured2)
	}
}

func TestCheckGrounding(t *testing.T) {
	kb := brain.KnowledgeBundle{
		Docs: []pack.KnowledgeDoc{
			{Name: "catalogo.md", Content: "Plano Enterprise custa R$ 500.000,00 por ano e inclui 10 usuários."},
		},
		QAContent: "P: Tem desconto?\nR: Até 5% à vista.",
	}
	newMsg := pack.PromptMessage{Sender: "customer", Text: "Tenho orçamento de 3000."}
	facts := map[string]string{"Budget": "3000"}

	t.Run("grounded structured reply passes", func(t *testing.T) {
		res := brain.CheckGrounding(brain.StructuredReply{
			Answerable: true,
			Sources:    []string{"catalogo.md", "QA.md"},
			Reply:      "O Plano Enterprise sai por R$ 500.000 para 10 usuários, com até 5% de desconto.",
		}, kb, nil, newMsg, facts)
		if !res.Grounded {
			t.Fatalf("expected grounded=true, got %+v", res)
		}
	})

	t.Run("hallucinated price fails grounding", func(t *testing.T) {
		res := brain.CheckGrounding(brain.StructuredReply{
			Answerable: true,
			Sources:    []string{"catalogo.md"},
			Reply:      "Posso fazer por R$ 420.000 hoje.",
		}, kb, nil, newMsg, facts)
		if res.Grounded || len(res.UngroundedNumbers) == 0 {
			t.Fatalf("expected ungrounded number failure, got %+v", res)
		}
	})

	t.Run("nonexistent cited source fails grounding", func(t *testing.T) {
		res := brain.CheckGrounding(brain.StructuredReply{
			Answerable: true,
			Sources:    []string{"tabela_secreta.md"},
			Reply:      "Temos disponibilidade imediata.",
		}, kb, nil, newMsg, facts)
		if res.Grounded || len(res.MissingSources) == 0 {
			t.Fatalf("expected missing source failure, got %+v", res)
		}
	})

	t.Run("answerable false fails grounding", func(t *testing.T) {
		res := brain.CheckGrounding(brain.StructuredReply{
			Answerable: false,
			Reply:      "Não encontrei essa informação.",
		}, kb, nil, newMsg, facts)
		if res.Grounded {
			t.Fatalf("expected grounded=false when answerable=false")
		}
	})
}

func TestPipeline_UngroundedReplyEscalatesWithNeutralMessage(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 11, 14, 0, 0, 0, time.UTC)
	clock := fakes.NewFakeClock(now)

	llm := fakes.NewScriptedLLM(nil)
	llm.EnqueueStage("analysis", core.LLMResponse{
		Content:          `{"summary":"Cliente perguntou preço especial.","last_sender":"customer","facts":{},"signals":{"has_new_question":true},"context":{}}`,
		PromptTokens:     50,
		CompletionTokens: 20,
		DurationMs:       10,
	})
	llm.EnqueueStage("response", core.LLMResponse{
		Content:          `{"answerable":false,"sources":[],"reply":"Posso oferecer por 999999 reais!"}`,
		PromptTokens:     80,
		CompletionTokens: 25,
		DurationMs:       15,
	})

	p, st, ch := setupGatesPipeline(t, core.TenantPolicy{Mode: core.ModeAuto, BotEnabled: true}.WithDefaults(), clock, llm)
	defer p.Stop()

	msg := core.NormalizedMessage{
		TenantID: "t1", Channel: "simulator", SourceKey: "u-esc",
		MessageID: "m1", Timestamp: now, Direction: core.DirectionInbound, Type: core.MessageTypeText, Text: "Faz desconto especial?",
	}
	if err := p.ProcessBatch(ctx, []core.NormalizedMessage{msg}); err != nil {
		t.Fatal(err)
	}

	sent := ch.SentMessages()
	if len(sent) != 1 || sent[0].Text != brain.NeutralFallbackReply {
		t.Fatalf("expected NeutralFallbackReply sent, got %+v", sent)
	}
	tasks, err := st.ListSourceTasks(ctx, "t1", "simulator", "u-esc")
	if err != nil || len(tasks) != 1 || tasks[0].Kind != "escalation" {
		t.Fatalf("expected escalation task created, got %+v err=%v", tasks, err)
	}
}
