package contextmod

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"whatshelper/internal/core"
	"whatshelper/internal/pack"
)

// KnowledgeReader abstrai a leitura de documentos e QA.md por tenant sem acoplar ao pacote brain.
type KnowledgeReader interface {
	Load(ctx context.Context, tenantID string) (docs []KnowledgeDocView, qaContent string, err error)
}

// PackResolver resolve o vertical pack configurado para um tenant.
type PackResolver interface {
	ResolvePack(ctx context.Context, tenantID string) (*pack.Pack, error)
}

// LearningController abstrai listagem e decisão humana sobre propostas de aprendizado.
type LearningController interface {
	ListPending(ctx context.Context, tenantID string) ([]LearningProposalView, error)
	Approve(ctx context.Context, tenantID, proposalID, actor, overrideAnswer string) (*LearningProposalView, error)
	Reject(ctx context.Context, tenantID, proposalID, actor, reason string) (*LearningProposalView, error)
}

// InProcessConfig configura o adaptador in-process sobre Store e BlobStore existentes.
type InProcessConfig struct {
	Store               core.Store
	Blobs               core.BlobStore
	Knowledge           KnowledgeReader
	Packs               PackResolver
	Learning            LearningController
	Bus                 core.EventBus
	Clock               core.Clock
	Searcher            KnowledgeSearcher
	RecentMessagesLimit int
	Logger              *slog.Logger
	ExtraProviders      []ContextProvider
}

// InProcessService implementa Service como fachada direta sobre DynamoDB/MemoryStore e S3/MinIO.
type InProcessService struct {
	cfg      InProcessConfig
	registry *ProviderRegistry
	searcher KnowledgeSearcher
	clock    core.Clock
	logger   *slog.Logger
}

