package brain

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"unicode"

	"whatshelper/internal/core"
	"whatshelper/internal/pack"
)

const (
	// NeutralFallbackReply é enviada quando a resposta não tem base no conhecimento.
	NeutralFallbackReply = "Obrigado pela sua mensagem! Vou verificar essa informação com nossa equipe e retornamos em seguida."
	// NeutralEscalationReply é enviada quando a intenção requer análise humana direta.
	NeutralEscalationReply = "Recebi sua solicitação e já encaminhei para nossa equipe analisar e retornar em breve."
)

// StructuredReply representa o envelope estruturado esperado do estágio de resposta.
type StructuredReply struct {
	Answerable bool     `json:"answerable"`
	Sources    []string `json:"sources"`
	Reply      string   `json:"reply"`
}

// GroundingResult registra o resultado da validação determinística de aderência ao conhecimento.
type GroundingResult struct {
	Grounded          bool     `json:"grounded"`
	Reason            string   `json:"reason,omitempty"`
	MissingSources    []string `json:"missing_sources,omitempty"`
	UngroundedNumbers []string `json:"ungrounded_numbers,omitempty"`
}

var replyNumberPattern = regexp.MustCompile(`\d+(?:[.,]\d+)*`)

// ParseReplyOutput tenta extrair o JSON estruturado {answerable, sources, reply}.
// Quando o prompt do pack não usa envelope JSON, retorna fallback com isStructured=false.
func ParseReplyOutput(raw string) (StructuredReply, bool) {
	trimmed := strings.TrimSpace(raw)
	jsonStr, err := ExtractJSON(trimmed)
	if err == nil {
		var envelope struct {
			Answerable *bool    `json:"answerable"`
			Sources    []string `json:"sources"`
			Reply      *string  `json:"reply"`
		}
		if json.Unmarshal([]byte(jsonStr), &envelope) == nil && (envelope.Answerable != nil || envelope.Reply != nil) {
			answerable := true
			if envelope.Answerable != nil {
				answerable = *envelope.Answerable
			}
			replyText := ""
			if envelope.Reply != nil {
				replyText = strings.TrimSpace(*envelope.Reply)
			}
			return StructuredReply{
				Answerable: answerable,
				Sources:    envelope.Sources,
				Reply:      replyText,
			}, true
		}
	}
	return StructuredReply{
		Answerable: trimmed != "",
		Reply:      trimmed,
	}, false
}

// CheckGrounding verifica deterministicamente se fontes citadas e números na resposta existem no conhecimento ou na conversa.
func CheckGrounding(
	reply StructuredReply,
	knowledge KnowledgeBundle,
	recentMessages []pack.PromptMessage,
	newMsg pack.PromptMessage,
	facts map[string]string,
) GroundingResult {
	if !reply.Answerable || strings.TrimSpace(reply.Reply) == "" {
		return GroundingResult{
			Grounded: false,
			Reason:   "pergunta sem cobertura no conhecimento configurado",
		}
	}

	allowedSources := make(map[string]bool, len(knowledge.Docs)+1)
	for _, doc := range knowledge.Docs {
		allowedSources[strings.ToLower(strings.TrimSpace(doc.Name))] = true
	}
	if strings.TrimSpace(knowledge.QAContent) != "" {
		allowedSources["qa.md"] = true
	}

	var missingSources []string
	for _, src := range reply.Sources {
		normSrc := strings.ToLower(strings.TrimSpace(src))
		if normSrc == "" {
			continue
		}
		if !allowedSources[normSrc] {
			missingSources = append(missingSources, src)
		}
	}
	if len(missingSources) > 0 {
		return GroundingResult{
			Grounded:       false,
			Reason:         fmt.Sprintf("fonte não encontrada no conhecimento: %s", strings.Join(missingSources, ", ")),
			MissingSources: missingSources,
		}
	}

	// Coleta todos os tokens numéricos autorizados (conhecimento, QA.md, mensagens e fatos).
	var allowedCorpus strings.Builder
	for _, doc := range knowledge.Docs {
		allowedCorpus.WriteString(doc.Content)
		allowedCorpus.WriteByte('\n')
	}
	allowedCorpus.WriteString(knowledge.QAContent)
	allowedCorpus.WriteByte('\n')
	for _, m := range recentMessages {
		allowedCorpus.WriteString(m.Text)
		allowedCorpus.WriteByte('\n')
	}
	allowedCorpus.WriteString(newMsg.Text)
	allowedCorpus.WriteByte('\n')
	for _, v := range facts {
		allowedCorpus.WriteString(v)
		allowedCorpus.WriteByte('\n')
	}

	allowedNums := extractNormalizedNumberSet(allowedCorpus.String())
	replyNums := replyNumberPattern.FindAllString(reply.Reply, -1)

	var ungrounded []string
	seenBad := make(map[string]bool)
	for _, rawNum := range replyNums {
		if isAnyVariantAllowed(rawNum, allowedNums) {
			continue
		}
		if !seenBad[rawNum] {
			seenBad[rawNum] = true
			ungrounded = append(ungrounded, rawNum)
		}
	}

	if len(ungrounded) > 0 {
		return GroundingResult{
			Grounded:          false,
			Reason:            fmt.Sprintf("números não respaldados pelo conhecimento/conversa: %s", strings.Join(ungrounded, ", ")),
			UngroundedNumbers: ungrounded,
		}
	}

	return GroundingResult{Grounded: true}
}

