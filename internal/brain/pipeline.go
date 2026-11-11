package brain

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"whatshelper/internal/core"
	"whatshelper/internal/pack"
	"whatshelper/internal/store"
)

// RunTrace armazena no BlobStore todos os detalhes de pensamento do bot para o visualizador.
type RunTrace struct {
	RunID           string               `json:"run_id"`
	TenantID        string               `json:"tenant_id"`
	Channel         string               `json:"channel"`
	SourceKey       string               `json:"source_key"`
	MessageID       string               `json:"message_id"`
	PackName        string               `json:"pack_name"`
	CreatedAt       time.Time            `json:"created_at"`
	Analysis        *StageTrace          `json:"analysis,omitempty"`
	ParsedResult    *pack.AnalysisResult `json:"parsed_result,omitempty"`
	FactsDiff       []FactChange         `json:"facts_diff,omitempty"`
	Reply           *StageTrace          `json:"reply,omitempty"`
	FinalReply      string               `json:"final_reply,omitempty"`
	TotalTokens     int                  `json:"total_tokens"`
	TotalDurationMs int64                `json:"total_duration_ms"`
}

// StageTrace detalha a execução de um estágio (análise ou resposta).
type StageTrace struct {
	Stage            string                 `json:"stage"`
	Model            string                 `json:"model"`
	SystemPrompt     string                 `json:"system_prompt"`
	UserPrompt       string                 `json:"user_prompt"`
	RawOutput        string                 `json:"raw_output"`
	RepairPrompt     string                 `json:"repair_prompt,omitempty"`
	RepairRawOutput  string                 `json:"repair_raw_output,omitempty"`
	PromptTokens     int                    `json:"prompt_tokens"`
	CompletionTokens int                    `json:"completion_tokens"`
	DurationMs       int64                  `json:"duration_ms"`
	Status           string                 `json:"status"`
	Errors           []pack.ValidationError `json:"errors,omitempty"`
}

// PipelineConfig configura o orquestrador do cérebro.
type PipelineConfig struct {
	Store               core.Store
	Blobs               core.BlobStore
	LLM                 core.LLM
	Bus                 core.EventBus
	Clock               core.Clock
	Tenants             TenantConfigLoader
	Knowledge           KnowledgeSource
	PackLoader          func(packName string) (*pack.Pack, error)
	AnalysisModel       string
	ResponseModel       string
	Debounce            time.Duration
	AfterFunc           AfterFunc
	RecentMessagesLimit int
	Logger              *slog.Logger
}

// Pipeline orquestra ingestão, dedupe, debounce, análise, resposta e persistência.
type Pipeline struct {
	cfg        PipelineConfig
	dispatcher *SourceDispatcher
	channelsMu sync.RWMutex
	channels   map[string]core.Channel
	logger     *slog.Logger
}