// NewInProcessService cria o adapter in-process com os 5 providers embutidos e busca BM25.
func NewInProcessService(cfg InProcessConfig) *InProcessService {
	clock := cfg.Clock
	if clock == nil {
		clock = core.SystemClock{}
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	searcher := cfg.Searcher
	if searcher == nil {
		searcher = NewBM25Searcher()
	}
	recentLimit := cfg.RecentMessagesLimit
	if recentLimit <= 0 {
		recentLimit = 20
	}

	providers := []ContextProvider{
		&summaryProvider{store: cfg.Store},
		&factsProvider{store: cfg.Store},
		&signalsProvider{store: cfg.Store},
		&recentMessagesProvider{store: cfg.Store, blobs: cfg.Blobs, defaultLimit: recentLimit},
		&knowledgeProvider{reader: cfg.Knowledge},
	}
	providers = append(providers, cfg.ExtraProviders...)

	return &InProcessService{
		cfg:      cfg,
		registry: NewProviderRegistry(logger, providers...),
		searcher: searcher,
		clock:    clock,
		logger:   logger,
	}
}

func (s *InProcessService) GetContext(ctx context.Context, req GetContextRequest) (*ContextBundle, error) {
	if err := req.Scope.Validate(); err != nil {
		return nil, err
	}
	if req.View == "" {
		req.View = ViewResponse
	}
	var pk *pack.Pack
	if s.cfg.Packs != nil {
		loaded, err := s.cfg.Packs.ResolvePack(ctx, req.Scope.TenantID)
		if err != nil {
			return nil, err
		}
		pk = loaded
	}

	spec, err := ResolveViewSpec(req.View, pk, req.BudgetTokens)
	if err != nil {
		return nil, err
	}

	needed := make(map[string]bool, len(spec.Blocks))
	for _, b := range spec.Blocks {
		needed[b] = true
	}
	excl := make(map[string]bool, len(req.ExcludeMessageIDs))
	for _, id := range req.ExcludeMessageIDs {
		excl[id] = true
	}

	raw := s.registry.Collect(ctx, ProviderRequest{
		Scope:             req.Scope,
		Pack:              pk,
		BudgetTokens:      spec.MaxTokens,
		RecentLimit:       req.RecentLimit,
		ExcludeMessageIDs: excl,
	}, needed)
	raw.GeneratedAt = s.clock.Now().UTC()

	return BuildContextBundle(spec, raw), nil
}

func (s *InProcessService) ListMessages(ctx context.Context, scope Scope, cursor string, limit int) (*MessagePage, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	offset := decodeCursor(cursor)

	// Busca janela suficiente para paginação determinística em ordem cronológica (mais antiga -> mais nova).
	fetchLimit := offset + limit + 1
	if fetchLimit < 50 {
		fetchLimit = 50
	}
	metas, err := s.cfg.Store.ListRecentMessages(ctx, scope.TenantID, scope.Channel, scope.SourceKey, fetchLimit)
	if err != nil {
		return nil, err
	}

	// ListRecentMessages vem do mais novo para o mais antigo; ordenamos do mais novo -> mais antigo para cursor estável
	// ou cronológico paginado a partir do offset. Usamos ordem cronológica recente (index 0 = mais recente na página).
	if offset >= len(metas) {
		return &MessagePage{
			Version: ContractVersion,
			Scope:   scope,
			Items:   []MessageView{},
			HasMore: false,
		}, nil
	}

	end := offset + limit
	hasMore := false
	if end < len(metas) {
		hasMore = true
	} else {
		end = len(metas)
	}

	slice := metas[offset:end]
	items := make([]MessageView, 0, len(slice))
	for _, meta := range slice {
		text := ""
		if meta.RawRef != "" && s.cfg.Blobs != nil {
			if raw, err := s.cfg.Blobs.Get(ctx, meta.RawRef); err == nil {
				var norm core.NormalizedMessage
				if json.Unmarshal(raw, &norm) == nil {
					text = norm.Text
				}
			}
		}
		items = append(items, MessageView{
			MessageID: meta.MessageID,
			Timestamp: meta.Timestamp,
			Direction: string(meta.Direction),
			Type:      string(meta.Type),
			Sender:    meta.Sender,
			Text:      text,
			MediaRef:  meta.MediaRef,
			RawRef:    meta.RawRef,
		})
	}

	nextCursor := ""
	if hasMore {
		nextCursor = encodeCursor(end)
	}
	return &MessagePage{
		Version:    ContractVersion,
		Scope:      scope,
		Items:      items,
		NextCursor: nextCursor,
		HasMore:    hasMore,
	}, nil
}

func (s *InProcessService) GetFacts(ctx context.Context, scope Scope) (*FactSet, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	state, err := s.cfg.Store.GetSourceState(ctx, scope.TenantID, scope.Channel, scope.SourceKey)
	if err != nil {
		return nil, err
	}
	facts := make(map[string]string, len(state.Facts))
	for k, v := range state.Facts {
		facts[k] = v
	}
	return &FactSet{
		Version:       ContractVersion,
		Scope:         scope,
		SourceVersion: state.Version,
		Facts:         facts,
		UpdatedAt:     state.Meta.UpdatedAt,
	}, nil
}

func (s *InProcessService) ApplyFacts(ctx context.Context, req ApplyFactsRequest) (*ApplyFactsResult, error) {
	if err := req.Scope.Validate(); err != nil {
		return nil, err
	}
	state, err := s.cfg.Store.GetSourceState(ctx, req.Scope.TenantID, req.Scope.Channel, req.Scope.SourceKey)
	if err != nil {
		return nil, err
	}
	if state.Version != req.ExpectedVersion {
		return nil, fmt.Errorf("%w: esperado %d, atual %d", ErrVersionConflict, req.ExpectedVersion, state.Version)
	}

	mergedFacts, diffs := mergeFacts(state.Facts, req.Facts, req.ClearedFacts)
	now := s.clock.Now().UTC()

	summary := state.Summary
	summary.TenantID = req.Scope.TenantID
	summary.Channel = req.Scope.Channel
	summary.SourceKey = req.Scope.SourceKey
	if req.Summary != nil {
		summary.Text = *req.Summary
		summary.TurnCount = state.Summary.TurnCount + 1
		summary.UpdatedAt = now
	}

	signals := state.Signals
	if req.Signals != nil {
		signals = make(map[string]bool, len(req.Signals))
		for k, v := range req.Signals {
			signals[k] = v
		}
	}

	convCtx := state.Context
	if req.StructuredContext != nil {
		convCtx = fromStructuredContextView(*req.StructuredContext, now)
	}

	var run core.Run
	if req.Run != nil {
		run = core.Run{
			ID:               req.Run.ID,
			TenantID:         req.Scope.TenantID,
			Channel:          req.Scope.Channel,
			SourceKey:        req.Scope.SourceKey,
			MessageID:        req.Run.MessageID,
			Stage:            req.Run.Stage,
			Status:           req.Run.Status,
			Model:            req.Run.Model,
			PromptTokens:     req.Run.PromptTokens,
			CompletionTokens: req.Run.CompletionTokens,
			DurationMs:       req.Run.DurationMs,
			TraceBlobRef:     req.Run.TraceBlobRef,
			TTL:              req.Run.TTL,
			CreatedAt:        req.Run.CreatedAt,
		}
	}

	commitErr := s.cfg.Store.CommitAnalysis(ctx, core.CommitAnalysisInput{
		TenantID:        req.Scope.TenantID,
		Channel:         req.Scope.Channel,
		SourceKey:       req.Scope.SourceKey,
		ExpectedVersion: req.ExpectedVersion,
		Summary:         summary,
		Facts:           mergedFacts,
		Signals:         signals,
		Context:         convCtx,
		Run:             run,
		UpdatedAt:       now,
	})
	if commitErr != nil {
		if errors.Is(commitErr, core.ErrVersionConflict) {
			return nil, fmt.Errorf("%w: %v", ErrVersionConflict, commitErr)
		}
		return nil, commitErr
	}

	return &ApplyFactsResult{
		Version:       ContractVersion,
		Scope:         req.Scope,
		SourceVersion: req.ExpectedVersion + 1,
		Facts:         mergedFacts,
		Diff:          diffs,
		UpdatedAt:     now,
	}, nil
}

func (s *InProcessService) GetSummary(ctx context.Context, scope Scope) (*SummaryView, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	state, err := s.cfg.Store.GetSourceState(ctx, scope.TenantID, scope.Channel, scope.SourceKey)
	if err != nil {
		return nil, err
	}
	return &SummaryView{
		Version:   ContractVersion,
		Scope:     scope,
		Text:      state.Summary.Text,
		TurnCount: state.Summary.TurnCount,
		UpdatedAt: state.Summary.UpdatedAt,
	}, nil
}

func (s *InProcessService) GetSignals(ctx context.Context, scope Scope) (*SignalsView, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	state, err := s.cfg.Store.GetSourceState(ctx, scope.TenantID, scope.Channel, scope.SourceKey)
	if err != nil {
		return nil, err
	}
	sigs := make(map[string]bool, len(state.Signals))
	for k, v := range state.Signals {
		sigs[k] = v
	}
	return &SignalsView{
		Version: ContractVersion,
		Scope:   scope,
		Signals: sigs,
	}, nil
}

func (s *InProcessService) GetStructuredContext(ctx context.Context, scope Scope) (*StructuredContextView, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	state, err := s.cfg.Store.GetSourceState(ctx, scope.TenantID, scope.Channel, scope.SourceKey)
	if err != nil {
		return nil, err
	}
	return toStructuredContextView(scope, state.Context), nil
}

func (s *InProcessService) ListKnowledge(ctx context.Context, tenantID string) ([]KnowledgeDocView, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, ErrInvalidScope
	}
	if s.cfg.Knowledge == nil {
		return nil, nil
	}
	docs, qa, err := s.cfg.Knowledge.Load(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	out := append([]KnowledgeDocView(nil), docs...)
	if strings.TrimSpace(qa) != "" {
		out = append(out, KnowledgeDocView{Name: "QA.md", Content: qa})
	}
	return out, nil
}

func (s *InProcessService) GetKnowledgeDoc(ctx context.Context, tenantID, filename string) (*KnowledgeDocView, error) {
	docs, err := s.ListKnowledge(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	target := strings.TrimSpace(filename)
	for _, d := range docs {
		if strings.EqualFold(d.Name, target) {
			copyDoc := d
			return &copyDoc, nil
		}
	}
	return nil, fmt.Errorf("%w: documento %q", ErrNotFound, filename)
}

func (s *InProcessService) SearchKnowledge(ctx context.Context, tenantID, query string, limit int) (*KnowledgeSearchResult, error) {
	docs, err := s.ListKnowledge(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	return s.searcher.Search(ctx, docs, query, limit)
}

func (s *InProcessService) ListLearningProposals(ctx context.Context, tenantID string) ([]LearningProposalView, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, ErrInvalidScope
	}
	if s.cfg.Learning == nil {
		return []LearningProposalView{}, nil
	}
	return s.cfg.Learning.ListPending(ctx, tenantID)
}

func (s *InProcessService) ApproveProposal(ctx context.Context, tenantID, proposalID, actor, overrideAnswer string) (*LearningProposalView, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, ErrInvalidScope
	}
	if s.cfg.Learning == nil {
		return nil, ErrApprovalRequired
	}
	return s.cfg.Learning.Approve(ctx, tenantID, proposalID, actor, overrideAnswer)
}

func (s *InProcessService) RejectProposal(ctx context.Context, tenantID, proposalID, actor, reason string) (*LearningProposalView, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, ErrInvalidScope
	}
	if s.cfg.Learning == nil {
		return nil, ErrApprovalRequired
	}
	return s.cfg.Learning.Reject(ctx, tenantID, proposalID, actor, reason)
}

