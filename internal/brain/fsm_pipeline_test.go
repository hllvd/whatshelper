package brain_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"whatshelper/internal/brain"
	"whatshelper/internal/core"
	"whatshelper/internal/core/fakes"
	"whatshelper/internal/pack"
	"whatshelper/internal/store/sessioncache"
)

func TestFSMPipeline_OrchestrationAndSessionCache(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 12, 14, 0, 0, 0, time.UTC)
	clock := fakes.NewFakeClock(now)
	memStore := fakes.NewMemoryStoreWithClock(clock)
	blobs := fakes.NewMemoryBlobStore()
	llm := fakes.NewScriptedLLM(nil)
	ch := fakes.NewMemoryChannel("simulator", 16)
	_ = ch.Start(ctx)

	repoRoot := filepath.Join("..", "..")
	tenantsDir := filepath.Join(repoRoot, "tenants")
	packsDir := filepath.Join(repoRoot, "packs")

	if _, err := brain.SeedLocalTenants(ctx, tenantsDir, memStore); err != nil {
		t.Fatal(err)
	}
	if _, err := brain.SeedLocalKnowledge(ctx, tenantsDir, blobs); err != nil {
		t.Fatal(err)
	}

	sc, err := sessioncache.Open(":memory:")
	if err != nil {
		t.Fatalf("falha ao inicializar sessioncache em memória: %v", err)
	}
	defer sc.Close()

	p := brain.NewPipeline(ctx, brain.PipelineConfig{
		Store:         memStore,
		Blobs:         blobs,
		LLM:           llm,
		Bus:           core.NewInMemoryEventBus(64),
		Clock:         clock,
		Tenants:       brain.NewStoreTenantPolicyLoader(memStore, nil),
		Knowledge:     brain.NewBlobKnowledgeSource(blobs, clock, 5*time.Second),
		PackLoader:    func(name string) (*pack.Pack, error) { return pack.LoadFromDir(filepath.Join(packsDir, name)) },
		AnalysisModel: "test-sensor",
		ResponseModel: "test-reply",
		SessionCache:  sc,
	})
	p.RegisterChannel(ch)
	defer p.Stop()

	tenantID := "demo-support"
	channelID := "simulator"
	sourceKey := "test-fsm-user-support"

	// Turno 1: Saudação inicial -> FSM dispara template greet_support e avança para troubleshooting
	llm.EnqueueStage("analysis", core.LLMResponse{
		Content: `{
			"success": true,
			"data": {
				"message_analysis": {
					"primary_intent": "greeting",
					"urgency_level": "low",
					"sentiment": "positive"
				},
				"summary": "Usuário saudou o suporte.",
				"last_sender": "user",
				"facts": {},
				"signals": {
					"has_new_question": false,
					"customer_engaged": true
				},
				"context": {},
				"conversation_state": "troubleshooting"
			},
			"error": null
		}`,
		PromptTokens: 80, CompletionTokens: 40, DurationMs: 15,
	})

	msg1 := core.NormalizedMessage{
		TenantID:  tenantID,
		Channel:   channelID,
		SourceKey: sourceKey,
		MessageID: "msg-fsm-1",
		Timestamp: clock.Now(),
		Direction: core.DirectionInbound,
		Type:      core.MessageTypeText,
		Text:      "Olá, preciso de ajuda com o sistema",
	}

	if err := p.ProcessBatch(ctx, []core.NormalizedMessage{msg1}); err != nil {
		t.Fatalf("Turno 1 falhou: %v", err)
	}

	// Verifica template disparado sem chamar Stage 2 de LLM
	sent := ch.SentMessages()
	if len(sent) != 1 {
		t.Fatalf("esperava 1 mensagem enviada no Turno 1, obteve %d", len(sent))
	}
	if !strings.Contains(sent[0].Text, "suporte técnico") {
		t.Errorf("resposta inesperada do template no Turno 1: %s", sent[0].Text)
	}

	// Verifica se estado na sessão ativa (SQLite) e no store virou troubleshooting
	sess, err := sc.GetSession(ctx, tenantID, channelID, sourceKey)
	if err != nil {
		t.Fatalf("falha ao carregar sessão ativa do SQLite: %v", err)
	}
	if sess.CurrentState != "troubleshooting" {
		t.Errorf("esperava sessão ativa no estado troubleshooting, obteve %s", sess.CurrentState)
	}

	st, err := memStore.GetSourceState(ctx, tenantID, channelID, sourceKey)
	if err != nil {
		t.Fatalf("falha ao carregar estado do store: %v", err)
	}
	if st.Meta.JourneyStage != "troubleshooting" {
		t.Errorf("esperava META.journey_stage troubleshooting, obteve %s", st.Meta.JourneyStage)
	}

	// Turno 2: Relato de bug SEM ErrorCode
	// Guarda da FSM exige ErrorCode. Como falta, dispara ação ask_error_code e mantém em troubleshooting/triage.
	clock.Advance(5 * time.Second)
	llm.EnqueueStage("analysis", core.LLMResponse{
		Content: `{
			"success": true,
			"data": {
				"message_analysis": {
					"primary_intent": "report_bug",
					"urgency_level": "high",
					"sentiment": "negative"
				},
				"summary": "Usuário relatou falha sem código.",
				"last_sender": "user",
				"facts": {},
				"signals": {
					"has_new_question": false,
					"has_pending_bug": true,
					"customer_engaged": true
				},
				"context": {},
				"conversation_state": "troubleshooting"
			},
			"error": null
		}`,
		PromptTokens: 90, CompletionTokens: 40, DurationMs: 15,
	})

	msg2 := core.NormalizedMessage{
		TenantID:  tenantID,
		Channel:   channelID,
		SourceKey: sourceKey,
		MessageID: "msg-fsm-2",
		Timestamp: clock.Now(),
		Direction: core.DirectionInbound,
		Type:      core.MessageTypeText,
		Text:      "Deu erro na tela ao salvar!",
	}

	if err := p.ProcessBatch(ctx, []core.NormalizedMessage{msg2}); err != nil {
		t.Fatalf("Turno 2 falhou: %v", err)
	}

	sent = ch.SentMessages()
	if len(sent) != 2 {
		t.Fatalf("esperava 2 mensagens enviadas no total, obteve %d", len(sent))
	}
	// Deve disparar a ação corretiva ask_error_code do template
	if !strings.Contains(sent[1].Text, "código de erro") {
		t.Errorf("esperava pergunta corretiva de código no Turno 2, obteve: %s", sent[1].Text)
	}

	// Turno 3: Usuário fornece ErrorCode
	// Guarda satisfeita! FSM dispara acknowledge_bug
	clock.Advance(5 * time.Second)
	llm.EnqueueStage("analysis", core.LLMResponse{
		Content: `{
			"success": true,
			"data": {
				"message_analysis": {
					"primary_intent": "report_bug",
					"urgency_level": "high",
					"sentiment": "neutral"
				},
				"extracted_entities": {
					"ErrorCode": "ERR-502"
				},
				"summary": "Usuário informou código de erro ERR-502.",
				"last_sender": "user",
				"facts": {
					"ErrorCode": "ERR-502"
				},
				"signals": {
					"has_new_question": false,
					"has_pending_bug": true,
					"customer_engaged": true
				},
				"context": {},
				"conversation_state": "troubleshooting"
			},
			"error": null
		}`,
		PromptTokens: 100, CompletionTokens: 50, DurationMs: 15,
	})

	msg3 := core.NormalizedMessage{
		TenantID:  tenantID,
		Channel:   channelID,
		SourceKey: sourceKey,
		MessageID: "msg-fsm-3",
		Timestamp: clock.Now(),
		Direction: core.DirectionInbound,
		Type:      core.MessageTypeText,
		Text:      "O código que aparece é ERR-502",
	}

	if err := p.ProcessBatch(ctx, []core.NormalizedMessage{msg3}); err != nil {
		t.Fatalf("Turno 3 falhou: %v", err)
	}

	sent = ch.SentMessages()
	if len(sent) != 3 {
		t.Fatalf("esperava 3 mensagens enviadas no total, obteve %d", len(sent))
	}
	if !strings.Contains(sent[2].Text, "equipe técnica está analisando") {
		t.Errorf("resposta inesperada no Turno 3: %s", sent[2].Text)
	}

	sess, err = sc.GetSession(ctx, tenantID, channelID, sourceKey)
	if err != nil || sess.CurrentState != "troubleshooting" {
		t.Errorf("esperava sessão em troubleshooting no Turno 3, obteve %+v (err=%v)", sess, err)
	}
	st, err = memStore.GetSourceState(ctx, tenantID, channelID, sourceKey)
	if err != nil || st.Meta.JourneyStage != "troubleshooting" {
		t.Errorf("esperava META.journey_stage troubleshooting no Turno 3, obteve %+v (err=%v)", st, err)
	}
}