// NewPipeline cria e inicializa um Pipeline pronto para receber mensagens.
func NewPipeline(ctx context.Context, cfg PipelineConfig) *Pipeline {
	if cfg.Clock == nil {
		cfg.Clock = core.SystemClock{}
	}
	if cfg.RecentMessagesLimit <= 0 {
		cfg.RecentMessagesLimit = 20
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	p := &Pipeline{
		cfg:      cfg,
		channels: make(map[string]core.Channel),
		logger:   logger,
	}
	p.dispatcher = NewSourceDispatcher(ctx, cfg.Debounce, cfg.AfterFunc, func(batchCtx context.Context, batch []core.NormalizedMessage) {
		if err := p.ProcessBatch(batchCtx, batch); err != nil {
			p.logger.Error("falha no processamento do lote",
				slog.String("tenant", batch[0].TenantID),
				slog.String("channel", batch[0].Channel),
				slog.String("source_key", batch[0].SourceKey),
				slog.Any("error", err),
			)
		}
	})
	return p
}

// RegisterChannel associa um canal pelo seu ID para despacho de respostas.
func (p *Pipeline) RegisterChannel(ch core.Channel) {
	p.channelsMu.Lock()
	defer p.channelsMu.Unlock()
	p.channels[ch.ID()] = ch
}

// Stop encerra o dispatcher aguardando lotes em execução.
func (p *Pipeline) Stop() {
	p.dispatcher.Stop()
}

// Ingest valida na fronteira, salva payload cru no BlobStore, deduplica no Store e enfileira no debounce.
func (p *Pipeline) Ingest(ctx context.Context, msg core.NormalizedMessage) error {
	if err := msg.Validate(); err != nil {
		return fmt.Errorf("mensagem inválida: %w", err)
	}

	rawRef := msg.RawRef
	if rawRef == "" {
		rawRef = store.S3RawMessageKey(msg.TenantID, msg.Channel, msg.SourceKey, msg.Timestamp, msg.MessageID)
		msg.RawRef = rawRef
	}

	rawBytes, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("erro ao serializar mensagem crua: %w", err)
	}
	if err := p.cfg.Blobs.Put(ctx, rawRef, rawBytes, "application/json"); err != nil {
		return fmt.Errorf("erro ao gravar mensagem crua no BlobStore: %w", err)
	}

	sender := p.resolveSender(ctx, msg)
	meta := core.MessageMeta{
		TenantID:  msg.TenantID,
		Channel:   msg.Channel,
		SourceKey: msg.SourceKey,
		MessageID: msg.MessageID,
		Timestamp: msg.Timestamp,
		Direction: msg.Direction,
		Type:      msg.Type,
		MediaRef:  msg.MediaRef,
		RawRef:    rawRef,
		Sender:    sender,
	}

	if err := p.cfg.Store.PutMessageMeta(ctx, meta); err != nil {
		if errors.Is(err, core.ErrDuplicateMessage) {
			p.publish(core.Event{
				ID:        newID("ev"),
				Type:      core.EventMessageDuplicate,
				TenantID:  msg.TenantID,
				Channel:   msg.Channel,
				SourceKey: msg.SourceKey,
				MessageID: msg.MessageID,
				At:        p.now(),
			})
			return nil
		}
		return fmt.Errorf("erro ao persistir metadados da mensagem: %w", err)
	}

	p.publish(core.Event{
		ID:        newID("ev"),
		Type:      core.EventMessageReceived,
		TenantID:  msg.TenantID,
		Channel:   msg.Channel,
		SourceKey: msg.SourceKey,
		MessageID: msg.MessageID,
		At:        p.now(),
		Payload: map[string]any{
			"text":      msg.Text,
			"direction": string(msg.Direction),
			"raw_ref":   rawRef,
		},
	})

	if msg.Direction == core.DirectionInbound {
		p.dispatcher.Enqueue(msg)
	}
	return nil
}

