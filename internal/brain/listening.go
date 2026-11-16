package brain

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"whatshelper/internal/core"
	"whatshelper/internal/store"
)

// ListeningConfig guarda a configuração do modo de escuta para um tenant.
type ListeningConfig struct {
	Global    bool      `json:"global"`
	Whitelist []string  `json:"whitelist,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// IsListeningMode avalia se o modo de escuta está ativo para uma mensagem/conversa.
func (p *Pipeline) IsListeningMode(ctx context.Context, tenantID, channel, sourceKey, contactID string) bool {
	if p.cfg.Store == nil {
		return false
	}
	if tenantID == "" {
		tenantID = "demo"
	}

	// 1. Checa configuração global do tenant
	if data, err := p.cfg.Store.GetTenantConfig(ctx, tenantID, "listening"); err == nil && len(data) > 0 {
		var cfg ListeningConfig
		if json.Unmarshal(data, &cfg) == nil {
			if cfg.Global {
				return true
			}
			for _, item := range cfg.Whitelist {
				trimmed := strings.TrimSpace(item)
				if trimmed == "" {
					continue
				}
				normItem := core.NormalizeEmail(trimmed)
				normPhone := core.NormalizePhone(trimmed)
				if strings.EqualFold(sourceKey, trimmed) || strings.EqualFold(contactID, trimmed) {
					return true
				}
				if normItem != "" && (strings.EqualFold(normItem, sourceKey) || strings.EqualFold(normItem, contactID)) {
					return true
				}
				if normPhone != "" && (strings.Contains(sourceKey, normPhone) || strings.Contains(contactID, normPhone)) {
					return true
				}
			}
		}
	}

	// 2. Checa contato específico
	if contactID != "" {
		if c, err := p.cfg.Store.GetContact(ctx, tenantID, contactID); err == nil && c != nil {
			if c.Listening {
				return true
			}
		}
	}

	// 3. Checa estado da fonte
	if state, err := p.cfg.Store.GetSourceState(ctx, tenantID, channel, sourceKey); err == nil && state != nil {
		if state.Meta.Status == "listening" {
			return true
		}
	}

	return false
}

// ObserveAndLearnFromAttendant escuta a resposta de um atendente humano e gera propostas de aprendizado
// para QA-listed.md (dúvidas genéricas) e character.md (tom de voz/personalidade).
func (p *Pipeline) ObserveAndLearnFromAttendant(ctx context.Context, attendantMsg core.NormalizedMessage) {
	if p.cfg.Store == nil || p.cfg.Blobs == nil || p.cfg.LLM == nil {
		return
	}

	tenantID := attendantMsg.TenantID
	if tenantID == "" {
		tenantID = "demo"
	}

	// Carrega histórico recente da conversa para contextualizar
	recent, err := p.cfg.Store.ListRecentMessages(ctx, tenantID, attendantMsg.Channel, attendantMsg.SourceKey, 6)
	if err != nil || len(recent) == 0 {
		return
	}

	attendantText := strings.TrimSpace(attendantMsg.Text)
	if attendantText == "" {
		return
	}

	// Encontra a última pergunta do cliente
	customerQuestion := ""
	for i := len(recent) - 1; i >= 0; i-- {
		if recent[i].Direction == core.DirectionInbound {
			if raw, bErr := p.cfg.Blobs.Get(ctx, recent[i].RawRef); bErr == nil {
				var nMsg core.NormalizedMessage
				if json.Unmarshal(raw, &nMsg) == nil {
					customerQuestion = strings.TrimSpace(nMsg.Text)
					if customerQuestion != "" {
						break
					}
				}
			}
		}
	}

	// Se não achou pergunta explícita, usa o resumo da conversa
	if customerQuestion == "" {
		if st, _ := p.cfg.Store.GetSourceState(ctx, tenantID, attendantMsg.Channel, attendantMsg.SourceKey); st != nil {
			customerQuestion = st.Summary.Text
		}
	}
	if customerQuestion == "" {
		return
	}

	// 1. Gera proposta de QA genérico (sem PII ou dados exclusivos)
	p.generateGenericQAProposal(ctx, tenantID, attendantMsg, customerQuestion, attendantText)

	// 2. Gera proposta de personalidade/tom de voz do atendente
	p.generateCharacterProposal(ctx, tenantID, attendantMsg, customerQuestion, attendantText)
}

func (p *Pipeline) generateGenericQAProposal(ctx context.Context, tenantID string, msg core.NormalizedMessage, customerQ, attendantReply string) {
	system := `Você extrai perguntas e respostas gerais a partir de atendimentos para compor uma base de conhecimento.
REGRAS OBRIGATÓRIAS:
- A pergunta e a resposta devem ser GENÉRICAS e aplicáveis a qualquer cliente.
- NUNCA inclua dados pessoais, nomes de clientes, contratos específicos, CPFs, e-mails pessoais ou valores individuais.
- Se a conversa tratou de algo estritamente privado de um cliente único que não pode ser generalizado, retorne {"skip": true}.
- Retorne APENAS um JSON: {"question": "pergunta genérica", "answer": "resposta genérica clara e prestativa", "skip": false}`

	user := fmt.Sprintf("DÚVIDA DO CLIENTE: %s\nRESPOSTA DO ATENDENTE: %s", customerQ, attendantReply)

	resp, err := p.cfg.LLM.Generate(ctx, core.LLMRequest{
		Stage:       "learning",
		Model:       p.learningModel(),
		System:      system,
		Prompt:      user,
		Temperature: 0.1,
		MaxTokens:   512,
	})
	if err != nil {
		return
	}

	jsonStr, err := ExtractJSON(resp.Content)
	if err != nil {
		return
	}

	var out struct {
		Question string `json:"question"`
		Answer   string `json:"answer"`
		Skip     bool   `json:"skip"`
	}
	if json.Unmarshal([]byte(jsonStr), &out) != nil || out.Skip || strings.TrimSpace(out.Question) == "" || strings.TrimSpace(out.Answer) == "" {
		return
	}

	now := p.now()
	propID := newID("lrn")
	prop := LearningProposal{
		ID:               propID,
		TenantID:         tenantID,
		OriginChannel:    msg.Channel,
		OriginSourceKey:  msg.SourceKey,
		CustomerQuestion: customerQ,
		OwnerCorrection:  attendantReply,
		Question:         out.Question,
		Answer:           out.Answer,
		Action:           "add",
		TargetFile:       "learning/QA-listed.md",
		Kind:             "qa",
		Status:           "pending",
		CreatedAt:        now,
	}

	blobRef := store.S3LearningKey(tenantID, propID)
	propBytes, _ := json.Marshal(prop)
	_ = p.cfg.Blobs.Put(ctx, blobRef, propBytes, "application/json")

	task := core.Task{
		ID:           propID,
		TenantID:     tenantID,
		Channel:      msg.Channel,
		SourceKey:    msg.SourceKey,
		Kind:         "learning",
		Title:        "Aprovar QA genérico: " + truncateText(prop.Question, 70),
		Type:         "qa_learning",
		Status:       core.TaskStatusPending,
		DueAt:        &now,
		DraftBlobRef: blobRef,
		Metadata: map[string]string{
			"proposal_id": propID,
			"kind":        "qa",
			"target_file": "learning/QA-listed.md",
		},
		CreatedAt: now,
	}
	_ = p.saveAndPublishTask(ctx, task)
}

func (p *Pipeline) generateCharacterProposal(ctx context.Context, tenantID string, msg core.NormalizedMessage, customerQ, attendantReply string) {
	system := `Você analisa o modo de falar, estilo, empatia e vocabulário do atendente humano para compor um arquivo de personalidade (character/persona).
Esse arquivo servirá no futuro para clonar o modo de fala do atendente.
REGRAS:
- Identifique o tom (formal, empático, direto, descontraído), saudações/fechamentos e frases características.
- Retorne APENAS um JSON: {"trait": "descrição do traço de estilo/tom", "sample": "exemplo extraído da fala do atendente"}`

	user := fmt.Sprintf("MENSAGEM DO CLIENTE: %s\nRESPOSTA DO ATENDENTE: %s", customerQ, attendantReply)

	resp, err := p.cfg.LLM.Generate(ctx, core.LLMRequest{
		Stage:       "learning",
		Model:       p.learningModel(),
		System:      system,
		Prompt:      user,
		Temperature: 0.1,
		MaxTokens:   512,
	})
	if err != nil {
		return
	}

	jsonStr, err := ExtractJSON(resp.Content)
	if err != nil {
		return
	}

	var out struct {
		Trait  string `json:"trait"`
		Sample string `json:"sample"`
	}
	if json.Unmarshal([]byte(jsonStr), &out) != nil || strings.TrimSpace(out.Trait) == "" {
		return
	}

	now := p.now()
	propID := newID("lrn")
	prop := LearningProposal{
		ID:               propID,
		TenantID:         tenantID,
		OriginChannel:    msg.Channel,
		OriginSourceKey:  msg.SourceKey,
		CustomerQuestion: customerQ,
		OwnerCorrection:  attendantReply,
		Question:         out.Trait,
		Answer:           out.Sample,
		Action:           "add",
		TargetFile:       "personas/character.md",
		Kind:             "character",
		Status:           "pending",
		CreatedAt:        now,
	}

	blobRef := store.S3LearningKey(tenantID, propID)
	propBytes, _ := json.Marshal(prop)
	_ = p.cfg.Blobs.Put(ctx, blobRef, propBytes, "application/json")

	task := core.Task{
		ID:           propID,
		TenantID:     tenantID,
		Channel:      msg.Channel,
		SourceKey:    msg.SourceKey,
		Kind:         "learning",
		Title:        "Aprovar traço de personalidade: " + truncateText(prop.Question, 70),
		Type:         "character_learning",
		Status:       core.TaskStatusPending,
		DueAt:        &now,
		DraftBlobRef: blobRef,
		Metadata: map[string]string{
			"proposal_id": propID,
			"kind":        "character",
			"target_file": "personas/character.md",
		},
		CreatedAt: now,
	}
	_ = p.saveAndPublishTask(ctx, task)
}
