package contextmod

import (
	"context"
	"log/slog"
	"sort"

	"whatshelper/internal/pack"
)

// ProviderSchema descreve os metadados de um ContextProvider plugável (CTX-06).
type ProviderSchema struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	BlockKind   string `json:"block_kind"`
}

// ProviderRequest carrega o escopo, pack e parâmetros necessários para o fetch de um provider.
type ProviderRequest struct {
	Scope             Scope
	Pack              *pack.Pack
	BudgetTokens      int
	RecentLimit       int
	ExcludeMessageIDs map[string]bool
}

// ProviderResult retorna os dados parciais produzidos por um ContextProvider para o ContextBuilder.
type ProviderResult struct {
	SourceVersion      int64
	SourceStatus       string
	SourceJourneyStage string
	Summary            *SummaryView
	Facts             map[string]string
	Signals           map[string]bool
	StructuredContext *StructuredContextView
	RecentMessages    []MessageView
	Knowledge         []KnowledgeDocView
	QAContent         string
}

// ContextProvider define o contrato de um provedor plugável de contexto.
type ContextProvider interface {
	Name() string
	Schema() ProviderSchema
	Fetch(ctx context.Context, req ProviderRequest) (*ProviderResult, error)
}

// ProviderRegistry gerencia providers registrados e filtra pelos habilitados no pack.
type ProviderRegistry struct {
	providers map[string]ContextProvider
	order     []string
	logger    *slog.Logger
}

// NewProviderRegistry inicializa um registro ordenado de ContextProviders.
func NewProviderRegistry(logger *slog.Logger, providers ...ContextProvider) *ProviderRegistry {
	if logger == nil {
		logger = slog.Default()
	}
	reg := &ProviderRegistry{
		providers: make(map[string]ContextProvider, len(providers)),
		logger:    logger,
	}
	for _, p := range providers {
		if p != nil {
			reg.Register(p)
		}
	}
	return reg
}

// Register adiciona ou substitui um provider pelo seu nome único.
func (r *ProviderRegistry) Register(p ContextProvider) {
	if p == nil {
		return
	}
	name := p.Name()
	if _, exists := r.providers[name]; !exists {
		r.order = append(r.order, name)
	}
	r.providers[name] = p
}

// EnabledForPack retorna a lista de providers ativos segundo pack.context_module.providers (ou todos se omitido).
func (r *ProviderRegistry) EnabledForPack(pk *pack.Pack) []ContextProvider {
	if pk != nil && pk.ContextModule != nil && len(pk.ContextModule.Providers) > 0 {
		out := make([]ContextProvider, 0, len(pk.ContextModule.Providers))
		for _, name := range pk.ContextModule.Providers {
			if p, ok := r.providers[name]; ok {
				out = append(out, p)
			}
		}
		return out
	}
	out := make([]ContextProvider, 0, len(r.order))
	for _, name := range r.order {
		if p, ok := r.providers[name]; ok {
			out = append(out, p)
		}
	}
	return out
}

// Collect executa os providers necessários isolando falhas individuais sem derrubar o turno (CTX-06).
func (r *ProviderRegistry) Collect(ctx context.Context, req ProviderRequest, neededBlocks map[string]bool) RawContextData {
	raw := RawContextData{Scope: req.Scope}
	for _, p := range r.EnabledForPack(req.Pack) {
		if !providerNeeded(p.Name(), neededBlocks) {
			continue
		}
		res, err := p.Fetch(ctx, req)
		if err != nil {
			r.logger.Warn("provider de contexto falhou; seguindo em modo degradado",
				slog.String("provider", p.Name()),
				slog.String("tenant", req.Scope.TenantID),
				slog.String("channel", req.Scope.Channel),
				slog.String("source_key", req.Scope.SourceKey),
				slog.Any("error", err),
			)
			raw.DegradedProviders = append(raw.DegradedProviders, p.Name())
			continue
		}
		if res == nil {
			continue
		}
		mergeProviderResult(&raw, res)
	}
	sort.Strings(raw.DegradedProviders)
	return raw
}

func providerNeeded(name string, needed map[string]bool) bool {
	if len(needed) == 0 {
		return true
	}
	switch name {
	case "summary":
		return needed["summary"] || needed["structured_context"]
	case "facts":
		return needed["facts"]
	case "signals":
		return needed["signals"] || needed["structured_context"]
	case "recent_messages":
		return needed["recent_messages"]
	case "knowledge":
		return needed["knowledge"] || needed["qa"]
	default:
		return needed[name]
	}
}

func mergeProviderResult(dst *RawContextData, src *ProviderResult) {
	if src.SourceVersion > dst.SourceVersion {
		dst.SourceVersion = src.SourceVersion
	}
	if src.SourceStatus != "" {
		dst.SourceStatus = src.SourceStatus
	}
	if src.SourceJourneyStage != "" {
		dst.SourceJourneyStage = src.SourceJourneyStage
	}
	if src.Summary != nil {
		dst.Summary = src.Summary
	}
	if len(src.Facts) > 0 {
		dst.Facts = src.Facts
	}
	if len(src.Signals) > 0 {
		dst.Signals = src.Signals
	}
	if src.StructuredContext != nil {
		dst.StructuredContext = src.StructuredContext
	}
	if len(src.RecentMessages) > 0 {
		dst.RecentMessages = src.RecentMessages
	}
	if len(src.Knowledge) > 0 {
		dst.Knowledge = src.Knowledge
	}
	if src.QAContent != "" {
		dst.QAContent = src.QAContent
	}
}