func extractNormalizedNumberSet(text string) map[string]bool {
	set := make(map[string]bool)
	for _, tok := range replyNumberPattern.FindAllString(text, -1) {
		for _, v := range numberVariants(tok) {
			set[v] = true
		}
	}
	return set
}

func isAnyVariantAllowed(tok string, allowed map[string]bool) bool {
	for _, v := range numberVariants(tok) {
		if allowed[v] {
			return true
		}
	}
	return false
}

func numberVariants(tok string) []string {
	clean := strings.TrimSpace(tok)
	if clean == "" {
		return nil
	}
	variants := []string{clean}

	// Remove separadores de milhar (ex: 500.000 -> 500000) e também captura apenas dígitos
	noDots := strings.ReplaceAll(clean, ".", "")
	if noDots != clean {
		variants = append(variants, noDots)
	}
	// Trata formato brasileiro ",00" no final (ex: 500.000,00 -> 500000)
	if strings.HasSuffix(noDots, ",00") {
		variants = append(variants, strings.TrimSuffix(noDots, ",00"))
	}
	digitsOnly := strings.Map(func(r rune) rune {
		if unicode.IsDigit(r) {
			return r
		}
		return -1
	}, clean)
	if digitsOnly != "" && digitsOnly != clean {
		variants = append(variants, digitsOnly)
	}
	return variants
}

// DefaultAdminNotifier registra escalonamentos em log, EventBus e tarefa TASK# no Store.
type DefaultAdminNotifier struct {
	store  core.Store
	bus    core.EventBus
	logger *slog.Logger
}

func NewDefaultAdminNotifier(st core.Store, bus core.EventBus, logger *slog.Logger) *DefaultAdminNotifier {
	if logger == nil {
		logger = slog.Default()
	}
	return &DefaultAdminNotifier{
		store:  st,
		bus:    bus,
		logger: logger,
	}
}

func (n *DefaultAdminNotifier) Notify(ctx context.Context, alert core.AdminAlert) error {
	n.logger.Warn("alerta de escalonamento para operador",
		slog.String("tenant", alert.TenantID),
		slog.String("channel", alert.Channel),
		slog.String("source_key", alert.SourceKey),
		slog.String("reason", alert.Reason),
	)

	now := alert.CreatedAt
	if now.IsZero() {
		now = core.SystemClock{}.Now()
	}

	if n.bus != nil {
		n.bus.Publish(core.Event{
			ID:        newID("ev"),
			Type:      core.EventEscalated,
			TenantID:  alert.TenantID,
			Channel:   alert.Channel,
			SourceKey: alert.SourceKey,
			At:        now,
			Payload: map[string]any{
				"reason":  alert.Reason,
				"message": alert.Message,
				"context": alert.ContextData,
			},
		})
	}

	if n.store != nil {
		meta := make(map[string]string, len(alert.ContextData)+1)
		for k, v := range alert.ContextData {
			meta[k] = v
		}
		meta["reason"] = alert.Reason
		task := core.Task{
			ID:        newID("esc"),
			TenantID:  alert.TenantID,
			Channel:   alert.Channel,
			SourceKey: alert.SourceKey,
			Kind:      "escalation",
			Title:     fmt.Sprintf("Escalonamento: %s", alert.Reason),
			Type:      "escalation",
			Status:    core.TaskStatusPending,
			DueAt:     &now,
			Metadata:  meta,
			CreatedAt: now,
		}
		if err := n.store.SaveTask(ctx, task); err != nil {
			return err
		}
		if n.bus != nil {
			n.bus.Publish(core.Event{
				ID:        newID("ev"),
				Type:      core.EventTaskCreated,
				TenantID:  task.TenantID,
				Channel:   task.Channel,
				SourceKey: task.SourceKey,
				At:        now,
				Payload: map[string]any{
					"task_id": task.ID,
					"kind":    task.Kind,
					"type":    task.Type,
					"title":   task.Title,
				},
			})
		}
	}
	return nil
}