func (s *InProcessService) ListRuns(ctx context.Context, scope Scope, limit int) ([]RunView, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	runs, err := s.cfg.Store.ListRuns(ctx, scope.TenantID, scope.Channel, scope.SourceKey, limit)
	if err != nil {
		return nil, err
	}
	out := make([]RunView, len(runs))
	for i, r := range runs {
		out[i] = RunView{
			ID:               r.ID,
			MessageID:        r.MessageID,
			Stage:            r.Stage,
			Status:           r.Status,
			Model:            r.Model,
			PromptTokens:     r.PromptTokens,
			CompletionTokens: r.CompletionTokens,
			DurationMs:       r.DurationMs,
			TraceBlobRef:     r.TraceBlobRef,
			CreatedAt:        r.CreatedAt,
		}
	}
	return out, nil
}

func (s *InProcessService) GetRun(ctx context.Context, scope Scope, runID string) (*RunTraceView, error) {
	runs, err := s.ListRuns(ctx, scope, 50)
	if err != nil {
		return nil, err
	}
	for _, r := range runs {
		if r.ID == runID {
			var rawTrace []byte
			if r.TraceBlobRef != "" && s.cfg.Blobs != nil {
				if data, err := s.cfg.Blobs.Get(ctx, r.TraceBlobRef); err == nil {
					rawTrace = data
				}
			}
			return &RunTraceView{
				Version:  ContractVersion,
				Scope:    scope,
				Run:      r,
				RawTrace: rawTrace,
			}, nil
		}
	}
	return nil, fmt.Errorf("%w: run %q", ErrNotFound, runID)
}