// ProcessBatch executa análise, commit otimista e geração de resposta para um lote de mensagens da mesma fonte.
func (p *Pipeline) ProcessBatch(ctx context.Context, batch []core.NormalizedMessage) error {
	if len(batch) == 0 {
		return nil
	}

	latest := batch[len(batch)-1]
	tenantID, channelID, sourceKey := latest.TenantID, latest.Channel, latest.SourceKey
	runID := newID("run")
	traceRef := store.S3TraceKey(tenantID, channelID, sourceKey, runID)

	tenantCfg, err := p.cfg.Tenants.LoadTenant(ctx, tenantID)
	if err != nil {
		return p.failPipeline(ctx, latest, runID, traceRef, nil, err)
	}

	pk, err := p.cfg.PackLoader(tenantCfg.Pack)
	if err != nil {
		return p.failPipeline(ctx, latest, runID, traceRef, nil, err)
	}

	knowledge, err := p.cfg.Knowledge.Load(ctx, tenantID)
	if err != nil {
		return p.failPipeline(ctx, latest, runID, traceRef, nil, err)
	}

	trace := &RunTrace{
		RunID:     runID,
		TenantID:  tenantID,
		Channel:   channelID,
		SourceKey: sourceKey,
		MessageID: latest.MessageID,
		PackName:  pk.Name,
		CreatedAt: p.now(),
	}

	ch := p.getChannel(channelID)
	plainTextOnly := true
	if ch != nil {
		plainTextOnly = ch.Capabilities().PlainTextOnly
	}

	batchIDs := make(map[string]bool, len(batch))
	for _, m := range batch {
		batchIDs[m.MessageID] = true
	}

	// Retry de leitura+análise+commit se houver ErrVersionConflict (máximo 2 tentativas)
	var updatedState *core.SourceState
	var diffs []FactChange
	var analysisOutcome *StructuredAnalysisOutcome

	const maxVersionRetries = 2
	for attempt := 0; attempt < maxVersionRetries; attempt++ {
		state, err := p.cfg.Store.GetSourceState(ctx, tenantID, channelID, sourceKey)
		if err != nil {
			return p.failPipeline(ctx, latest, runID, traceRef, trace, err)
		}

		recentMsgs, err := p.loadRecentPromptMessages(ctx, pk, tenantID, channelID, sourceKey, batchIDs)
		if err != nil {
			return p.failPipeline(ctx, latest, runID, traceRef, trace, err)
		}

		combinedNewText := joinBatchTexts(batch)
		newPromptMsg := pack.PromptMessage{
			Sender:    pk.Actors.Customer,
			Text:      combinedNewText,
			Timestamp: latest.Timestamp.UTC().Format(time.RFC3339),
		}

		promptInput := pack.PromptInput{
			Pack:           pk,
			Summary:        state.Summary.Text,
			Facts:          state.Facts,
			Signals:        state.Signals,
			Context:        state.Context.Sections,
			RecentMessages: recentMsgs,
			NewMessage:     newPromptMsg,
		}

		renderedAnalysis, err := pack.RenderAnalysis(pk, promptInput)
		if err != nil {
			return p.failPipeline(ctx, latest, runID, traceRef, trace, err)
		}

		p.publish(core.Event{
			ID:        newID("ev"),
			Type:      core.EventAnalysisStarted,
			TenantID:  tenantID,
			Channel:   channelID,
			SourceKey: sourceKey,
			MessageID: latest.MessageID,
			RunID:     runID,
			At:        p.now(),
		})

		p.publish(core.Event{
			ID:        newID("ev"),
			Type:      core.EventPromptSent,
			TenantID:  tenantID,
			Channel:   channelID,
			SourceKey: sourceKey,
			MessageID: latest.MessageID,
			RunID:     runID,
			At:        p.now(),
			Payload: map[string]any{
				"stage":  "analysis",
				"model":  p.cfg.AnalysisModel,
				"system": renderedAnalysis.System,
				"prompt": renderedAnalysis.User,
			},
		})

		analysisOutcome, err = GenerateStructuredAnalysis(ctx, p.cfg.LLM, p.cfg.AnalysisModel, pk, renderedAnalysis)
		trace.Analysis = &StageTrace{
			Stage:            "analysis",
			Model:            p.cfg.AnalysisModel,
			SystemPrompt:     renderedAnalysis.System,
			UserPrompt:       renderedAnalysis.User,
			RawOutput:        analysisOutcome.RawOutput,
			RepairPrompt:     analysisOutcome.RepairPrompt,
			RepairRawOutput:  analysisOutcome.RepairRawOutput,
			PromptTokens:     analysisOutcome.PromptTokens,
			CompletionTokens: analysisOutcome.CompletionTokens,
			DurationMs:       analysisOutcome.DurationMs,
			Status:           analysisOutcome.Status,
			Errors:           analysisOutcome.FinalErrors,
		}

		p.publish(core.Event{
			ID:        newID("ev"),
			Type:      core.EventLLMResponded,
			TenantID:  tenantID,
			Channel:   channelID,
			SourceKey: sourceKey,
			MessageID: latest.MessageID,
			RunID:     runID,
			At:        p.now(),
			Payload: map[string]any{
				"stage":             "analysis",
				"status":            analysisOutcome.Status,
				"raw_output":        analysisOutcome.RawOutput,
				"repair_raw_output": analysisOutcome.RepairRawOutput,
				"prompt_tokens":     analysisOutcome.PromptTokens,
				"completion_tokens": analysisOutcome.CompletionTokens,
				"duration_ms":       analysisOutcome.DurationMs,
			},
		})

		if err != nil {
			// Fallback seguro: salva trace + RUN failed e não altera estado nem responde
			return p.failPipeline(ctx, latest, runID, traceRef, trace, err)
		}

		mergedFacts, computedDiffs := MergeAndDiffFacts(state.Facts, analysisOutcome.Result.Facts, analysisOutcome.Result.ClearedFacts)
		diffs = computedDiffs
		trace.ParsedResult = analysisOutcome.Result
		trace.FactsDiff = diffs

		now := p.now()
		newSummary := core.Summary{
			TenantID:  tenantID,
			Channel:   channelID,
			SourceKey: sourceKey,
			Text:      analysisOutcome.Result.Summary,
			TurnCount: state.Summary.TurnCount + 1,
			UpdatedAt: now,
		}
		newContext := BuildConversationContext(analysisOutcome.Result.Context, now)

		runRecord := core.Run{
			ID:               runID,
			TenantID:         tenantID,
			Channel:          channelID,
			SourceKey:        sourceKey,
			MessageID:        latest.MessageID,
			Stage:            "pipeline",
			Status:           analysisOutcome.Status,
			Model:            p.cfg.AnalysisModel,
			PromptTokens:     analysisOutcome.PromptTokens,
			CompletionTokens: analysisOutcome.CompletionTokens,
			DurationMs:       analysisOutcome.DurationMs,
			TraceBlobRef:     traceRef,
			CreatedAt:        trace.CreatedAt,
		}

		commitErr := p.cfg.Store.CommitAnalysis(ctx, core.CommitAnalysisInput{
			TenantID:        tenantID,
			Channel:         channelID,
			SourceKey:       sourceKey,
			ExpectedVersion: state.Version,
			Summary:         newSummary,
			Facts:           mergedFacts,
			Signals:         analysisOutcome.Result.Signals,
			Context:         newContext,
			Run:             runRecord,
			UpdatedAt:       now,
		})
		if commitErr != nil {
			if errors.Is(commitErr, core.ErrVersionConflict) && attempt+1 < maxVersionRetries {
				continue
			}
			return p.failPipeline(ctx, latest, runID, traceRef, trace, commitErr)
		}

		updatedState = &core.SourceState{
			Version: state.Version + 1,
			Meta:    state.Meta,
			Summary: newSummary,
			Facts:   mergedFacts,
			Signals: analysisOutcome.Result.Signals,
			Context: newContext,
		}
		break
	}

	p.publish(core.Event{
		ID:        newID("ev"),
		Type:      core.EventFactsDiff,
		TenantID:  tenantID,
		Channel:   channelID,
		SourceKey: sourceKey,
		MessageID: latest.MessageID,
		RunID:     runID,
		At:        p.now(),
		Payload: map[string]any{
			"diff":  diffs,
			"facts": updatedState.Facts,
		},
	})

	p.publish(core.Event{
		ID:        newID("ev"),
		Type:      core.EventAnalysisCompleted,
		TenantID:  tenantID,
		Channel:   channelID,
		SourceKey: sourceKey,
		MessageID: latest.MessageID,
		RunID:     runID,
		At:        p.now(),
		Payload: map[string]any{
			"summary": updatedState.Summary.Text,
			"signals": updatedState.Signals,
			"context": updatedState.Context,
			"parsed":  analysisOutcome.Result,
		},
	})

	// Estágio de resposta (BRAIN-03)
	recentForReply, err := p.loadRecentPromptMessages(ctx, pk, tenantID, channelID, sourceKey, batchIDs)
	if err != nil {
		return p.failPipeline(ctx, latest, runID, traceRef, trace, err)
	}

	replyInput := pack.PromptInput{
		Pack:           pk,
		Summary:        updatedState.Summary.Text,
		Facts:          updatedState.Facts,
		Signals:        updatedState.Signals,
		Context:        updatedState.Context.Sections,
		RecentMessages: recentForReply,
		NewMessage: pack.PromptMessage{
			Sender:    pk.Actors.Customer,
			Text:      joinBatchTexts(batch),
			Timestamp: latest.Timestamp.UTC().Format(time.RFC3339),
		},
		Knowledge:     knowledge.Docs,
		QAContent:     knowledge.QAContent,
		PlainTextOnly: plainTextOnly,
	}

	renderedReply, err := pack.RenderReply(pk, replyInput)
	if err != nil {
		return p.failPipeline(ctx, latest, runID, traceRef, trace, err)
	}

	p.publish(core.Event{
		ID:        newID("ev"),
		Type:      core.EventPromptSent,
		TenantID:  tenantID,
		Channel:   channelID,
		SourceKey: sourceKey,
		MessageID: latest.MessageID,
		RunID:     runID,
		At:        p.now(),
		Payload: map[string]any{
			"stage":  "response",
			"model":  p.cfg.ResponseModel,
			"system": renderedReply.System,
			"prompt": renderedReply.User,
		},
	})

	replyResp, err := p.cfg.LLM.Generate(ctx, core.LLMRequest{
		Stage:       "response",
		Model:       p.cfg.ResponseModel,
		System:      renderedReply.System,
		Prompt:      renderedReply.User,
		Temperature: 0.3,
		MaxTokens:   1024,
	})
	if err != nil {
		return p.failPipeline(ctx, latest, runID, traceRef, trace, err)
	}

	finalReply := strings.TrimSpace(replyResp.Content)
	if plainTextOnly {
		finalReply = SanitizePlainText(finalReply)
	}

	trace.Reply = &StageTrace{
		Stage:            "response",
		Model:            p.cfg.ResponseModel,
		SystemPrompt:     renderedReply.System,
		UserPrompt:       renderedReply.User,
		RawOutput:        replyResp.Content,
		PromptTokens:     replyResp.PromptTokens,
		CompletionTokens: replyResp.CompletionTokens,
		DurationMs:       replyResp.DurationMs,
		Status:           "ok",
	}
	trace.FinalReply = finalReply
	trace.TotalTokens = trace.Analysis.PromptTokens + trace.Analysis.CompletionTokens + replyResp.PromptTokens + replyResp.CompletionTokens
	trace.TotalDurationMs = trace.Analysis.DurationMs + replyResp.DurationMs

	p.publish(core.Event{
		ID:        newID("ev"),
		Type:      core.EventLLMResponded,
		TenantID:  tenantID,
		Channel:   channelID,
		SourceKey: sourceKey,
		MessageID: latest.MessageID,
		RunID:     runID,
		At:        p.now(),
		Payload: map[string]any{
			"stage":             "response",
			"raw_output":        replyResp.Content,
			"prompt_tokens":     replyResp.PromptTokens,
			"completion_tokens": replyResp.CompletionTokens,
			"duration_ms":       replyResp.DurationMs,
		},
	})

	// Persiste trace completo no BlobStore e Run final atualizado
	if err := p.saveTrace(ctx, traceRef, trace); err != nil {
		return err
	}
	_ = p.cfg.Store.SaveRun(ctx, core.Run{
		ID:               runID,
		TenantID:         tenantID,
		Channel:          channelID,
		SourceKey:        sourceKey,
		MessageID:        latest.MessageID,
		Stage:            "pipeline",
		Status:           analysisOutcome.Status,
		Model:            p.cfg.ResponseModel,
		PromptTokens:     trace.Analysis.PromptTokens + replyResp.PromptTokens,
		CompletionTokens: trace.Analysis.CompletionTokens + replyResp.CompletionTokens,
		DurationMs:       trace.TotalDurationMs,
		TraceBlobRef:     traceRef,
		CreatedAt:        trace.CreatedAt,
	})

	p.publish(core.Event{
		ID:        newID("ev"),
		Type:      core.EventReplyGenerated,
		TenantID:  tenantID,
		Channel:   channelID,
		SourceKey: sourceKey,
		MessageID: latest.MessageID,
		RunID:     runID,
		At:        p.now(),
		Payload: map[string]any{
			"text":            finalReply,
			"trace_ref":       traceRef,
			"total_tokens":    trace.TotalTokens,
			"total_duration":  trace.TotalDurationMs,
		},
	})

	// Persiste e envia resposta outbound
	outMsg := core.NormalizedMessage{
		TenantID:  tenantID,
		Channel:   channelID,
		Account:   latest.Account,
		SourceKey: sourceKey,
		MessageID: newID("out"),
		Timestamp: p.now(),
		Direction: core.DirectionOutbound,
		Type:      core.MessageTypeText,
		Text:      finalReply,
	}
	outMsg.RawRef = store.S3RawMessageKey(tenantID, channelID, sourceKey, outMsg.Timestamp, outMsg.MessageID)

	outBytes, _ := json.Marshal(outMsg)
	if err := p.cfg.Blobs.Put(ctx, outMsg.RawRef, outBytes, "application/json"); err != nil {
		return fmt.Errorf("erro ao gravar resposta crua no BlobStore: %w", err)
	}
	if err := p.cfg.Store.PutMessageMeta(ctx, core.MessageMeta{
		TenantID:  tenantID,
		Channel:   channelID,
		SourceKey: sourceKey,
		MessageID: outMsg.MessageID,
		Timestamp: outMsg.Timestamp,
		Direction: core.DirectionOutbound,
		Type:      core.MessageTypeText,
		RawRef:    outMsg.RawRef,
		Sender:    pk.Actors.Business,
	}); err != nil {
		return fmt.Errorf("erro ao gravar MSG# outbound: %w", err)
	}

	if ch != nil {
		if err := ch.Send(ctx, outMsg); err != nil {
			return fmt.Errorf("erro ao enviar resposta no canal %s: %w", channelID, err)
		}
	}

	p.publish(core.Event{
		ID:        newID("ev"),
		Type:      core.EventReplySent,
		TenantID:  tenantID,
		Channel:   channelID,
		SourceKey: sourceKey,
		MessageID: outMsg.MessageID,
		RunID:     runID,
		At:        p.now(),
		Payload: map[string]any{
			"text":      finalReply,
			"trace_ref": traceRef,
		},
	})

	return nil
}

