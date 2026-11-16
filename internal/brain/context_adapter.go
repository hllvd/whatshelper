package brain

import (
	"context"
	"time"

	"whatshelper/internal/contextmod"
	"whatshelper/internal/core"
	"whatshelper/internal/pack"
)

type knowledgeSourceReader struct {
	src KnowledgeSource
}

func (r knowledgeSourceReader) Load(ctx context.Context, tenantID string) ([]contextmod.KnowledgeDocView, string, error) {
	if r.src == nil {
		return nil, "", nil
	}
	bundle, err := r.src.Load(ctx, tenantID)
	if err != nil {
		return nil, "", err
	}
	docs := make([]contextmod.KnowledgeDocView, len(bundle.Docs))
	for i, d := range bundle.Docs {
		docs[i] = contextmod.KnowledgeDocView{Name: d.Name, Content: d.Content}
	}
	return docs, bundle.QAContent, nil
}

type pipelinePackResolver struct {
	tenants TenantConfigLoader
	loader  func(string) (*pack.Pack, error)
}

func (r pipelinePackResolver) ResolvePack(ctx context.Context, tenantID string) (*pack.Pack, error) {
	if r.tenants == nil || r.loader == nil {
		return nil, nil
	}
	tCfg, err := r.tenants.LoadTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	return r.loader(tCfg.Pack)
}

type pipelineLearningController struct {
	p *Pipeline
}