func mergeFacts(existing, incoming map[string]string, cleared []string) (map[string]string, []FactDiff) {
	merged := make(map[string]string, len(existing)+len(incoming))
	for k, v := range existing {
		merged[k] = v
	}
	var diffs []FactDiff
	for _, key := range cleared {
		if oldVal, ok := merged[key]; ok {
			delete(merged, key)
			diffs = append(diffs, FactDiff{Key: key, Action: "removed", OldValue: oldVal})
		}
	}
	for k, newVal := range incoming {
		oldVal, existed := merged[k]
		if !existed {
			merged[k] = newVal
			diffs = append(diffs, FactDiff{Key: k, Action: "added", NewValue: newVal})
			continue
		}
		if oldVal != newVal {
			merged[k] = newVal
			diffs = append(diffs, FactDiff{Key: k, Action: "updated", OldValue: oldVal, NewValue: newVal})
		}
	}
	sort.Slice(diffs, func(i, j int) bool { return diffs[i].Key < diffs[j].Key })
	return merged, diffs
}

func fromStructuredContextView(v StructuredContextView, updatedAt time.Time) core.ConversationContext {
	sections := make(map[string]map[string]string, len(v.Sections))
	for sk, fields := range v.Sections {
		fc := make(map[string]string, len(fields))
		for fk, fv := range fields {
			fc[fk] = fv
		}
		sections[sk] = fc
	}
	details := make(map[string]string, len(v.Details))
	for k, val := range v.Details {
		details[k] = val
	}
	return core.ConversationContext{
		LastActionType: v.LastActionType,
		LastActionBy:   v.LastActionBy,
		Description:    v.Description,
		Details:        details,
		Sections:       sections,
		UpdatedAt:      updatedAt,
	}
}

func encodeCursor(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

func decodeCursor(cursor string) int {
	if strings.TrimSpace(cursor) == "" {
		return 0
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(cursor))
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(string(raw))
	if err != nil || n < 0 {
		return 0
	}
	return n
}
