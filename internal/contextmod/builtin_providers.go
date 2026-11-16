package contextmod

import (
	"context"
	"encoding/json"
	"strings"

	"whatshelper/internal/core"
)

type summaryProvider struct {
	store core.Store
}

func (p *summaryProvider) Name() string { return "summary" }
func (p *summaryProvider) Schema() ProviderSchema {
	return ProviderSchema{Name: "summary", Description: "Resumo cumulativo e metadados da fonte", BlockKind: "summary"}
}
func (p *summaryProvider) Fetch(ctx context.Context, req ProviderRequest) (*ProviderResult, error) {
	state, err := p.store.GetSourceState(ctx, req.Scope.TenantID, req.Scope.Channel, req.Scope.SourceKey)
	if err != nil {
		return nil, err
	}
	return &ProviderResult{
		SourceVersion:      state.Version,
		SourceStatus:       string(state.Meta.Status),
		SourceJourneyStage: state.Meta.JourneyStage,
		Summary: &SummaryView{
			Version:   ContractVersion,
			Scope:     req.Scope,
			Text:      state.Summary.Text,
			TurnCount: state.Summary.TurnCount,
			UpdatedAt: state.Summary.UpdatedAt,
		},
	}, nil
}

type factsProvider struct {
	store core.Store
}

func (p *factsProvider) Name() string { return "facts" }
func (p *factsProvider) Schema() ProviderSchema {
	return ProviderSchema{Name: "facts", Description: "Fatos estruturados extraídos", BlockKind: "facts"}
}
func (p *factsProvider) Fetch(ctx context.Context, req ProviderRequest) (*ProviderResult, error) {
	state, err := p.store.GetSourceState(ctx, req.Scope.TenantID, req.Scope.Channel, req.Scope.SourceKey)
	if err != nil {
		return nil, err
	}
	copied := make(map[string]string, len(state.Facts))
	for k, v := range state.Facts {
		copied[k] = v
	}
	return &ProviderResult{
		SourceVersion:      state.Version,
		SourceStatus:       string(state.Meta.Status),
		SourceJourneyStage: state.Meta.JourneyStage,
		Facts:              copied,
	}, nil
}

type signalsProvider struct {
	store core.Store
}

func (p *signalsProvider) Name() string { return "signals" }
func (p *signalsProvider) Schema() ProviderSchema {
	return ProviderSchema{Name: "signals", Description: "Sinais booleanos e contexto estruturado", BlockKind: "signals"}
}
func (p *signalsProvider) Fetch(ctx context.Context, req ProviderRequest) (*ProviderResult, error) {
	state, err := p.store.GetSourceState(ctx, req.Scope.TenantID, req.Scope.Channel, req.Scope.SourceKey)
	if err != nil {
		return nil, err
	}
	sigs := make(map[string]bool, len(state.Signals))
	for k, v := range state.Signals {
		sigs[k] = v
	}
	sc := toStructuredContextView(req.Scope, state.Context)
	return &ProviderResult{
		SourceVersion:      state.Version,
		SourceStatus:       string(state.Meta.Status),
		SourceJourneyStage: state.Meta.JourneyStage,
		Signals:            sigs,
		StructuredContext:  sc,
	}, nil
}

type recentMessagesProvider struct {
	store        core.Store
	blobs        core.BlobStore
	defaultLimit int
}

func (p *recentMessagesProvider) Name() string { return "recent_messages" }
func (p *recentMessagesProvider) Schema() ProviderSchema {
	return ProviderSchema{Name: "recent_messages", Description: "Janela cronológica de mensagens recentes", BlockKind: "recent_messages"}
}
const minInboundMessagesInContext = 5

