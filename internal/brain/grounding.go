package brain

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"
	"unicode"

	"whatshelper/internal/core"
	"whatshelper/internal/pack"
	"whatshelper/internal/store"
)

const (
	// AdminChannelID identifica o canal separado de comunicação com o dono/operador.
	AdminChannelID = "admin"
	// AdminOwnerKey identifica a conversa padrão do operador no canal admin.
	AdminOwnerKey = "owner"
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

// LooksLikeInternalJSON detecta se o texto aparenta ser um envelope JSON interno
// (mesmo truncado ou cercado por markdown) que nunca deve ser enviado ao cliente.
func LooksLikeInternalJSON(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return false
	}
	unwrapped := trimmed
	if strings.HasPrefix(unwrapped, "```") {
		if idx := strings.IndexByte(unwrapped, '\n'); idx >= 0 {
			unwrapped = strings.TrimSpace(unwrapped[idx+1:])
		}
	}
	if !strings.HasPrefix(unwrapped, "{") {
		return false
	}
	lower := strings.ToLower(unwrapped)
	for _, key := range []string{`"answerable"`, `"sources"`, `"reply"`, `"facts"`, `"signals"`, `"decision"`, `"summary"`} {
		if strings.Contains(lower, key) {
			return true
		}
	}
	return false
}

// ParseReplyOutput tenta extrair o JSON estruturado {answerable, sources, reply}.
// Quando o prompt do pack não usa envelope JSON, retorna fallback com isStructured=false.
// Se o texto parecer um JSON interno malformado ou truncado, retorna Answerable=false e Reply vazio
// para impedir vazamento de JSON interno ao cliente.
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
	if LooksLikeInternalJSON(trimmed) {
		return StructuredReply{
			Answerable: false,
			Reply:      "",
		}, true
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
	if LooksLikeInternalJSON(reply.Reply) {
		return GroundingResult{
			Grounded: false,
			Reason:   "resposta contém JSON interno não sanitizado",
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

// DefaultAdminNotifier registra escalonamentos em log, EventBus, tarefa TASK# e canal admin.
type DefaultAdminNotifier struct {
	store     core.Store
	blobs     core.BlobStore
	bus       core.EventBus
	channelFn func(string) core.Channel
	logger    *slog.Logger
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

// WithAdminDelivery acopla BlobStore e lookup do canal admin para entrega contextualizada ao dono.
func (n *DefaultAdminNotifier) WithAdminDelivery(blobs core.BlobStore, channelFn func(string) core.Channel) *DefaultAdminNotifier {
	n.blobs = blobs
	n.channelFn = channelFn
	return n
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

	var taskID string
	if n.store != nil {
		meta := make(map[string]string, len(alert.ContextData)+4)
		for k, v := range alert.ContextData {
			meta[k] = v
		}
		meta["reason"] = alert.Reason
		if _, ok := meta["origin_channel"]; !ok {
			meta["origin_channel"] = alert.Channel
		}
		if _, ok := meta["origin_source_key"]; !ok {
			meta["origin_source_key"] = alert.SourceKey
		}
		if _, ok := meta["question"]; !ok && strings.TrimSpace(alert.Message) != "" {
			meta["question"] = alert.Message
		}
		taskID = newID("esc")
		task := core.Task{
			ID:        taskID,
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

	n.deliverToAdminChannel(ctx, alert, taskID, now)
	return nil
}

func (n *DefaultAdminNotifier) deliverToAdminChannel(ctx context.Context, alert core.AdminAlert, taskID string, now time.Time) {
	if n.channelFn == nil && n.store == nil {
		return
	}
	var adminCh core.Channel
	if n.channelFn != nil {
		adminCh = n.channelFn(AdminChannelID)
	}
	if adminCh == nil {
		return
	}

	summary := strings.TrimSpace(alert.ContextData["summary"])
	var b strings.Builder
	fmt.Fprintf(&b, "[Escalonamento %s] Origem: %s:%s\n", taskID, alert.Channel, alert.SourceKey)
	fmt.Fprintf(&b, "Motivo: %s\n", alert.Reason)
	if summary != "" {
		fmt.Fprintf(&b, "Resumo: %s\n", summary)
	}
	fmt.Fprintf(&b, "Pergunta do cliente: %s\n", strings.TrimSpace(alert.Message))
	b.WriteString("Responda nesta conversa para gerar uma proposta de aprendizado em QA.md.")

	outMsg := core.NormalizedMessage{
		TenantID:  alert.TenantID,
		Channel:   AdminChannelID,
		SourceKey: AdminOwnerKey,
		MessageID: newID("adm"),
		Timestamp: now,
		Direction: core.DirectionOutbound,
		Type:      core.MessageTypeText,
		Text:      b.String(),
	}
	if n.blobs != nil {
		outMsg.RawRef = store.S3RawMessageKey(alert.TenantID, AdminChannelID, AdminOwnerKey, now, outMsg.MessageID)
		if rawBytes, err := json.Marshal(outMsg); err == nil {
			_ = n.blobs.Put(ctx, outMsg.RawRef, rawBytes, "application/json")
		}
	}
	if n.store != nil {
		_ = n.store.PutMessageMeta(ctx, core.MessageMeta{
			TenantID:  alert.TenantID,
			Channel:   AdminChannelID,
			SourceKey: AdminOwnerKey,
			MessageID: outMsg.MessageID,
			Timestamp: now,
			Direction: core.DirectionOutbound,
			Type:      core.MessageTypeText,
			RawRef:    outMsg.RawRef,
			Sender:    "brain",
		})
	}
	_ = adminCh.Send(ctx, outMsg)
	if n.bus != nil {
		n.bus.Publish(core.Event{
			ID:        newID("ev"),
			Type:      core.EventAdminMessage,
			TenantID:  alert.TenantID,
			Channel:   AdminChannelID,
			SourceKey: AdminOwnerKey,
			MessageID: outMsg.MessageID,
			At:        now,
			Payload: map[string]any{
				"direction":     string(core.DirectionOutbound),
				"text":          outMsg.Text,
				"escalation_id": taskID,
			},
		})
	}
}