func (c pipelineLearningController) ListPending(ctx context.Context, tenantID string) ([]contextmod.LearningProposalView, error) {
	props, err := c.p.ListPendingLearningProposals(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	out := make([]contextmod.LearningProposalView, len(props))
	for i, pr := range props {
		out[i] = toLearningProposalView(pr)
	}
	return out, nil
}

func (c pipelineLearningController) Approve(ctx context.Context, tenantID, proposalID, actor, overrideAnswer string) (*contextmod.LearningProposalView, error) {
	prop, err := c.p.ApproveLearningProposal(ctx, ApproveLearningRequest{
		TenantID:   tenantID,
		ProposalID: proposalID,
		Answer:     overrideAnswer,
	})
	if err != nil {
		return nil, err
	}
	view := toLearningProposalView(*prop)
	return &view, nil
}

func (c pipelineLearningController) Reject(ctx context.Context, tenantID, proposalID, actor, reason string) (*contextmod.LearningProposalView, error) {
	prop, err := c.p.RejectLearningProposal(ctx, tenantID, proposalID)
	if err != nil {
		return nil, err
	}
	view := toLearningProposalView(*prop)
	return &view, nil
}

func toLearningProposalView(pr LearningProposal) contextmod.LearningProposalView {
	return contextmod.LearningProposalView{
		Version:         contextmod.ContractVersion,
		ID:              pr.ID,
		TenantID:        pr.TenantID,
		OriginChannel:   pr.OriginChannel,
		OriginSourceKey: pr.OriginSourceKey,
		Question:        pr.Question,
		ProposedAnswer:  pr.Answer,
		FinalAnswer:     pr.Answer,
		Status:          pr.Status,
		CreatedAt:       pr.CreatedAt,
		DecidedAt:       pr.ResolvedAt,
	}
}

func buildDefaultContextService(p *Pipeline) contextmod.Service {
	return contextmod.NewInProcessService(contextmod.InProcessConfig{
		Store:               p.cfg.Store,
		Blobs:               p.cfg.Blobs,
		Knowledge:           knowledgeSourceReader{src: p.cfg.Knowledge},
		Packs:               pipelinePackResolver{tenants: p.cfg.Tenants, loader: p.cfg.PackLoader},
		Learning:            pipelineLearningController{p: p},
		Bus:                 p.cfg.Bus,
		Clock:               p.cfg.Clock,
		RecentMessagesLimit: p.cfg.RecentMessagesLimit,
		Logger:              p.logger,
	})
}

func bundlePromptMessages(b *contextmod.ContextBundle) []pack.PromptMessage {
	if b == nil || len(b.RecentMessages) == 0 {
		return nil
	}
	out := make([]pack.PromptMessage, len(b.RecentMessages))
	for i, m := range b.RecentMessages {
		out[i] = pack.PromptMessage{
			Sender:    m.Sender,
			Text:      m.Text,
			Timestamp: m.Timestamp.UTC().Format(time.RFC3339),
			Answered:  m.Answered,
		}
	}
	return out
}

func buildWindowForBundle(b *contextmod.ContextBundle, batch []core.NormalizedMessage, pk *pack.Pack, maxHist int) (contextmod.WindowOutput, []pack.PromptMessage, pack.PromptMessage) {
	var histIn []contextmod.WindowMessage
	if b != nil {
		histIn = make([]contextmod.WindowMessage, len(b.RecentMessages))
		for i, m := range b.RecentMessages {
			histIn[i] = contextmod.WindowMessage{
				MessageID: m.MessageID,
				Timestamp: m.Timestamp,
				Direction: m.Direction,
				Sender:    m.Sender,
				Text:      m.Text,
				Answered:  m.Answered,
			}
		}
	}
	custActor := "customer"
	if pk != nil && pk.Actors.Customer != "" {
		custActor = pk.Actors.Customer
	}
	batchIn := make([]contextmod.WindowMessage, len(batch))
	for i, m := range batch {
		batchIn[i] = contextmod.WindowMessage{
			MessageID: m.MessageID,
			Timestamp: m.Timestamp,
			Direction: string(core.DirectionInbound),
			Origin:    string(m.Origin),
			Sender:    custActor,
			Text:      m.Text,
			Answered:  false,
		}
	}

	win := contextmod.BuildWindow(contextmod.WindowInput{
		History:            histIn,
		Batch:              batchIn,
		HistoryMaxMessages: maxHist,
		HistoryMinCustomer: contextmod.DefaultWindowHistoryCustomer,
	})

	histMsgs := make([]pack.PromptMessage, len(win.History))
	for i, m := range win.History {
		histMsgs[i] = pack.PromptMessage{
			Sender:    m.Sender,
			Text:      m.Text,
			Timestamp: m.Timestamp.UTC().Format(time.RFC3339),
			Answered:  m.Answered,
		}
	}

	var latestTS string
	if len(batch) > 0 {
		latestTS = batch[len(batch)-1].Timestamp.UTC().Format(time.RFC3339)
	}
	newMsg := pack.PromptMessage{
		Sender:    custActor,
		Text:      win.CombinedBatchText,
		Timestamp: latestTS,
		Answered:  false,
	}
	return win, histMsgs, newMsg
}

func bundleStructuredSections(b *contextmod.ContextBundle) map[string]map[string]string {
	if b == nil || b.StructuredContext == nil {
		return nil
	}
	return b.StructuredContext.Sections
}

func bundleSummaryText(b *contextmod.ContextBundle) string {
	if b == nil || b.Summary == nil {
		return ""
	}
	return b.Summary.Text
}

func bundleKnowledge(b *contextmod.ContextBundle) KnowledgeBundle {
	if b == nil {
		return KnowledgeBundle{}
	}
	docs := make([]pack.KnowledgeDoc, len(b.Knowledge))
	for i, d := range b.Knowledge {
		docs[i] = pack.KnowledgeDoc{Name: d.Name, Content: d.Content}
	}
	return KnowledgeBundle{
		Docs:      docs,
		QAContent: b.QAContent,
	}
}

func factChangesFromDiffs(diffs []contextmod.FactDiff) []FactChange {
	out := make([]FactChange, len(diffs))
	for i, d := range diffs {
		out[i] = FactChange{
			Key:      d.Key,
			Action:   d.Action,
			OldValue: d.OldValue,
			NewValue: d.NewValue,
		}
	}
	return out
}

func toStructuredContextView(scope contextmod.Scope, ctx core.ConversationContext) *contextmod.StructuredContextView {
	return &contextmod.StructuredContextView{
		Version:        contextmod.ContractVersion,
		Scope:          scope,
		LastActionType: ctx.LastActionType,
		LastActionBy:   ctx.LastActionBy,
		Description:    ctx.Description,
		Details:        ctx.Details,
		Sections:       ctx.Sections,
		UpdatedAt:      ctx.UpdatedAt,
	}
}