func (p *recentMessagesProvider) Fetch(ctx context.Context, req ProviderRequest) (*ProviderResult, error) {
	limit := req.RecentLimit
	if limit <= 0 {
		limit = p.defaultLimit
	}
	if limit <= 0 {
		limit = 10
	}
	fetchLimit := limit * 3
	if fetchLimit < 30 {
		fetchLimit = 30
	}
	metas, err := p.store.ListRecentMessages(ctx, req.Scope.TenantID, req.Scope.Channel, req.Scope.SourceKey, fetchLimit)
	if err != nil {
		return nil, err
	}

	customerActor := "customer"
	businessActor := "agent"
	if req.Pack != nil {
		if strings.TrimSpace(req.Pack.Actors.Customer) != "" {
			customerActor = req.Pack.Actors.Customer
		}
		if strings.TrimSpace(req.Pack.Actors.Business) != "" {
			businessActor = req.Pack.Actors.Business
		}
	}

	all := make([]MessageView, 0, len(metas))
	for i := len(metas) - 1; i >= 0; i-- {
		meta := metas[i]
		if req.ExcludeMessageIDs[meta.MessageID] {
			continue
		}
		text := ""
		if meta.RawRef != "" && p.blobs != nil {
			if raw, err := p.blobs.Get(ctx, meta.RawRef); err == nil {
				var norm core.NormalizedMessage
				if json.Unmarshal(raw, &norm) == nil {
					text = norm.Text
				}
			}
		}
		if text == "" {
			continue
		}
		sender := customerActor
		if meta.Direction == core.DirectionOutbound {
			sender = businessActor
		}
		all = append(all, MessageView{
			MessageID: meta.MessageID,
			Timestamp: meta.Timestamp,
			Direction: string(meta.Direction),
			Type:      string(meta.Type),
			Sender:    sender,
			Text:      text,
			MediaRef:  meta.MediaRef,
			RawRef:    meta.RawRef,
			Answered:  meta.Answered,
		})
	}
	return &ProviderResult{RecentMessages: selectRecentMessages(all, limit, minInboundMessagesInContext)}, nil
}

// selectRecentMessages seleciona até totalLimit mensagens em ordem cronológica,
// garantindo a inclusão de até minInbound mensagens mais recentes do cliente.
func selectRecentMessages(all []MessageView, totalLimit, minInbound int) []MessageView {
	if totalLimit <= 0 || len(all) <= totalLimit {
		return all
	}
	keep := make([]bool, len(all))
	kept := 0

	// 1) Marca até minInbound mensagens mais recentes do cliente.
	inboundKept := 0
	for i := len(all) - 1; i >= 0 && inboundKept < minInbound && kept < totalLimit; i-- {
		if all[i].Direction == string(core.DirectionInbound) {
			keep[i] = true
			inboundKept++
			kept++
		}
	}

	// 2) Preenche os slots restantes com as mensagens mais recentes (cliente ou robô).
	for i := len(all) - 1; i >= 0 && kept < totalLimit; i-- {
		if !keep[i] {
			keep[i] = true
			kept++
		}
	}

	out := make([]MessageView, 0, kept)
	for i, m := range all {
		if keep[i] {
			out = append(out, m)
		}
	}
	return out
}

type knowledgeProvider struct {
	reader KnowledgeReader
}

func (p *knowledgeProvider) Name() string { return "knowledge" }
func (p *knowledgeProvider) Schema() ProviderSchema {
	return ProviderSchema{Name: "knowledge", Description: "Base de conhecimento Markdown e QA.md", BlockKind: "knowledge"}
}
func (p *knowledgeProvider) Fetch(ctx context.Context, req ProviderRequest) (*ProviderResult, error) {
	if p.reader == nil {
		return &ProviderResult{}, nil
	}
	docs, qa, err := p.reader.Load(ctx, req.Scope.TenantID)
	if err != nil {
		return nil, err
	}
	return &ProviderResult{
		Knowledge: docs,
		QAContent: qa,
	}, nil
}

func toStructuredContextView(scope Scope, c core.ConversationContext) *StructuredContextView {
	sections := make(map[string]map[string]string, len(c.Sections))
	for sk, fields := range c.Sections {
		fc := make(map[string]string, len(fields))
		for fk, fv := range fields {
			fc[fk] = fv
		}
		sections[sk] = fc
	}
	details := make(map[string]string, len(c.Details))
	for k, v := range c.Details {
		details[k] = v
	}
	if len(sections) == 0 {
		sections = nil
	}
	if len(details) == 0 {
		details = nil
	}
	return &StructuredContextView{
		Version:        ContractVersion,
		Scope:          scope,
		LastActionType: c.LastActionType,
		LastActionBy:   c.LastActionBy,
		Description:    c.Description,
		Details:        details,
		Sections:       sections,
		UpdatedAt:      c.UpdatedAt,
	}
}