func TestFSMPipeline_Supporter_GratitudeClosesFlow(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	clock := fakes.NewFakeClock(now)
	memStore := fakes.NewMemoryStoreWithClock(clock)
	blobs := fakes.NewMemoryBlobStore()
	llm := fakes.NewScriptedLLM(nil)
	ch := fakes.NewMemoryChannel("simulator", 16)
	_ = ch.Start(ctx)

	repoRoot := filepath.Join("..", "..")
	tenantsDir := filepath.Join(repoRoot, "tenants")
	packsDir := filepath.Join(repoRoot, "packs")

	if _, err := brain.SeedLocalTenants(ctx, tenantsDir, memStore); err != nil {
		t.Fatal(err)
	}
	if _, err := brain.SeedLocalKnowledge(ctx, tenantsDir, blobs); err != nil {
		t.Fatal(err)
	}

	sc, err := sessioncache.Open(":memory:")
	if err != nil {
		t.Fatalf("falha ao inicializar sessioncache em memória: %v", err)
	}
	defer sc.Close()

	p := brain.NewPipeline(ctx, brain.PipelineConfig{
		Store:         memStore,
		Blobs:         blobs,
		LLM:           llm,
		Bus:           core.NewInMemoryEventBus(64),
		Clock:         clock,
		Tenants:       brain.NewStoreTenantPolicyLoader(memStore, nil),
		Knowledge:     brain.NewBlobKnowledgeSource(blobs, clock, 5*time.Second),
		PackLoader:    func(name string) (*pack.Pack, error) { return pack.LoadFromDir(filepath.Join(packsDir, name)) },
		AnalysisModel: "test-sensor",
		ResponseModel: "test-reply",
		SessionCache:  sc,
	})
	p.RegisterChannel(ch)
	defer p.Stop()

	tenantID := "supporter"
	channelID := "simulator"
	sourceKey := "test-supporter-closing-user"

	// Turno 1: Usuário pergunta sobre contrato (ask_howto) -> entra em assisting e responde com conhecimento
	llm.EnqueueStage("analysis", core.LLMResponse{
		Content: `{
			"success": true,
			"data": {
				"message_analysis": {
					"primary_intent": "ask_howto",
					"urgency_level": "medium",
					"sentiment": "neutral"
				},
				"summary": "Usuário tem dúvida sobre contrato.",
				"last_sender": "user",
				"facts": {},
				"signals": {
					"has_new_question": true,
					"user_engaged": true
				},
				"context": {},
				"conversation_state": "assisting"
			},
			"error": null
		}`,
		PromptTokens: 80, CompletionTokens: 40, DurationMs: 15,
	})
	llm.EnqueueStage("response", core.LLMResponse{
		Content: `{
			"success": true,
			"data": {
				"reply": "O contrato foi sincronizado com sucesso.",
				"sources": ["sistema"],
				"grounding_score": 0.95
			},
			"error": null
		}`,
		PromptTokens: 100, CompletionTokens: 30, DurationMs: 20,
	})

	msg1 := core.NormalizedMessage{
		TenantID:  tenantID,
		Channel:   channelID,
		SourceKey: sourceKey,
		MessageID: "msg-close-1",
		Timestamp: clock.Now(),
		Direction: core.DirectionInbound,
		Type:      core.MessageTypeText,
		Text:      "Como vejo meu contrato?",
	}

	if err := p.ProcessBatch(ctx, []core.NormalizedMessage{msg1}); err != nil {
		t.Fatalf("Turno 1 falhou: %v", err)
	}

	sess, err := sc.GetSession(ctx, tenantID, channelID, sourceKey)
	if err != nil || sess.CurrentState != "assisting" {
		t.Fatalf("esperava estado assisting após Turno 1, obteve %+v (err=%v)", sess, err)
	}

	// Turno 2 (3 horas depois): Cliente agradece com "Obrigado" -> NÃO pode voltar saudação de início
	// FSM deve transicionar para "resolved" com action close_support (mensagem de encerramento cordial)
	clock.Advance(3 * time.Hour)

	llm.EnqueueStage("analysis", core.LLMResponse{
		Content: `{
			"success": true,
			"data": {
				"message_analysis": {
					"primary_intent": "closing_or_gratitude",
					"urgency_level": "low",
					"sentiment": "positive"
				},
				"summary": "Usuário agradeceu pelo suporte.",
				"last_sender": "user",
				"facts": {},
				"signals": {
					"has_new_question": false,
					"user_engaged": true,
					"issue_resolved": true
				},
				"context": {},
				"conversation_state": "resolved"
			},
			"error": null
		}`,
		PromptTokens: 60, CompletionTokens: 30, DurationMs: 10,
	})

	msg2 := core.NormalizedMessage{
		TenantID:  tenantID,
		Channel:   channelID,
		SourceKey: sourceKey,
		MessageID: "msg-close-2",
		Timestamp: clock.Now(),
		Direction: core.DirectionInbound,
		Type:      core.MessageTypeText,
		Text:      "Obrigado",
	}

	if err := p.ProcessBatch(ctx, []core.NormalizedMessage{msg2}); err != nil {
		t.Fatalf("Turno 2 falhou: %v", err)
	}

	sent := ch.SentMessages()
	if len(sent) != 2 {
		t.Fatalf("esperava 2 mensagens enviadas no canal, obteve %d", len(sent))
	}
	latestSent := sent[len(sent)-1].Text
	if strings.Contains(latestSent, "Como posso te ajudar hoje?") {
		t.Fatalf("erro: bot reiniciou o fluxo de saudação indevidamente após agradecimento: %s", latestSent)
	}
	if !strings.Contains(latestSent, "Por nada!") {
		t.Errorf("esperava encerramento cordial com 'Por nada!', obteve: %s", latestSent)
	}

	sess, err = sc.GetSession(ctx, tenantID, channelID, sourceKey)
	if err != nil || sess.CurrentState != "resolved" {
		t.Fatalf("esperava estado 'resolved' após agradecimento, obteve %+v (err=%v)", sess, err)
	}
	st, err := memStore.GetSourceState(ctx, tenantID, channelID, sourceKey)
	if err != nil || st.Meta.JourneyStage != "resolved" {
		t.Fatalf("esperava journey_stage 'resolved' no SourceState, obteve %+v (err=%v)", st, err)
	}

	// Turno 3 (no dia seguinte): Usuário inicia novo contato com "Olá" -> sai de resolved para assisting
	clock.Advance(24 * time.Hour)

	llm.EnqueueStage("analysis", core.LLMResponse{
		Content: `{
			"success": true,
			"data": {
				"message_analysis": {
					"primary_intent": "greeting",
					"urgency_level": "low",
					"sentiment": "neutral"
				},
				"summary": "Usuário iniciou nova conversa.",
				"last_sender": "user",
				"facts": {},
				"signals": {
					"has_new_question": false,
					"user_engaged": true
				},
				"context": {},
				"conversation_state": "assisting"
			},
			"error": null
		}`,
		PromptTokens: 50, CompletionTokens: 25, DurationMs: 10,
	})

	msg3 := core.NormalizedMessage{
		TenantID:  tenantID,
		Channel:   channelID,
		SourceKey: sourceKey,
		MessageID: "msg-close-3",
		Timestamp: clock.Now(),
		Direction: core.DirectionInbound,
		Type:      core.MessageTypeText,
		Text:      "Olá",
	}

	if err := p.ProcessBatch(ctx, []core.NormalizedMessage{msg3}); err != nil {
		t.Fatalf("Turno 3 falhou: %v", err)
	}

	sent = ch.SentMessages()
	if len(sent) != 3 {
		t.Fatalf("esperava 3 mensagens enviadas no total, obteve %d", len(sent))
	}
	if !strings.Contains(sent[2].Text, "Como posso te ajudar hoje?") {
		t.Errorf("esperava saudação no novo atendimento, obteve: %s", sent[2].Text)
	}
}

