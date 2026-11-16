package brain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"whatshelper/internal/core"
	"whatshelper/internal/pack"
	"whatshelper/internal/store"
)

var (
	ErrProposalNotFound    = errors.New("proposta de aprendizado não encontrada")
	ErrEscalationNotFound  = errors.New("nenhum escalonamento pendente encontrado")
	ErrInvalidLearningJSON = errors.New("resposta do estágio de aprendizado não retornou JSON válido")
)

// LearningProposal representa um par P&R proposto pelo LLM a partir de correção do dono e aguardando aprovação.
type LearningProposal struct {
	ID               string     `json:"id"`
	TenantID         string     `json:"tenant_id"`
	OriginChannel    string     `json:"origin_channel,omitempty"`
	OriginSourceKey  string     `json:"origin_source_key,omitempty"`
	EscalationID     string     `json:"escalation_id,omitempty"`
	CustomerQuestion string     `json:"customer_question,omitempty"`
	WrongReply       string     `json:"wrong_reply,omitempty"`
	OwnerCorrection  string     `json:"owner_correction"`
	Question         string     `json:"question"`
	Answer           string     `json:"answer"`
	Action           string     `json:"action"` // "add" | "merge"
	TargetQuestion   string     `json:"target_question,omitempty"`
	TargetFile       string     `json:"target_file,omitempty"`
	Kind             string     `json:"kind,omitempty"` // "qa" | "character"
	Status           string     `json:"status"` // "pending" | "approved" | "rejected"
	MergeOutcome     string     `json:"merge_outcome,omitempty"`
	ResumedReply     string     `json:"resumed_reply,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	ResolvedAt       *time.Time `json:"resolved_at,omitempty"`
}

// ApproveLearningRequest configura aprovação (com edição opcional) de uma proposta de aprendizado.
type ApproveLearningRequest struct {
	TenantID       string `json:"tenant_id"`
	ProposalID     string `json:"proposal_id,omitempty"`
	Question       string `json:"question,omitempty"`
	Answer         string `json:"answer,omitempty"`
	ResumeCustomer bool   `json:"resume_customer"`
}

// CorrectReplyRequest solicita criação de aprendizado a partir de uma resposta já enviada pelo bot.
type CorrectReplyRequest struct {
	TenantID   string `json:"tenant_id"`
	Channel    string `json:"channel"`
	SourceKey  string `json:"source_key"`
	MessageID  string `json:"message_id,omitempty"`
	Correction string `json:"correction"`
}

type learningLLMOutput struct {
	Question       string `json:"question"`
	Answer         string `json:"answer"`
	Action         string `json:"action"`
	TargetQuestion string `json:"target_question"`
}

// ProposeLearningFromEscalation transforma a resposta do dono em proposta estruturada de QA.md.
func (p *Pipeline) ProposeLearningFromEscalation(ctx context.Context, tenantID, escalationID, ownerCorrection string) (*LearningProposal, error) {
	if p.cfg.Store == nil || p.cfg.Blobs == nil {
		return nil, errors.New("store ou blob store não configurado")
	}
	if strings.TrimSpace(ownerCorrection) == "" {
		return nil, errors.New("texto da resposta/correção do dono é obrigatório")
	}
	if strings.TrimSpace(tenantID) == "" {
		tenantID = "demo"
	}

	esc, err := p.findPendingEscalation(ctx, tenantID, escalationID)
	if err != nil {
		return nil, err
	}

	originChannel := esc.Metadata["origin_channel"]
	if originChannel == "" {
		originChannel = esc.Channel
	}
	originSource := esc.Metadata["origin_source_key"]
	if originSource == "" {
		originSource = esc.SourceKey
	}
	customerQuestion := strings.TrimSpace(esc.Metadata["question"])
	if customerQuestion == "" {
		customerQuestion = esc.Title
	}

	return p.createLearningProposal(ctx, proposalBuildInput{
		TenantID:         tenantID,
		OriginChannel:    originChannel,
		OriginSourceKey:  originSource,
		EscalationID:     esc.ID,
		CustomerQuestion: customerQuestion,
		Summary:          esc.Metadata["summary"],
		OwnerCorrection:  strings.TrimSpace(ownerCorrection),
	})
}

// ProposeLearningCorrection gera uma proposta de aprendizado corrigindo uma resposta já enviada ao cliente (LRN-06).
func (p *Pipeline) ProposeLearningCorrection(ctx context.Context, req CorrectReplyRequest) (*LearningProposal, error) {
	if p.cfg.Store == nil || p.cfg.Blobs == nil {
		return nil, errors.New("store ou blob store não configurado")
	}
	tenantID := strings.TrimSpace(req.TenantID)
	if tenantID == "" {
		tenantID = "demo"
	}
	channelID := strings.TrimSpace(req.Channel)
	if channelID == "" {
		channelID = "simulator"
	}
	sourceKey := strings.TrimSpace(req.SourceKey)
	if sourceKey == "" {
		return nil, errors.New("source_key da conversa é obrigatório para /corrigir")
	}
	correction := strings.TrimSpace(req.Correction)
	if correction == "" {
		return nil, errors.New("texto da correção é obrigatório")
	}

	wrongReply, customerQuestion := p.findWrongReplyContext(ctx, tenantID, channelID, sourceKey, req.MessageID)
	if customerQuestion == "" {
		customerQuestion = "Correção de atendimento em " + channelID + ":" + sourceKey
	}

	state, _ := p.cfg.Store.GetSourceState(ctx, tenantID, channelID, sourceKey)
	summary := ""
	if state != nil {
		summary = state.Summary.Text
	}

	return p.createLearningProposal(ctx, proposalBuildInput{
		TenantID:         tenantID,
		OriginChannel:    channelID,
		OriginSourceKey:  sourceKey,
		CustomerQuestion: customerQuestion,
		WrongReply:       wrongReply,
		Summary:          summary,
		OwnerCorrection:  correction,
	})
}

type proposalBuildInput struct {
	TenantID         string
	OriginChannel    string
	OriginSourceKey  string
	EscalationID     string
	CustomerQuestion string
	WrongReply       string
	Summary          string
	OwnerCorrection  string
}

func (p *Pipeline) createLearningProposal(ctx context.Context, in proposalBuildInput) (*LearningProposal, error) {
	existingQA := p.loadExistingQAEntries(ctx, in.TenantID)
	parsed, err := p.synthesizeQAWithLLM(ctx, in, existingQA)
	if err != nil {
		return nil, err
	}

	now := p.now()
	propID := newID("lrn")
	prop := LearningProposal{
		ID:               propID,
		TenantID:         in.TenantID,
		OriginChannel:    in.OriginChannel,
		OriginSourceKey:  in.OriginSourceKey,
		EscalationID:     in.EscalationID,
		CustomerQuestion: in.CustomerQuestion,
		WrongReply:       in.WrongReply,
		OwnerCorrection:  in.OwnerCorrection,
		Question:         strings.TrimSpace(parsed.Question),
		Answer:           strings.TrimSpace(parsed.Answer),
		Action:           normalizeProposalAction(parsed.Action),
		TargetQuestion:   strings.TrimSpace(parsed.TargetQuestion),
		Status:           "pending",
		CreatedAt:        now,
	}

	blobRef := store.S3LearningKey(in.TenantID, propID)
	propBytes, err := json.Marshal(prop)
	if err != nil {
		return nil, err
	}
	if err := p.cfg.Blobs.Put(ctx, blobRef, propBytes, "application/json"); err != nil {
		return nil, fmt.Errorf("erro ao salvar proposta de aprendizado no BlobStore: %w", err)
	}

	taskChannel := in.OriginChannel
	if taskChannel == "" {
		taskChannel = AdminChannelID
	}
	taskSource := in.OriginSourceKey
	if taskSource == "" {
		taskSource = AdminOwnerKey
	}

	task := core.Task{
		ID:           propID,
		TenantID:     in.TenantID,
		Channel:      taskChannel,
		SourceKey:    taskSource,
		Kind:         "learning",
		Title:        "Aprovar aprendizado: " + truncateText(prop.Question, 70),
		Type:         "qa_learning",
		Status:       core.TaskStatusPending,
		DueAt:        &now,
		DraftBlobRef: blobRef,
		Metadata: map[string]string{
			"proposal_id":       propID,
			"origin_channel":    in.OriginChannel,
			"origin_source_key": in.OriginSourceKey,
			"escalation_id":     in.EscalationID,
			"question":          prop.Question,
			"answer":            prop.Answer,
		},
		CreatedAt: now,
	}
	if err := p.saveAndPublishTask(ctx, task); err != nil {
		return nil, err
	}

	p.publish(core.Event{
		ID:        newID("ev"),
		Type:      core.EventLearningProposed,
		TenantID:  in.TenantID,
		Channel:   taskChannel,
		SourceKey: taskSource,
		At:        now,
		Payload: map[string]any{
			"proposal_id": prop.ID,
			"question":    prop.Question,
			"answer":      prop.Answer,
			"action":      prop.Action,
		},
	})

	preview := fmt.Sprintf("[Proposta %s]\nP: %s\nR: %s\nEnvie '/aprovar %s' (ou '/aprovar %s | resposta editada') para gravar em QA.md, ou '/rejeitar %s'.",
		prop.ID, prop.Question, prop.Answer, prop.ID, prop.ID, prop.ID)
	_ = p.deliverAdminMessage(ctx, in.TenantID, AdminOwnerKey, preview)

	return &prop, nil
}

func (p *Pipeline) synthesizeQAWithLLM(ctx context.Context, in proposalBuildInput, existing []QAEntry) (learningLLMOutput, error) {
	if p.cfg.LLM == nil {
		return learningLLMOutput{}, errors.New("LLM não configurado para aprendizado")
	}

	var existingList strings.Builder
	for _, e := range existing {
		fmt.Fprintf(&existingList, "- %s\n", e.Question)
	}

	system := `Você extrai conhecimento reutilizável para um arquivo QA.md a partir da correção do operador.
Retorne APENAS um objeto JSON válido (sem cercas markdown):
{"question": "pergunta clara e genérica", "answer": "resposta objetiva e fiel à correção do dono", "action": "add|merge", "target_question": "pergunta existente idêntica ou vazio"}
Regras:
- Não invente dados além do que o dono informou.
- Se uma pergunta equivalente já existir em PERGUNTAS EXISTENTES, use action="merge" e preencha target_question com a pergunta existente exata.`

	var user strings.Builder
	if in.Summary != "" {
		fmt.Fprintf(&user, "RESUMO DO ATENDIMENTO: %s\n", in.Summary)
	}
	fmt.Fprintf(&user, "PERGUNTA DO CLIENTE: %s\n", in.CustomerQuestion)
	if in.WrongReply != "" {
		fmt.Fprintf(&user, "RESPOSTA ANTERIOR INCORRETA: %s\n", in.WrongReply)
	}
	fmt.Fprintf(&user, "CORREÇÃO DO DONO: %s\n", in.OwnerCorrection)
	if existingList.Len() > 0 {
		fmt.Fprintf(&user, "PERGUNTAS EXISTENTES EM QA.md:\n%s", existingList.String())
	}

	resp, err := p.cfg.LLM.Generate(ctx, core.LLMRequest{
		Stage:       "learning",
		Model:       p.learningModel(),
		System:      system,
		Prompt:      user.String(),
		Temperature: 0.1,
		MaxTokens:   512,
	})
	if err != nil {
		return learningLLMOutput{}, err
	}

	jsonStr, err := ExtractJSON(resp.Content)
	if err != nil {
		return learningLLMOutput{}, fmt.Errorf("%w: %v", ErrInvalidLearningJSON, err)
	}
	var out learningLLMOutput
	if err := json.Unmarshal([]byte(jsonStr), &out); err != nil {
		return learningLLMOutput{}, fmt.Errorf("%w: %v", ErrInvalidLearningJSON, err)
	}
	if strings.TrimSpace(out.Question) == "" {
		out.Question = in.CustomerQuestion
	}
	if strings.TrimSpace(out.Answer) == "" {
		out.Answer = in.OwnerCorrection
	}
	return out, nil
}

// ApproveLearningProposal aplica (com edição opcional) a proposta em QA.md versionado no BlobStore, invalida cache e retoma o cliente se solicitado.
func (p *Pipeline) ApproveLearningProposal(ctx context.Context, req ApproveLearningRequest) (*LearningProposal, error) {
	tenantID := strings.TrimSpace(req.TenantID)
	if tenantID == "" {
		tenantID = "demo"
	}
	task, prop, err := p.loadLearningTaskAndProposal(ctx, tenantID, req.ProposalID)
	if err != nil {
		return nil, err
	}
	if task.Status != core.TaskStatusPending {
		return nil, fmt.Errorf("proposta %q já foi processada (status=%s)", task.ID, task.Status)
	}

	if q := strings.TrimSpace(req.Question); q != "" {
		prop.Question = q
	}
	if a := strings.TrimSpace(req.Answer); a != "" {
		prop.Answer = a
	}
	if strings.TrimSpace(prop.Question) == "" || strings.TrimSpace(prop.Answer) == "" {
		return nil, errors.New("pergunta e resposta não podem ficar vazias na aprovação")
	}

	targetFile := prop.TargetFile
	if targetFile == "" {
		targetFile = "QA.md"
	}

	var outcome QAMergeOutcome
	if prop.Kind == "character" || strings.HasSuffix(targetFile, "character.md") {
		key := store.S3KnowledgeKey(tenantID, targetFile)
		var currentContent string
		if raw, bErr := p.cfg.Blobs.Get(ctx, key); bErr == nil {
			currentContent = string(raw)
		} else {
			currentContent = "# Personalidade do Atendente (Character)\nDiretrizes de comunicação, tom de voz e vocabulário aprendidos do atendimento.\n"
		}
		newSection := fmt.Sprintf("\n## %s\n- **Exemplo de uso:** %s\n", prop.Question, prop.Answer)
		updatedContent := strings.TrimSpace(currentContent) + "\n" + newSection

		if err := p.cfg.Blobs.Put(ctx, key, []byte(updatedContent), "text/markdown; charset=utf-8"); err != nil {
			return nil, fmt.Errorf("erro ao gravar arquivo de personalidade %s: %w", targetFile, err)
		}
		outcome = QAMergeAdded
	} else {
		// Dedupe/merge determinístico em QA.md ou QA-listed.md e escrita versionada no S3/MinIO
		existing := p.loadExistingQAEntriesForFile(ctx, tenantID, targetFile)
		merged, mOutcome, _ := MergeQAEntry(existing, QAEntry{
			Question: prop.Question,
			Answer:   prop.Answer,
		}, prop.TargetQuestion)
		outcome = mOutcome
		qaBody := FormatQA(merged)

		qaKey := store.S3KnowledgeKey(tenantID, targetFile)
		if err := p.cfg.Blobs.Put(ctx, qaKey, []byte(qaBody), "text/markdown; charset=utf-8"); err != nil {
			return nil, fmt.Errorf("erro ao gravar %s versionado: %w", targetFile, err)
		}
	}

	// Hot reload imediato do cache de conhecimento
	if inv, ok := p.cfg.Knowledge.(interface{ Invalidate(string) }); ok {
		inv.Invalidate(tenantID)
	}

	now := p.now()
	prop.Status = "approved"
	prop.MergeOutcome = string(outcome)
	prop.ResolvedAt = &now

	// Retomada opcional ao cliente aguardando resposta
	if req.ResumeCustomer && prop.OriginChannel != "" && prop.OriginSourceKey != "" && prop.OriginChannel != AdminChannelID {
		if sentText, resumeErr := p.resumeCustomerAfterLearning(ctx, *prop); resumeErr == nil && sentText != "" {
			prop.ResumedReply = sentText
		} else if resumeErr != nil {
			p.logger.Warn("retomada do cliente após aprendizado não enviada",
				"tenant", tenantID,
				"origin", prop.OriginChannel+":"+prop.OriginSourceKey,
				"error", resumeErr,
			)
		}
	}

	if updatedBytes, err := json.Marshal(prop); err == nil && task.DraftBlobRef != "" {
		_ = p.cfg.Blobs.Put(ctx, task.DraftBlobRef, updatedBytes, "application/json")
	}

	task.Status = core.TaskStatusCompleted
	task.CompletedAt = &now
	if err := p.cfg.Store.SaveTask(ctx, *task); err != nil {
		return nil, err
	}

	if prop.EscalationID != "" {
		if escTask, err := p.cfg.Store.GetTask(ctx, tenantID, prop.OriginChannel, prop.OriginSourceKey, prop.EscalationID); err == nil && escTask.Status == core.TaskStatusPending {
			escTask.Status = core.TaskStatusCompleted
			escTask.CompletedAt = &now
			_ = p.cfg.Store.SaveTask(ctx, *escTask)
		}
	}

	p.publish(core.Event{
		ID:        newID("ev"),
		Type:      core.EventLearned,
		TenantID:  tenantID,
		Channel:   prop.OriginChannel,
		SourceKey: prop.OriginSourceKey,
		At:        now,
		Payload: map[string]any{
			"proposal_id":   prop.ID,
			"question":      prop.Question,
			"answer":        prop.Answer,
			"merge_outcome": prop.MergeOutcome,
			"resumed_reply": prop.ResumedReply,
		},
	})

	return prop, nil
}

// RejectLearningProposal descarta uma proposta pendente sem alterar QA.md (LRN-03).
func (p *Pipeline) RejectLearningProposal(ctx context.Context, tenantID, proposalID string) (*LearningProposal, error) {
	if strings.TrimSpace(tenantID) == "" {
		tenantID = "demo"
	}
	task, prop, err := p.loadLearningTaskAndProposal(ctx, tenantID, proposalID)
	if err != nil {
		return nil, err
	}
	if task.Status != core.TaskStatusPending {
		return nil, fmt.Errorf("proposta %q já foi processada (status=%s)", task.ID, task.Status)
	}

	now := p.now()
	prop.Status = "rejected"
	prop.ResolvedAt = &now
	if updatedBytes, err := json.Marshal(prop); err == nil && task.DraftBlobRef != "" {
		_ = p.cfg.Blobs.Put(ctx, task.DraftBlobRef, updatedBytes, "application/json")
	}

	task.Status = core.TaskStatusCancelled
	task.CompletedAt = &now
	if err := p.cfg.Store.SaveTask(ctx, *task); err != nil {
		return nil, err
	}
	return prop, nil
}

// ListPendingLearningProposals retorna todas as propostas de aprendizado pendentes do tenant ordenadas da mais recente à mais antiga.
func (p *Pipeline) ListPendingLearningProposals(ctx context.Context, tenantID string) ([]LearningProposal, error) {
	if p.cfg.Store == nil {
		return nil, nil
	}
	if strings.TrimSpace(tenantID) == "" {
		tenantID = "demo"
	}
	due, err := p.cfg.Store.ListDueTasks(ctx, tenantID, p.now().Add(365*24*time.Hour))
	if err != nil {
		return nil, err
	}
	var out []LearningProposal
	for _, t := range due {
		if t.Kind != "learning" || t.Status != core.TaskStatusPending {
			continue
		}
		if t.DraftBlobRef != "" && p.cfg.Blobs != nil {
			if raw, err := p.cfg.Blobs.Get(ctx, t.DraftBlobRef); err == nil {
				var prop LearningProposal
				if json.Unmarshal(raw, &prop) == nil {
					out = append(out, prop)
					continue
				}
			}
		}
		out = append(out, LearningProposal{
			ID:              t.ID,
			TenantID:        t.TenantID,
			OriginChannel:   t.Metadata["origin_channel"],
			OriginSourceKey: t.Metadata["origin_source_key"],
			EscalationID:    t.Metadata["escalation_id"],
			Question:        t.Metadata["question"],
			Answer:          t.Metadata["answer"],
			Status:          string(t.Status),
			CreatedAt:       t.CreatedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (p *Pipeline) resumeCustomerAfterLearning(ctx context.Context, prop LearningProposal) (string, error) {
	state, err := p.cfg.Store.GetSourceState(ctx, prop.TenantID, prop.OriginChannel, prop.OriginSourceKey)
	if err != nil {
		return "", err
	}
	switch state.Meta.Status {
	case core.SourceStatusPaused, core.SourceStatusHuman, core.SourceStatusOptedOut, core.SourceStatusArchived:
		return "", fmt.Errorf("fonte bloqueada (%s)", state.Meta.Status)
	}

	tenantCfg, err := p.cfg.Tenants.LoadTenant(ctx, prop.TenantID)
	if err != nil {
		return "", err
	}
	if !tenantCfg.BotEnabled {
		return "", errors.New("bot desabilitado para o tenant")
	}
	if tenantCfg.BusinessHours.Enabled && !core.IsWithinBusinessHours(tenantCfg, p.now()) {
		return "", errors.New("fora do horário comercial")
	}
	if ok, reason := p.limiter.Allow(prop.TenantID, prop.OriginChannel, prop.OriginSourceKey, tenantCfg.RateLimit); !ok {
		return "", errors.New(reason)
	}

	pk, err := p.cfg.PackLoader(tenantCfg.Pack)
	if err != nil {
		return "", err
	}
	knowledge, err := p.cfg.Knowledge.Load(ctx, prop.TenantID)
	if err != nil {
		return "", err
	}
	ch := p.getChannel(prop.OriginChannel)
	plainTextOnly := ch == nil || ch.Capabilities().PlainTextOnly

	replyText := strings.TrimSpace(prop.Answer)
	if p.cfg.LLM != nil {
		recent, _ := p.loadRecentPromptMessages(ctx, pk, prop.TenantID, prop.OriginChannel, prop.OriginSourceKey, nil)
		newMsg := pack.PromptMessage{
			Sender:    pk.Actors.Customer,
			Text:      prop.CustomerQuestion,
			Timestamp: p.now().Format(time.RFC3339),
		}
		rendered, rErr := pack.RenderReply(pk, pack.PromptInput{
			Pack:           pk,
			Summary:        state.Summary.Text,
			Facts:          state.Facts,
			Signals:        state.Signals,
			Context:        state.Context.Sections,
			RecentMessages: recent,
			NewMessage:     newMsg,
			Knowledge:      knowledge.Docs,
			QAContent:      knowledge.QAContent,
			PlainTextOnly:  plainTextOnly,
		})
		if rErr == nil {
			if resp, gErr := p.cfg.LLM.Generate(ctx, core.LLMRequest{
				Stage:       "response",
				Model:       p.cfg.ResponseModel,
				System:      rendered.System,
				Prompt:      rendered.User,
				Temperature: 0.2,
				MaxTokens:   512,
			}); gErr == nil {
				parsed, _ := ParseReplyOutput(resp.Content)
				gr := CheckGrounding(parsed, knowledge, recent, newMsg, state.Facts)
				if gr.Grounded && strings.TrimSpace(parsed.Reply) != "" {
					replyText = parsed.Reply
				}
			}
		}
	}

	if plainTextOnly {
		replyText = SanitizePlainText(replyText)
	}
	if replyText == "" {
		return "", nil
	}

	runID := newID("resume")
	if _, err := p.DeliverTextOutbound(ctx, prop.TenantID, prop.OriginChannel, "", prop.OriginSourceKey, pk.Actors.Business, replyText, runID, ""); err != nil {
		return "", err
	}
	return replyText, nil
}

func (p *Pipeline) findPendingEscalation(ctx context.Context, tenantID, escalationID string) (core.Task, error) {
	due, err := p.cfg.Store.ListDueTasks(ctx, tenantID, p.now().Add(365*24*time.Hour))
	if err != nil {
		return core.Task{}, err
	}
	var candidates []core.Task
	for _, t := range due {
		if t.Kind != "escalation" || t.Status != core.TaskStatusPending {
			continue
		}
		if escalationID != "" && t.ID == escalationID {
			return t, nil
		}
		candidates = append(candidates, t)
	}
	if escalationID != "" || len(candidates) == 0 {
		return core.Task{}, ErrEscalationNotFound
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].CreatedAt.After(candidates[j].CreatedAt)
	})
	return candidates[0], nil
}

func (p *Pipeline) loadLearningTaskAndProposal(ctx context.Context, tenantID, proposalID string) (*core.Task, *LearningProposal, error) {
	due, err := p.cfg.Store.ListDueTasks(ctx, tenantID, p.now().Add(365*24*time.Hour))
	if err != nil {
		return nil, nil, err
	}
	var matching []core.Task
	for _, t := range due {
		if t.Kind != "learning" || t.Status != core.TaskStatusPending {
			continue
		}
		if proposalID != "" && t.ID == proposalID {
			matching = []core.Task{t}
			break
		}
		matching = append(matching, t)
	}
	if len(matching) == 0 {
		return nil, nil, ErrProposalNotFound
	}
	sort.Slice(matching, func(i, j int) bool {
		return matching[i].CreatedAt.After(matching[j].CreatedAt)
	})
	if proposalID != "" && matching[0].ID != proposalID {
		return nil, nil, ErrProposalNotFound
	}

	task := matching[0]
	var prop LearningProposal
	if task.DraftBlobRef != "" && p.cfg.Blobs != nil {
		raw, err := p.cfg.Blobs.Get(ctx, task.DraftBlobRef)
		if err != nil {
			return nil, nil, err
		}
		if err := json.Unmarshal(raw, &prop); err != nil {
			return nil, nil, err
		}
	} else {
		prop = LearningProposal{
			ID:              task.ID,
			TenantID:        task.TenantID,
			OriginChannel:   task.Metadata["origin_channel"],
			OriginSourceKey: task.Metadata["origin_source_key"],
			EscalationID:    task.Metadata["escalation_id"],
			Question:        task.Metadata["question"],
			Answer:          task.Metadata["answer"],
			Status:          string(task.Status),
			CreatedAt:       task.CreatedAt,
		}
	}
	return &task, &prop, nil
}

func (p *Pipeline) findWrongReplyContext(ctx context.Context, tenantID, channelID, sourceKey, targetMsgID string) (wrongReply, question string) {
	metas, err := p.cfg.Store.ListRecentMessages(ctx, tenantID, channelID, sourceKey, 20)
	if err != nil {
		return "", ""
	}
	foundWrong := false
	for _, m := range metas {
		text := ""
		if m.RawRef != "" {
			if raw, err := p.cfg.Blobs.Get(ctx, m.RawRef); err == nil {
				var norm core.NormalizedMessage
				if json.Unmarshal(raw, &norm) == nil {
					text = norm.Text
				}
			}
		}
		if !foundWrong && m.Direction == core.DirectionOutbound {
			if targetMsgID == "" || m.MessageID == targetMsgID {
				wrongReply = text
				foundWrong = true
				continue
			}
		}
		if m.Direction == core.DirectionInbound && text != "" {
			question = text
			if foundWrong {
				break
			}
		}
	}
	return wrongReply, question
}

func (p *Pipeline) loadExistingQAEntries(ctx context.Context, tenantID string) []QAEntry {
	return p.loadExistingQAEntriesForFile(ctx, tenantID, "QA.md")
}

func (p *Pipeline) loadExistingQAEntriesForFile(ctx context.Context, tenantID, filename string) []QAEntry {
	if p.cfg.Blobs == nil {
		return nil
	}
	if strings.TrimSpace(filename) == "" {
		filename = "QA.md"
	}
	raw, err := p.cfg.Blobs.Get(ctx, store.S3KnowledgeKey(tenantID, filename))
	if err != nil {
		return nil
	}
	return ParseQA(string(raw))
}

func (p *Pipeline) learningModel() string {
	if strings.TrimSpace(p.cfg.LearningModel) != "" {
		return p.cfg.LearningModel
	}
	if strings.TrimSpace(p.cfg.ResponseModel) != "" {
		return p.cfg.ResponseModel
	}
	return p.cfg.AnalysisModel
}

func normalizeProposalAction(a string) string {
	if strings.EqualFold(strings.TrimSpace(a), "merge") {
		return "merge"
	}
	return "add"
}