func (p *Pipeline) loadRecentPromptMessages(
	ctx context.Context,
	pk *pack.Pack,
	tenantID, channelID, sourceKey string,
	excludeIDs map[string]bool,
) ([]pack.PromptMessage, error) {
	metas, err := p.cfg.Store.ListRecentMessages(ctx, tenantID, channelID, sourceKey, p.cfg.RecentMessagesLimit)
	if err != nil {
		return nil, err
	}

	// ListRecentMessages retorna do mais novo para o mais antigo; invertemos para ordem cronológica
	result := make([]pack.PromptMessage, 0, len(metas))
	for i := len(metas) - 1; i >= 0; i-- {
		meta := metas[i]
		if excludeIDs[meta.MessageID] {
			continue
		}
		text := ""
		if meta.RawRef != "" {
			raw, err := p.cfg.Blobs.Get(ctx, meta.RawRef)
			if err == nil {
				var norm core.NormalizedMessage
				if json.Unmarshal(raw, &norm) == nil {
					text = norm.Text
				}
			}
		}
		if text == "" {
			continue
		}
		sender := pk.Actors.Customer
		if meta.Direction == core.DirectionOutbound {
			sender = pk.Actors.Business
		}
		result = append(result, pack.PromptMessage{
			Sender:    sender,
			Text:      text,
			Timestamp: meta.Timestamp.UTC().Format(time.RFC3339),
		})
	}
	return result, nil
}