func TestFSMPipeline_Supporter_TeamFilterMigratedConsultant(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 18, 9, 20, 0, 0, time.UTC)
	clock := fakes.NewFakeClock(now)
	memStore := fakes.NewMemoryStoreWithClock(clock)
	blobs := fakes.NewMemoryBlobStore()
	llm := fakes.NewScriptedLLM(nil)
	ch := fakes.NewMemoryChannel("simulator", 16)
	_ = ch.Start(ctx)

	repoRoot := filepath.Join("..", "..")
	tenantsDir := filepath.Join(repoRoot, "tenants")
	packsDir := filepath.Join(repoRoot, "packs")

	if _, err := brain.SeedLocalTenants(ctx, tenantsDir, memStore); err != nil {
		t.Fatal(err)
	}
	if _, err := brain.SeedLocalKnowledge(ctx, tenantsDir, blobs); err != nil {
		t.Fatal(err)
	}

	p := brain.NewPipeline(ctx, brain.PipelineConfig{
		Store:         memStore,
		Blobs:         blobs,
		LLM:           llm,
		Bus:           core.NewInMemoryEventBus(64),
		Clock:         clock,
		Tenants:       brain.NewStoreTenantPolicyLoader(memStore, nil),
		Knowledge:     brain.NewBlobKnowledgeSource(blobs, clock, 5*time.Second),
		PackLoader:    func(name string) (*pack.Pack, error) { return pack.LoadFromDir(filepath.Join(packsDir, name)) },
		AnalysisModel: "test-sensor",
		ResponseModel: "test-reply",
	})
	p.RegisterChannel(ch)
	defer p.Stop()

	tenantID := "supporter"
	channelID := "simulator"
	sourceKey := "test-team-filter-user"

	// Turno 1: Usuário já identificou que está em "contratos" e relata que o filtro de equipe puxa vendas de quem mudou de time
	llm.EnqueueStage("analysis", core.LLMResponse{
		Content: `{
			"summary": "Usuário está na tela Contratos e relata que o filtro de equipe puxa vendas de gestora que já foi da equipe.",
			"last_sender": "user",
			"intent": "ask_howto",
			"facts": {
				"ChannelOrScreen": "Contratos",
				"Topic": "contratos"
			},
			"signals": {
				"has_new_question": true,
				"user_engaged": true
			},
			"context": {}
		}`,
		PromptTokens: 80, CompletionTokens: 40, DurationMs: 15,
	})
	llm.EnqueueStage("response", core.LLMResponse{
		Content: `{
			"answerable": true,
			"sources": ["como-ajustar-membros-equipe-calendario.md", "QA.md"],
			"reply": "Isso acontece porque as datas de vigência no Calendário de Equipes (#/teams/calendar) estão sobrepostas. Acesse Equipes → Calendário (#/teams/calendar), localize a gestora/consultora e preencha a Data Final na equipe antiga e a Data de Início na nova equipe."
		}`,
		PromptTokens: 100, CompletionTokens: 40, DurationMs: 20,
	})

	msg := core.NormalizedMessage{
		TenantID:  tenantID,
		Channel:   channelID,
		SourceKey: sourceKey,
		MessageID: "msg-team-filter-1",
		Timestamp: clock.Now(),
		Direction: core.DirectionInbound,
		Type:      core.MessageTypeText,
		Text:      "Eu estou filtrando por equipe em contratos e mesmo assim aparece venda de uma gestora que já foi da minha equipe mas está em outra equipe",
	}

	if err := p.ProcessBatch(ctx, []core.NormalizedMessage{msg}); err != nil {
		t.Fatalf("ProcessBatch falhou: %v", err)
	}

	sent := ch.SentMessages()
	if len(sent) != 1 {
		t.Fatalf("esperava 1 mensagem enviada, obteve %d", len(sent))
	}
	replyText := sent[0].Text
	if strings.Contains(replyText, "qual tela ou página") || strings.Contains(replyText, "qual página exata") {
		t.Errorf("erro: bot repetiu pergunta sobre tela/página quando ChannelOrScreen já era conhecido: %s", replyText)
	}
	if !strings.Contains(replyText, "Calendário") || !strings.Contains(replyText, "Data Final") {
		t.Errorf("esperava orientação sobre Calendário de Equipes e data de corte, obteve: %s", replyText)
	}
}