func (p *Pipeline) resolveSender(ctx context.Context, msg core.NormalizedMessage) string {
	if p.cfg.Tenants != nil && p.cfg.PackLoader != nil {
		if tCfg, err := p.cfg.Tenants.LoadTenant(ctx, msg.TenantID); err == nil {
			if pk, err := p.cfg.PackLoader(tCfg.Pack); err == nil {
				if msg.Direction == core.DirectionOutbound {
					return pk.Actors.Business
				}
				return pk.Actors.Customer
			}
		}
	}
	return string(msg.Direction)
}

func (p *Pipeline) failPipeline(ctx context.Context, msg core.NormalizedMessage, runID, traceRef string, trace *RunTrace, err error) error {
	now := p.now()
	runCreatedAt := now
	stage := "analysis"
	model := p.cfg.AnalysisModel
	if trace != nil {
		if !trace.CreatedAt.IsZero() {
			runCreatedAt = trace.CreatedAt
		}
		if trace.ParsedResult != nil {
			stage = "response"
			model = p.cfg.ResponseModel
		}
		_ = p.saveTrace(ctx, traceRef, trace)
	}
	_ = p.cfg.Store.SaveRun(ctx, core.Run{
		ID:           runID,
		TenantID:     msg.TenantID,
		Channel:      msg.Channel,
		SourceKey:    msg.SourceKey,
		MessageID:    msg.MessageID,
		Stage:        stage,
		Status:       "failed",
		Model:        model,
		TraceBlobRef: traceRef,
		CreatedAt:    runCreatedAt,
	})
	p.publish(core.Event{
		ID:        newID("ev"),
		Type:      core.EventPipelineFailed,
		TenantID:  msg.TenantID,
		Channel:   msg.Channel,
		SourceKey: msg.SourceKey,
		MessageID: msg.MessageID,
		RunID:     runID,
		At:        now,
		Payload: map[string]any{
			"error":     err.Error(),
			"trace_ref": traceRef,
		},
	})
	return err
}

func (p *Pipeline) saveTrace(ctx context.Context, traceRef string, trace *RunTrace) error {
	data, err := json.MarshalIndent(trace, "", "  ")
	if err != nil {
		return err
	}
	return p.cfg.Blobs.Put(ctx, traceRef, data, "application/json")
}

func (p *Pipeline) getChannel(channelID string) core.Channel {
	p.channelsMu.RLock()
	defer p.channelsMu.RUnlock()
	return p.channels[channelID]
}

func (p *Pipeline) publish(ev core.Event) {
	if p.cfg.Bus != nil {
		p.cfg.Bus.Publish(ev)
	}
}

func (p *Pipeline) now() time.Time {
	return p.cfg.Clock.Now().UTC()
}

func joinBatchTexts(batch []core.NormalizedMessage) string {
	if len(batch) == 1 {
		return batch[0].Text
	}
	parts := make([]string, 0, len(batch))
	for _, m := range batch {
		if t := strings.TrimSpace(m.Text); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "\n")
}

func newID(prefix string) string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s_%d_%s", prefix, time.Now().UTC().UnixNano(), hex.EncodeToString(b[:]))
}
