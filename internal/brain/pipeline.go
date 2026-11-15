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

	"whatshelper/internal/contextmod"
	"whatshelper/internal/core"
	"whatshelper/internal/pack"
	"whatshelper/internal/security"
	"whatshelper/internal/store"
	"whatshelper/internal/store/sessioncache"
)

// RunTrace armazena no BlobStore todos os detalhes de pensamento do bot para o visualizador.
type RunTrace struct {
	RunID              string                       `json:"run_id"`
	TenantID           string                       `json:"tenant_id"`
	Channel            string                       `json:"channel"`
	SourceKey          string                       `json:"source_key"`
	MessageID          string                       `json:"message_id"`
	PackName           string                       `json:"pack_name"`
	CreatedAt          time.Time                    `json:"created_at"`
	AnalysisTruncation *contextmod.TruncationReport `json:"analysis_truncation,omitempty"`
	Analysis           *StageTrace                  `json:"analysis,omitempty"`
	ParsedResult       *pack.AnalysisResult         `json:"parsed_result,omitempty"`
	FactsDiff          []FactChange                 `json:"facts_diff,omitempty"`
	FSMOutcome         *TransitionOutcome           `json:"fsm_outcome,omitempty"`
	Decision           *Decision                    `json:"decision,omitempty"`
	ReplyTruncation    *contextmod.TruncationReport `json:"reply_truncation,omitempty"`
	Reply              *StageTrace                  `json:"reply,omitempty"`
	Grounding          *GroundingResult             `json:"grounding,omitempty"`
	FinalReply         string                       `json:"final_reply,omitempty"`
	TotalTokens        int                          `json:"total_tokens"`
	TotalDurationMs    int64                        `json:"total_duration_ms"`
	BatchSize          int                          `json:"batch_size,omitempty"`
	ReprocessCount     int                          `json:"reprocess_count,omitempty"`
	DiscardedReplies   int                          `json:"discarded_replies,omitempty"`
	Window             *contextmod.WindowOutput     `json:"window,omitempty"`
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
	Notifier            core.AdminNotifier
	Clock               core.Clock
	Tenants             TenantConfigLoader
	Knowledge           KnowledgeSource
	PackLoader          func(packName string) (*pack.Pack, error)
	AnalysisModel       string
	ResponseModel       string
	NudgeModel          string
	LearningModel       string
	VisionModel         string
	AudioModel          string
	Debounce            time.Duration
	TypingMaxWait       time.Duration
	AfterFunc           AfterFunc
	RecentMessagesLimit int
	RunRetentionDays    int
	PIICipher           security.PIICipher
	Transcriber         core.AudioTranscriber
	ContextSvc          contextmod.Service
	SessionCache        *sessioncache.Cache
	Logger              *slog.Logger
}

// Pipeline orquestra ingestão, dedupe, debounce, análise, resposta e persistência.
type Pipeline struct {
	cfg          PipelineConfig
	dispatcher   *SourceDispatcher
	limiter      *TokenBucketLimiter
	notifier     core.AdminNotifier
	outboxWorker *OutboxWorker
	sessionCache *sessioncache.Cache
	channelsMu   sync.RWMutex
	channels     map[string]core.Channel
	logger       *slog.Logger
}

// NewPipeline cria e inicializa um Pipeline pronto para receber mensagens.
func NewPipeline(ctx context.Context, cfg PipelineConfig) *Pipeline {
	if cfg.Clock == nil {
		cfg.Clock = core.SystemClock{}
	}
	if cfg.RecentMessagesLimit <= 0 {
		cfg.RecentMessagesLimit = 10
	}
	if cfg.RunRetentionDays <= 0 {
		cfg.RunRetentionDays = 90
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	notifier := cfg.Notifier

	p := &Pipeline{
		cfg:          cfg,
		limiter:      NewTokenBucketLimiter(cfg.Clock),
		notifier:     notifier,
		sessionCache: cfg.SessionCache,
		channels:     make(map[string]core.Channel),
		logger:       logger,
	}
	if p.notifier == nil {
		p.notifier = NewDefaultAdminNotifier(cfg.Store, cfg.Bus, logger).WithAdminDelivery(cfg.Blobs, p.getChannel)
	}
	p.outboxWorker = NewOutboxWorker(cfg.Store, p.notifier, cfg.Bus, p.now)
	if p.cfg.ContextSvc == nil && p.cfg.Store != nil {
		p.cfg.ContextSvc = buildDefaultContextService(p)
	}
	p.dispatcher = NewSourceDispatcherWithConfig(ctx, DispatcherConfig{
		Debounce:       cfg.Debounce,
		TypingMaxWait:  cfg.TypingMaxWait,
		AfterFunc:      cfg.AfterFunc,
		TimingResolver: p.resolveTiming,
		Handler: func(batchCtx context.Context, batch []core.NormalizedMessage) {
			if err := p.ProcessBatch(batchCtx, batch); err != nil {
				p.logger.Error("falha no processamento do lote",
					slog.String("tenant", batch[0].TenantID),
					slog.String("channel", batch[0].Channel),
					slog.String("source_key", batch[0].SourceKey),
					slog.Any("error", err),
				)
			}
		},
	})
	return p
}

// NotifyTyping atualiza o estado de digitação do cliente para a conversa informada.
func (p *Pipeline) NotifyTyping(tenantID, channel, sourceKey string, typing bool) {
	if p == nil || p.dispatcher == nil {
		return
	}
	p.dispatcher.SetTyping(tenantID, channel, sourceKey, typing)
}

func (p *Pipeline) resolveTiming(tenantID string) (debounce, typingMaxWait time.Duration) {
	debounce = p.cfg.Debounce
	typingMaxWait = p.cfg.TypingMaxWait
	if typingMaxWait <= 0 && debounce > 0 {
		typingMaxWait = time.Duration(pack.DefaultTypingMaxWaitMs) * time.Millisecond
	}
	if p.cfg.Tenants != nil && p.cfg.PackLoader != nil {
		if tCfg, err := p.cfg.Tenants.LoadTenant(context.Background(), tenantID); err == nil {
			if pk, err := p.cfg.PackLoader(tCfg.Pack); err == nil && pk != nil && pk.Timing != nil {
				pkDeb, pkWait := pk.EffectiveTiming()
				if pk.Timing.DebounceMs != nil {
					debounce = pkDeb
				}
				if pk.Timing.TypingMaxWaitMs != nil {
					typingMaxWait = pkWait
				}
			}
		}
	}
	return debounce, typingMaxWait
}

// RegisterChannel associa um canal pelo seu ID para despacho de respostas.
func (p *Pipeline) RegisterChannel(ch core.Channel) {
	p.channelsMu.Lock()
	defer p.channelsMu.Unlock()
	p.channels[ch.ID()] = ch
}

// ContextService retorna o módulo de contexto utilizado pelo Pipeline.
func (p *Pipeline) ContextService() contextmod.Service {
	return p.cfg.ContextSvc
}

// OutboxWorker retorna o worker de outbox transacional do Pipeline.
func (p *Pipeline) OutboxWorker() *OutboxWorker {
	return p.outboxWorker
}

// Stop encerra o dispatcher aguardando lotes em execução.
func (p *Pipeline) Stop() {
	p.dispatcher.Stop()
}

// Ingest valida na fronteira, salva payload cru no BlobStore, deduplica no Store e enfileira no debounce.
func (p *Pipeline) Ingest(ctx context.Context, msg core.NormalizedMessage) error {
	_, err := p.IngestWithStatus(ctx, msg)
	return err
}

// IngestWithStatus executa Ingest informando se a mensagem já havia sido processada (dedupe idempotente).
func (p *Pipeline) IngestWithStatus(ctx context.Context, msg core.NormalizedMessage) (duplicate bool, err error) {
	if err := msg.Validate(); err != nil {
		return false, fmt.Errorf("mensagem inválida: %w", err)
	}

	rawRef := msg.RawRef
	if rawRef == "" {
		rawRef = store.S3RawMessageKey(msg.TenantID, msg.Channel, msg.SourceKey, msg.Timestamp, msg.MessageID)
		msg.RawRef = rawRef
	}

	rawBytes, err := json.Marshal(msg)
	if err != nil {
		return false, fmt.Errorf("erro ao serializar mensagem crua: %w", err)
	}
	if err := p.cfg.Blobs.Put(ctx, rawRef, rawBytes, "application/json"); err != nil {
		return false, fmt.Errorf("erro ao gravar mensagem crua no BlobStore: %w", err)
	}

	sender := p.resolveSender(ctx, msg)
	var outboundRec *core.OutboundMessageRecord
	if msg.Direction == core.DirectionInbound && msg.FromMe && strings.TrimSpace(msg.MessageID) != "" && p.cfg.Store != nil {
		outboundRec, _ = p.cfg.Store.GetOutboundRecord(ctx, msg.TenantID, msg.Channel, msg.MessageID)
	}
	reportsOwn := false
	if ch := p.getChannel(msg.Channel); ch != nil {
		reportsOwn = ch.Capabilities().ReportsOwnMessages
	}
	originRes := ResolveMessageOrigin(OriginResolutionInput{
		Direction:          msg.Direction,
		Channel:            msg.Channel,
		FromMe:             msg.FromMe,
		MessageID:          msg.MessageID,
		ReportsOwnMessages: reportsOwn,
		OutboundRecord:     outboundRec,
	})
	msg.Origin = originRes.Origin

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
		Origin:    originRes.Origin,
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
			return true, nil
		}
		return false, fmt.Errorf("erro ao persistir metadados da mensagem: %w", err)
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

	p.publish(core.Event{
		ID:        newID("ev"),
		Type:      core.EventOriginResolved,
		TenantID:  msg.TenantID,
		Channel:   msg.Channel,
		SourceKey: msg.SourceKey,
		MessageID: msg.MessageID,
		At:        p.now(),
		Payload: map[string]any{
			"origin":     string(originRes.Origin),
			"reason":     originRes.Reason,
			"confidence": originRes.Confidence,
			"from_me":    msg.FromMe,
		},
	})

	if msg.Direction == core.DirectionInbound {
		if msg.Channel == AdminChannelID {
			p.publish(core.Event{
				ID:        newID("ev"),
				Type:      core.EventAdminMessage,
				TenantID:  msg.TenantID,
				Channel:   AdminChannelID,
				SourceKey: msg.SourceKey,
				MessageID: msg.MessageID,
				At:        p.now(),
				Payload: map[string]any{
					"direction": string(core.DirectionInbound),
					"text":      msg.Text,
				},
			})
			return false, p.HandleAdminInbound(ctx, msg)
		}

		switch originRes.Origin {
		case core.OriginBot, core.OriginUnknown, core.OriginHumanAdmin:
			return false, nil
		case core.OriginHumanPhone:
			if err := p.handleHumanTakeover(ctx, msg); err != nil {
				return false, err
			}
			return false, nil
		}

		if err := p.ensureEncryptedContactInfo(ctx, msg); err != nil {
			p.logger.Warn("falha ao cifrar contato bruto",
				slog.String("tenant", msg.TenantID),
				slog.String("channel", msg.Channel),
				slog.String("source_key", msg.SourceKey),
				slog.Any("error", err),
			)
		}
		_ = p.CancelPendingNudges(ctx, msg.TenantID, msg.Channel, msg.SourceKey)
		p.dispatcher.Enqueue(msg)
	}
	return false, nil
}

func (p *Pipeline) handleHumanTakeover(ctx context.Context, msg core.NormalizedMessage) error {
	policy := core.TenantPolicy{}
	if p.cfg.Tenants != nil {
		if loaded, err := p.cfg.Tenants.LoadTenant(ctx, msg.TenantID); err == nil {
			policy = loaded
		}
	}
	takeover := policy.Takeover
	mode := takeover.Mode
	if mode == "" {
		mode = core.TakeoverModePause
	}
	if mode == core.TakeoverModeIgnore {
		return nil
	}

	now := p.now()
	payload := map[string]any{
		"origin": string(msg.Origin),
		"mode":   string(mode),
	}
	if mode == core.TakeoverModePause {
		mins := takeover.PauseDurationMinutes
		if mins <= 0 {
			mins = 60
		}
		until := now.Add(time.Duration(mins) * time.Minute)
		payload["paused_until"] = until.Format(time.RFC3339)
		if p.cfg.Store != nil {
			if err := UpdateSourceStatusWithPause(ctx, p.cfg.Store, now, msg.TenantID, msg.Channel, msg.SourceKey, core.SourceStatusHuman, &until); err != nil {
				return fmt.Errorf("erro ao pausar fonte por takeover humano: %w", err)
			}
		}
		p.publishStatusUpdated(msg.TenantID, msg.Channel, msg.SourceKey, core.SourceStatusHuman, "takeover humano pelo aparelho")
	}

	p.publish(core.Event{
		ID:        newID("ev"),
		Type:      core.EventHumanTakeover,
		TenantID:  msg.TenantID,
		Channel:   msg.Channel,
		SourceKey: msg.SourceKey,
		MessageID: msg.MessageID,
		At:        now,
		Payload:   payload,
	})
	return nil
}

// NeutralAudioFallbackReply é enviada ao cliente quando um áudio chega e a transcrição automática não está disponível.
const NeutralAudioFallbackReply = "Recebi seu áudio, mas no momento não consegui transcrevê-lo automaticamente. Já avisei nossa equipe para responder por aqui."

type stageLLMResolver interface {
	Resolve(stage string, overrides map[string]core.StageLLMConfig) core.StageLLMConfig
}

type configAudioTranscriber interface {
	TranscribeWithConfig(ctx context.Context, audio []byte, mimeType, provider, model string) (string, error)
}

func (p *Pipeline) resolveStageLLMConfig(stage string, tenantCfg *core.TenantPolicy, fallbackModel string) core.StageLLMConfig {
	var overrides map[string]core.StageLLMConfig
	if tenantCfg != nil {
		overrides = tenantCfg.LLMOverrides
	}
	if resolver, ok := p.cfg.LLM.(stageLLMResolver); ok {
		resolved := resolver.Resolve(stage, overrides)
		if resolved.Model == "" {
			resolved.Model = fallbackModel
		}
		return resolved
	}
	var out core.StageLLMConfig
	if overrides != nil {
		if ov, ok := overrides[stage]; ok {
			out = ov
		}
	}
	if out.Model == "" {
		out.Model = fallbackModel
	}
	return out
}

func (p *Pipeline) resolveStageLLM(stage string, tenantCfg *core.TenantPolicy, fallbackModel string) (string, string) {
	cfg := p.resolveStageLLMConfig(stage, tenantCfg, fallbackModel)
	return cfg.Provider, cfg.Model
}

func (p *Pipeline) prepareMediaBatch(
	ctx context.Context,
	tenantCfg *core.TenantPolicy,
	businessActor string,
	batch []core.NormalizedMessage,
	runID, traceRef string,
) ([]core.NormalizedMessage, bool, error) {
	out := make([]core.NormalizedMessage, len(batch))
	copy(out, batch)

	for i := range out {
		msg := out[i]
		if strings.TrimSpace(msg.Text) != "" {
			continue
		}
		switch msg.Type {
		case core.MessageTypeAudio:
			transcript, err := p.transcribeAudioMessage(ctx, msg, tenantCfg)
			if err != nil || strings.TrimSpace(transcript) == "" {
				if p.notifier != nil {
					_ = p.notifier.Notify(ctx, core.AdminAlert{
						TenantID:  msg.TenantID,
						Channel:   msg.Channel,
						SourceKey: msg.SourceKey,
						Reason:    "áudio recebido sem transcrição automática",
						Message:   fmt.Sprintf("Áudio em %s (%s)", msg.Channel, msg.MediaRef),
						ContextData: map[string]string{
							"media_ref":  msg.MediaRef,
							"message_id": msg.MessageID,
						},
						CreatedAt: p.now(),
					})
				}
				if _, sendErr := p.DeliverTextOutbound(
					ctx,
					msg.TenantID,
					msg.Channel,
					msg.Account,
					msg.SourceKey,
					businessActor,
					NeutralAudioFallbackReply,
					runID,
					traceRef,
				); sendErr != nil {
					return nil, false, sendErr
				}
				return nil, true, nil
			}
			out[i].Text = "[Áudio transcrito]: " + strings.TrimSpace(transcript)
		case core.MessageTypeImage:
			if desc := p.describeImageMessage(ctx, msg, tenantCfg); desc != "" {
				out[i].Text = "[Imagem descrita]: " + desc
			} else {
				out[i].Text = "[Imagem recebida]"
			}
		case core.MessageTypeDoc:
			out[i].Text = "[Documento recebido]"
		}
	}
	return out, false, nil
}

func (p *Pipeline) describeImageMessage(ctx context.Context, msg core.NormalizedMessage, tenantCfg *core.TenantPolicy) string {
	if p.cfg.LLM == nil || p.cfg.Blobs == nil || strings.TrimSpace(msg.MediaRef) == "" {
		return ""
	}
	fallbackVision := p.cfg.VisionModel
	if fallbackVision == "" {
		fallbackVision = p.cfg.AnalysisModel
	}
	visionCfg := p.resolveStageLLMConfig("vision", tenantCfg, fallbackVision)
	if visionCfg.Model == "" && visionCfg.Provider == "" {
		return ""
	}
	imgBytes, err := p.cfg.Blobs.Get(ctx, msg.MediaRef)
	if err != nil || len(imgBytes) == 0 {
		return ""
	}
	resp, err := p.cfg.LLM.Generate(ctx, core.LLMRequest{
		Stage:           "vision",
		Provider:        visionCfg.Provider,
		Model:           visionCfg.Model,
		ReasoningEffort: visionCfg.ReasoningEffort,
		System:          "Descreva objetivamente o conteúdo relevante da imagem enviada pelo cliente em português claro e conciso.",
		Prompt:      "Descreva esta imagem.",
		MediaParts:  []core.MediaPart{{MimeType: inferImageMime(msg.MediaRef), Data: imgBytes}},
		Temperature: 0.2,
		MaxTokens:   512,
	})
	if err != nil || resp == nil {
		return ""
	}
	return strings.TrimSpace(resp.Content)
}

func inferImageMime(mediaRef string) string {
	lower := strings.ToLower(mediaRef)
	switch {
	case strings.HasSuffix(lower, ".png"):
		return "image/png"
	case strings.HasSuffix(lower, ".webp"):
		return "image/webp"
	case strings.HasSuffix(lower, ".gif"):
		return "image/gif"
	default:
		return "image/jpeg"
	}
}

func (p *Pipeline) transcribeAudioMessage(ctx context.Context, msg core.NormalizedMessage, tenantCfg *core.TenantPolicy) (string, error) {
	if p.cfg.Transcriber == nil || p.cfg.Blobs == nil || strings.TrimSpace(msg.MediaRef) == "" {
		return "", errors.New("transcrição indisponível")
	}
	audioBytes, err := p.cfg.Blobs.Get(ctx, msg.MediaRef)
	if err != nil {
		return "", err
	}
	mimeType := inferAudioMime(msg.MediaRef)
	if dyn, ok := p.cfg.Transcriber.(configAudioTranscriber); ok {
		audioProvider, audioModel := p.resolveStageLLM("audio", tenantCfg, p.cfg.AudioModel)
		return dyn.TranscribeWithConfig(ctx, audioBytes, mimeType, audioProvider, audioModel)
	}
	return p.cfg.Transcriber.Transcribe(ctx, audioBytes, mimeType)
}

func inferAudioMime(mediaRef string) string {
	lower := strings.ToLower(mediaRef)
	switch {
	case strings.HasSuffix(lower, ".mp3"):
		return "audio/mpeg"
	case strings.HasSuffix(lower, ".m4a"):
		return "audio/mp4"
	case strings.HasSuffix(lower, ".wav"):
		return "audio/wav"
	default:
		return "audio/ogg"
	}
}

func (p *Pipeline) ensureEncryptedContactInfo(ctx context.Context, msg core.NormalizedMessage) error {
	if p.cfg.PIICipher == nil {
		return nil
	}
	if strings.TrimSpace(msg.RawContact) == "" && strings.TrimSpace(msg.DisplayName) == "" {
		return nil
	}
	state, err := p.cfg.Store.GetSourceState(ctx, msg.TenantID, msg.Channel, msg.SourceKey)
	if err != nil {
		return err
	}
	if strings.TrimSpace(state.Meta.EncryptedContactInfo) != "" {
		return nil
	}
	encoded, err := security.EncryptContactInfo(p.cfg.PIICipher, security.ContactInfo{
		Channel:       msg.Channel,
		RawIdentifier: strings.TrimSpace(msg.RawContact),
		DisplayName:   strings.TrimSpace(msg.DisplayName),
	})
	if err != nil || encoded == "" {
		return err
	}
	now := p.now()
	meta := state.Meta
	meta.TenantID = msg.TenantID
	meta.Channel = msg.Channel
	meta.SourceKey = msg.SourceKey
	if meta.Status == "" {
		meta.Status = core.SourceStatusActive
	}
	if meta.CreatedAt.IsZero() {
		meta.CreatedAt = now
	}
	meta.UpdatedAt = now
	meta.EncryptedContactInfo = encoded
	return p.cfg.Store.SaveSourceMeta(ctx, meta)
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

	initialState, err := p.cfg.Store.GetSourceState(ctx, tenantID, channelID, sourceKey)
	if err != nil {
		return p.failPipeline(ctx, latest, runID, traceRef, nil, err)
	}

	preparedBatch, handled, err := p.prepareMediaBatch(ctx, &tenantCfg, pk.Actors.Business, batch, runID, traceRef)
	if err != nil {
		return p.failPipeline(ctx, latest, runID, traceRef, nil, err)
	}
	if handled {
		return nil
	}
	batch = preparedBatch
	latest = batch[len(batch)-1]

	batchTexts := make([]string, len(batch))
	for i, m := range batch {
		batchTexts[i] = m.Text
	}
	if skip, gateErr := p.enforcePreLLMGates(ctx, tenantCfg, pk, initialState, latest, runID, batchTexts); gateErr != nil {
		return p.failPipeline(ctx, latest, runID, traceRef, nil, gateErr)
	} else if skip {
		return nil
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

	scope := contextmod.Scope{
		TenantID:  tenantID,
		Channel:   channelID,
		SourceKey: sourceKey,
	}
	excludeIDs := make([]string, 0, len(batch))
	for _, m := range batch {
		excludeIDs = append(excludeIDs, m.MessageID)
	}
	initialSeq := p.dispatcher.CurrentSeq(tenantID, channelID, sourceKey)
	reprocessCount := 0

staleReprocessLoop:
	trace.BatchSize = len(batch)
	trace.MessageID = latest.MessageID

	activeStage := initialState.Meta.JourneyStage
	if p.sessionCache != nil {
		if sess, err := p.sessionCache.GetSession(ctx, tenantID, channelID, sourceKey); err == nil && sess != nil && sess.CurrentState != "" {
			activeStage = sess.CurrentState
		}
	}
	if activeStage == "" && pk.FSM != nil {
		activeStage = pk.FSM.InitialState
	}

	// Retry de leitura+análise+commit se houver ErrVersionConflict (máximo 2 tentativas)
	var updatedState *core.SourceState
	var diffs []FactChange
	var analysisOutcome *StructuredAnalysisOutcome
	var fsmOutcome TransitionOutcome

	const maxVersionRetries = 2
	for attempt := 0; attempt < maxVersionRetries; attempt++ {
		analysisBundle, ctxErr := p.cfg.ContextSvc.GetContext(ctx, contextmod.GetContextRequest{
			Scope:             scope,
			View:              contextmod.ViewAnalysis,
			ExcludeMessageIDs: excludeIDs,
			RecentLimit:       p.cfg.RecentMessagesLimit,
		})
		if ctxErr != nil {
			return p.failPipeline(ctx, latest, runID, traceRef, trace, ctxErr)
		}
		truncCopy := analysisBundle.Truncation
		trace.AnalysisTruncation = &truncCopy

		winOut, recentMsgs, newPromptMsg := buildWindowForBundle(analysisBundle, batch, pk, p.cfg.RecentMessagesLimit)
		trace.Window = &winOut

		promptInput := pack.PromptInput{
			Pack:           pk,
			Summary:        bundleSummaryText(analysisBundle),
			Facts:          analysisBundle.Facts,
			Signals:        analysisBundle.Signals,
			Context:        bundleStructuredSections(analysisBundle),
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

		analysisCfg := p.resolveStageLLMConfig("analysis", &tenantCfg, p.cfg.AnalysisModel)
		analysisProvider, analysisModel := analysisCfg.Provider, analysisCfg.Model
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
				"stage":    "analysis",
				"provider": analysisProvider,
				"model":    analysisModel,
				"system":   renderedAnalysis.System,
				"prompt":   renderedAnalysis.User,
			},
		})

		analysisOutcome, err = GenerateStructuredAnalysisWithConfig(ctx, p.cfg.LLM, analysisCfg, pk, renderedAnalysis)
		trace.Analysis = &StageTrace{
			Stage:            "analysis",
			Model:            analysisModel,
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

		if p.tryDiscardStaleAndExpandBatch(ctx, tenantID, channelID, sourceKey, runID, "analysis", &initialSeq, &reprocessCount, &batch, &latest, &excludeIDs, trace) {
			goto staleReprocessLoop
		}

		now := p.now()
		newContext := BuildConversationContext(analysisOutcome.Result.Context, now)
		prevTurnCount := 0
		if analysisBundle.Summary != nil {
			prevTurnCount = analysisBundle.Summary.TurnCount
		}
		newSummary := core.Summary{
			TenantID:  tenantID,
			Channel:   channelID,
			SourceKey: sourceKey,
			Text:      analysisOutcome.Result.Summary,
			TurnCount: prevTurnCount + 1,
			UpdatedAt: now,
		}

		summaryText := analysisOutcome.Result.Summary
		nextStage := activeStage
		if pk.FSM != nil {
			fsmAnalysis := *analysisOutcome.Result
			fsmFacts := make(map[string]string, len(analysisBundle.Facts)+len(analysisOutcome.Result.Facts))
			for k, v := range analysisBundle.Facts {
				fsmFacts[k] = v
			}
			for k, v := range analysisOutcome.Result.Facts {
				fsmFacts[k] = v
			}
			fsmAnalysis.Facts = fsmFacts

			fsmSignals := make(map[string]bool, len(analysisBundle.Signals)+len(analysisOutcome.Result.Signals))
			for k, v := range analysisBundle.Signals {
				fsmSignals[k] = v
			}
			for k, v := range analysisOutcome.Result.Signals {
				fsmSignals[k] = v
			}
			fsmAnalysis.Signals = fsmSignals

			fsmOutcome = CalculateNextState(activeStage, fsmAnalysis, pk.FSM)
			nextStage = fsmOutcome.NextState
			trace.FSMOutcome = &fsmOutcome
		}

		applyRes, commitErr := p.cfg.ContextSvc.ApplyFacts(ctx, contextmod.ApplyFactsRequest{
			Scope:             scope,
			ExpectedVersion:   analysisBundle.SourceVersion,
			JourneyStage:      nextStage,
			Facts:             analysisOutcome.Result.Facts,
			ClearedFacts:      analysisOutcome.Result.ClearedFacts,
			Actor:             "brain:analysis",
			Summary:           &summaryText,
			Signals:           analysisOutcome.Result.Signals,
			StructuredContext: toStructuredContextView(scope, newContext),
			Run: &contextmod.RunWriteInput{
				ID:               runID,
				MessageID:        latest.MessageID,
				Stage:            "pipeline",
				Status:           analysisOutcome.Status,
				Model:            p.cfg.AnalysisModel,
				PromptTokens:     analysisOutcome.PromptTokens,
				CompletionTokens: analysisOutcome.CompletionTokens,
				DurationMs:       analysisOutcome.DurationMs,
				TraceBlobRef:     traceRef,
				TTL:              p.runTTL(),
				CreatedAt:        trace.CreatedAt,
			},
		})
		if commitErr != nil {
			if errors.Is(commitErr, contextmod.ErrVersionConflict) && attempt+1 < maxVersionRetries {
				continue
			}
			return p.failPipeline(ctx, latest, runID, traceRef, trace, commitErr)
		}

		if p.sessionCache != nil && pk.FSM != nil {
			_ = p.sessionCache.SaveSession(ctx, &sessioncache.ActiveSession{
				TenantID:     tenantID,
				Channel:      channelID,
				SourceKey:    sourceKey,
				CurrentState: nextStage,
				LastActivity: now,
				Metadata: map[string]string{
					"last_action": fsmOutcome.ActionToTrigger,
					"intent":      analysisOutcome.Result.Intent,
				},
			})
		}

		diffs = factChangesFromDiffs(applyRes.Diff)
		trace.ParsedResult = analysisOutcome.Result
		trace.FactsDiff = diffs

		meta := initialState.Meta
		meta.JourneyStage = nextStage
		if analysisBundle.SourceStatus != "" {
			meta.Status = core.SourceStatus(analysisBundle.SourceStatus)
		}
		updatedState = &core.SourceState{
			Version: applyRes.SourceVersion,
			Meta:    meta,
			Summary: newSummary,
			Facts:   applyRes.Facts,
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

	var journeyDirective *pack.ResponseDirectiveDef
	if jDef := pk.ResolveJourney(tenantCfg.Workflow); jDef != nil && p.cfg.Store != nil {
		jOut := p.evaluateAndCommitJourney(ctx, pk, jDef, tenantID, channelID, sourceKey, runID, analysisOutcome.Result, updatedState, core.SystemEvent{
			EventID:    runID,
			Type:       core.SysEventUserMessage,
			Origin:     latest.Origin,
			TenantID:   tenantID,
			Channel:    channelID,
			SourceKey:  sourceKey,
			MessageID:  latest.MessageID,
			OccurredAt: p.now(),
		})
		journeyDirective = jOut.Directive
	}

	withinHours := core.IsWithinBusinessHours(tenantCfg, p.now())
	decision := Route(RouteInput{
		Pack:               pk,
		Analysis:           analysisOutcome.Result,
		State:              updatedState,
		Policy:             tenantCfg,
		WithinBusinessHour: withinHours,
	})
	trace.Decision = &decision

	if pk.FSM != nil && withinHours {
		switch fsmOutcome.ActionDef.Type {
		case "opt_out":
			decision.Action = DecisionOptOut
			decision.Reason = fsmOutcome.Reason
		case "handoff":
			decision.Action = DecisionHandoff
			decision.Reason = fsmOutcome.Reason
		case "escalate":
			decision.Action = DecisionEscalate
			decision.Reason = fsmOutcome.Reason
		case "message_template":
			p.deriveAndSaveRuleTasks(ctx, pk, analysisOutcome.Result, latest)
			replyText := fsmOutcome.ActionDef.Text
			if plainTextOnly {
				replyText = SanitizePlainText(replyText)
			}
			trace.FinalReply = replyText
			if err := p.recordRouterOnlyRun(ctx, latest, runID, traceRef, trace, analysisOutcome.Status); err != nil {
				return err
			}
			_, err := p.DeliverTextOutbound(ctx, tenantID, channelID, latest.Account, sourceKey, pk.Actors.Business, replyText, runID, traceRef)
			return err
		case "knowledge_reply":
			if decision.Action != DecisionCreateTask {
				decision.Action = DecisionReply
			}
			decision.Reason = fsmOutcome.Reason
		}
		trace.Decision = &decision
	}

	if decision.Action != DecisionOptOut && decision.Action != DecisionSkip {
		p.deriveAndSaveRuleTasks(ctx, pk, analysisOutcome.Result, latest)
	}

	switch decision.Action {
	case DecisionOptOut:
		if err := UpdateSourceStatus(ctx, p.cfg.Store, p.now(), tenantID, channelID, sourceKey, core.SourceStatusOptedOut); err != nil {
			return p.failPipeline(ctx, latest, runID, traceRef, trace, err)
		}
		p.publishStatusUpdated(tenantID, channelID, sourceKey, core.SourceStatusOptedOut, decision.Reason)
		return p.recordRouterOnlyRun(ctx, latest, runID, traceRef, trace, analysisOutcome.Status)

	case DecisionHandoff:
		if err := UpdateSourceStatus(ctx, p.cfg.Store, p.now(), tenantID, channelID, sourceKey, core.SourceStatusHuman); err != nil {
			return p.failPipeline(ctx, latest, runID, traceRef, trace, err)
		}
		p.publishStatusUpdated(tenantID, channelID, sourceKey, core.SourceStatusHuman, decision.Reason)
		now := p.now()
		_ = p.saveAndPublishTask(ctx, core.Task{
			ID:        newID("task"),
			TenantID:  tenantID,
			Channel:   channelID,
			SourceKey: sourceKey,
			Kind:      "task",
			Title:     "Atendimento humano solicitado pelo cliente",
			Type:      "handoff",
			Status:    core.TaskStatusPending,
			DueAt:     &now,
			Metadata:  map[string]string{"intent": decision.Intent},
			CreatedAt: now,
		})
		return p.recordRouterOnlyRun(ctx, latest, runID, traceRef, trace, analysisOutcome.Status)

	case DecisionOutOfHoursReply:
		now := p.now()
		_ = p.saveAndPublishTask(ctx, core.Task{
			ID:        newID("task"),
			TenantID:  tenantID,
			Channel:   channelID,
			SourceKey: sourceKey,
			Kind:      "task",
			Title:     "Retornar contato recebido fora do horário comercial",
			Type:      "out_of_hours_followup",
			Status:    core.TaskStatusPending,
			DueAt:     &now,
			Metadata:  map[string]string{"intent": decision.Intent},
			CreatedAt: now,
		})
		if strings.TrimSpace(tenantCfg.OutOfHoursReply) != "" {
			replyText := tenantCfg.OutOfHoursReply
			if plainTextOnly {
				replyText = SanitizePlainText(replyText)
			}
			trace.FinalReply = replyText
			if err := p.recordRouterOnlyRun(ctx, latest, runID, traceRef, trace, analysisOutcome.Status); err != nil {
				return err
			}
			_, err := p.DeliverTextOutbound(ctx, tenantID, channelID, latest.Account, sourceKey, pk.Actors.Business, replyText, runID, traceRef)
			return err
		}
		return p.recordRouterOnlyRun(ctx, latest, runID, traceRef, trace, analysisOutcome.Status)

	case DecisionSkip:
		p.publishRunSkipped(latest, runID, string(decision.Action), decision.Reason)
		return p.recordRouterOnlyRun(ctx, latest, runID, traceRef, trace, analysisOutcome.Status)

	case DecisionEscalate:
		_ = p.notifier.Notify(ctx, core.AdminAlert{
			TenantID:  tenantID,
			Channel:   channelID,
			SourceKey: sourceKey,
			Reason:    decision.Reason,
			Message:   joinBatchTexts(batch),
			ContextData: map[string]string{
				"intent":            decision.Intent,
				"run_id":            runID,
				"summary":           updatedState.Summary.Text,
				"question":          joinBatchTexts(batch),
				"origin_channel":    channelID,
				"origin_source_key": sourceKey,
			},
			CreatedAt: p.now(),
		})
		replyText := NeutralEscalationReply
		if plainTextOnly {
			replyText = SanitizePlainText(replyText)
		}
		trace.FinalReply = replyText
		if err := p.recordRouterOnlyRun(ctx, latest, runID, traceRef, trace, "escalated"); err != nil {
			return err
		}
		_, err := p.DeliverTextOutbound(ctx, tenantID, channelID, latest.Account, sourceKey, pk.Actors.Business, replyText, runID, traceRef)
		return err
	}

	// Estágio de resposta (BRAIN-03) via módulo de contexto (CTX-03 / CTX-04 / CTX-05)
	knowledgeQuery := BuildKnowledgeSearchQuery(
		joinBatchTexts(batch),
		updatedState.Summary.Text,
		decision.Intent,
		updatedState.Facts,
	)
	replyBundle, err := p.cfg.ContextSvc.GetContext(ctx, contextmod.GetContextRequest{
		Scope:             scope,
		View:              contextmod.ViewResponse,
		ExcludeMessageIDs: excludeIDs,
		RecentLimit:       p.cfg.RecentMessagesLimit,
		KnowledgeQuery:    knowledgeQuery,
	})
	if err != nil {
		return p.failPipeline(ctx, latest, runID, traceRef, trace, err)
	}
	replyTruncCopy := replyBundle.Truncation
	trace.ReplyTruncation = &replyTruncCopy

	replyWin, recentForReply, newReplyPromptMsg := buildWindowForBundle(replyBundle, batch, pk, p.cfg.RecentMessagesLimit)
	trace.Window = &replyWin
	knowledge := bundleKnowledge(replyBundle)

	replyInput := pack.PromptInput{
		Pack:           pk,
		Summary:        bundleSummaryText(replyBundle),
		Facts:          replyBundle.Facts,
		Signals:        replyBundle.Signals,
		Context:        bundleStructuredSections(replyBundle),
		RecentMessages: recentForReply,
		NewMessage:     newReplyPromptMsg,
		Knowledge:      knowledge.Docs,
		QAContent:      knowledge.QAContent,
		PlainTextOnly:  plainTextOnly,
		Directive:      journeyDirective,
	}

	renderedReply, err := pack.RenderReply(pk, replyInput)
	if err != nil {
		return p.failPipeline(ctx, latest, runID, traceRef, trace, err)
	}

	responseCfg := p.resolveStageLLMConfig("response", &tenantCfg, p.cfg.ResponseModel)
	responseProvider, responseModel := responseCfg.Provider, responseCfg.Model
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
			"stage":    "response",
			"provider": responseProvider,
			"model":    responseModel,
			"system":   renderedReply.System,
			"prompt":   renderedReply.User,
		},
	})

	replyResp, err := p.cfg.LLM.Generate(ctx, core.LLMRequest{
		Stage:           "response",
		Provider:        responseProvider,
		Model:           responseModel,
		ReasoningEffort: responseCfg.ReasoningEffort,
		System:          renderedReply.System,
		Prompt:          renderedReply.User,
		Temperature:     0.3,
		MaxTokens:       4096,
	})
	if err != nil {
		return p.failPipeline(ctx, latest, runID, traceRef, trace, err)
	}

	if p.tryDiscardStaleAndExpandBatch(ctx, tenantID, channelID, sourceKey, runID, "response", &initialSeq, &reprocessCount, &batch, &latest, &excludeIDs, trace) {
		p.cancelPendingOutboxForRun(ctx, tenantID, channelID, sourceKey, runID)
		goto staleReprocessLoop
	}

	parsedReply, _ := ParseReplyOutput(replyResp.Content)
	grounding := CheckGrounding(parsedReply, knowledge, recentForReply, replyInput.NewMessage, updatedState.Facts)
	trace.Grounding = &grounding

	finalReply := parsedReply.Reply
	replyStatus := "ok"
	if !grounding.Grounded {
		replyStatus = "escalated"
		finalReply = NeutralFallbackReply
		_ = p.notifier.Notify(ctx, core.AdminAlert{
			TenantID:  tenantID,
			Channel:   channelID,
			SourceKey: sourceKey,
			Reason:    grounding.Reason,
			Message:   replyInput.NewMessage.Text,
			ContextData: map[string]string{
				"run_id":            runID,
				"trace_ref":         traceRef,
				"summary":           updatedState.Summary.Text,
				"question":          replyInput.NewMessage.Text,
				"origin_channel":    channelID,
				"origin_source_key": sourceKey,
			},
			CreatedAt: p.now(),
		})
	}
	if LooksLikeInternalJSON(finalReply) {
		finalReply = NeutralFallbackReply
	}
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
		Status:           replyStatus,
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

	runStatus := analysisOutcome.Status
	runStage := "pipeline"
	var draftRef string
	if decision.Action == DecisionDraftOnly || decision.Action == DecisionAwaitApproval {
		draftRef = store.S3DraftKey(tenantID, channelID, sourceKey, runID)
		draftBytes, err := json.Marshal(DraftPayload{
			RunID:         runID,
			TenantID:      tenantID,
			Channel:       channelID,
			SourceKey:     sourceKey,
			Account:       latest.Account,
			BusinessActor: pk.Actors.Business,
			Text:          finalReply,
			Intent:        decision.Intent,
			Mode:          string(tenantCfg.Mode),
			CreatedAt:     p.now(),
		})
		if err != nil {
			return err
		}
		if err := p.cfg.Blobs.Put(ctx, draftRef, draftBytes, "application/json"); err != nil {
			return fmt.Errorf("erro ao salvar rascunho no BlobStore: %w", err)
		}
		if decision.Action == DecisionDraftOnly {
			runStage = "draft"
			runStatus = "draft_only"
		} else {
			runStage = "approval"
			runStatus = "awaiting_approval"
		}
	}

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
		Stage:            runStage,
		Status:           runStatus,
		Model:            p.cfg.ResponseModel,
		PromptTokens:     trace.Analysis.PromptTokens + replyResp.PromptTokens,
		CompletionTokens: trace.Analysis.CompletionTokens + replyResp.CompletionTokens,
		DurationMs:       trace.TotalDurationMs,
		TraceBlobRef:     traceRef,
		TTL:              p.runTTL(),
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
			"text":           finalReply,
			"trace_ref":      traceRef,
			"draft_ref":      draftRef,
			"mode":           string(tenantCfg.Mode),
			"total_tokens":   trace.TotalTokens,
			"total_duration": trace.TotalDurationMs,
		},
	})

	if decision.Action == DecisionDraftOnly {
		return nil
	}

	if decision.Action == DecisionAwaitApproval {
		now := p.now()
		apprTask := core.Task{
			ID:           newID("appr"),
			TenantID:     tenantID,
			Channel:      channelID,
			SourceKey:    sourceKey,
			Kind:         "approval",
			Title:        "Aprovar resposta sugerida",
			Type:         "reply_approval",
			Status:       core.TaskStatusPending,
			DueAt:        &now,
			DraftBlobRef: draftRef,
			Metadata: map[string]string{
				"run_id":  runID,
				"intent":  decision.Intent,
				"preview": truncateText(finalReply, 140),
			},
			CreatedAt: now,
		}
		if err := p.saveAndPublishTask(ctx, apprTask); err != nil {
			return err
		}
		p.publish(core.Event{
			ID:        newID("ev"),
			Type:      core.EventApprovalPending,
			TenantID:  tenantID,
			Channel:   channelID,
			SourceKey: sourceKey,
			MessageID: latest.MessageID,
			RunID:     runID,
			At:        now,
			Payload: map[string]any{
				"task_id":   apprTask.ID,
				"draft_ref": draftRef,
				"preview":   finalReply,
			},
		})
		return nil
	}

	// Persiste e envia resposta outbound
	_, err = p.DeliverTextOutbound(ctx, tenantID, channelID, latest.Account, sourceKey, pk.Actors.Business, finalReply, runID, traceRef)
	if err != nil {
		return err
	}
	return p.ScheduleNudge(ctx, tenantID, channelID, sourceKey, latest.Account)
}

// ApproveTask aprova (e opcionalmente edita) um rascunho de resposta pendente e o envia pelo canal.
func (p *Pipeline) ApproveTask(ctx context.Context, tenant, channel, sourceKey, taskID, overrideText string) (*core.Task, error) {
	task, err := p.cfg.Store.GetTask(ctx, tenant, channel, sourceKey, taskID)
	if err != nil {
		return nil, err
	}
	if task.Status != core.TaskStatusPending {
		return nil, fmt.Errorf("tarefa %q já está com status %s", taskID, task.Status)
	}

	var draft DraftPayload
	if task.DraftBlobRef != "" {
		raw, err := p.cfg.Blobs.Get(ctx, task.DraftBlobRef)
		if err != nil {
			return nil, fmt.Errorf("falha ao carregar rascunho %s: %w", task.DraftBlobRef, err)
		}
		if err := json.Unmarshal(raw, &draft); err != nil {
			return nil, fmt.Errorf("falha ao decodificar rascunho: %w", err)
		}
	}

	text := strings.TrimSpace(overrideText)
	if text == "" {
		text = strings.TrimSpace(draft.Text)
	}
	if text == "" {
		return nil, errors.New("texto da resposta aprovada está vazio")
	}

	businessActor := draft.BusinessActor
	if businessActor == "" {
		businessActor = "business"
	}
	if ch := p.getChannel(channel); ch != nil && ch.Capabilities().PlainTextOnly {
		text = SanitizePlainText(text)
	}

	if _, err := p.DeliverTextOutbound(ctx, tenant, channel, draft.Account, sourceKey, businessActor, text, draft.RunID, ""); err != nil {
		return nil, err
	}

	now := p.now()
	task.Status = core.TaskStatusCompleted
	task.CompletedAt = &now
	if err := p.cfg.Store.SaveTask(ctx, *task); err != nil {
		return nil, err
	}
	return task, nil
}

func (p *Pipeline) deriveAndSaveRuleTasks(ctx context.Context, pk *pack.Pack, analysis *pack.AnalysisResult, latest core.NormalizedMessage) {
	derived := DeriveTasks(DeriveTasksInput{
		Pack:      pk,
		Analysis:  analysis,
		TenantID:  latest.TenantID,
		Channel:   latest.Channel,
		SourceKey: latest.SourceKey,
		MessageID: latest.MessageID,
		Now:       p.now(),
	})
	if len(derived) == 0 {
		return
	}
	existing, err := p.cfg.Store.ListSourceTasks(ctx, latest.TenantID, latest.Channel, latest.SourceKey)
	if err != nil {
		p.logger.Warn("falha ao listar tarefas existentes para deduplicação", slog.Any("error", err))
		return
	}
	for _, task := range FilterNewTasks(derived, existing) {
		if err := p.saveAndPublishTask(ctx, task); err != nil {
			p.logger.Warn("falha ao salvar tarefa derivada", slog.String("task_id", task.ID), slog.Any("error", err))
		}
	}
}

func truncateText(s string, max int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= max {
		return string(r)
	}
	return string(r[:max]) + "..."
}

// DeliverTextOutbound grava mensagem crua no BlobStore, metadados MSG# no Store, envia no canal e publica EventReplySent.
func (p *Pipeline) DeliverTextOutbound(
	ctx context.Context,
	tenantID, channelID, account, sourceKey, businessActor, text, runID, traceRef string,
) (string, error) {
	outMsg := core.NormalizedMessage{
		TenantID:  tenantID,
		Channel:   channelID,
		Account:   account,
		SourceKey: sourceKey,
		MessageID: newID("out"),
		Timestamp: p.now(),
		Direction: core.DirectionOutbound,
		Type:      core.MessageTypeText,
		Text:      text,
	}
	outMsg.RawRef = store.S3RawMessageKey(tenantID, channelID, sourceKey, outMsg.Timestamp, outMsg.MessageID)

	outBytes, err := json.Marshal(outMsg)
	if err != nil {
		return "", err
	}
	if err := p.cfg.Blobs.Put(ctx, outMsg.RawRef, outBytes, "application/json"); err != nil {
		return "", fmt.Errorf("erro ao gravar resposta crua no BlobStore: %w", err)
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
		Sender:    businessActor,
		Origin:    core.OriginBot,
	}); err != nil {
		return "", fmt.Errorf("erro ao gravar MSG# outbound: %w", err)
	}

	now := p.now()
	if err := p.cfg.Store.PreRegisterOutbound(ctx, core.OutboundMessageRecord{
		TenantID:  tenantID,
		Channel:   channelID,
		SourceKey: sourceKey,
		MessageID: outMsg.MessageID,
		Status:    core.OutboundStatusReserved,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		return "", fmt.Errorf("erro ao pré-registrar OUTMSG# antes do envio: %w", err)
	}

	if ch := p.getChannel(channelID); ch != nil {
		if err := ch.Send(ctx, outMsg); err != nil {
			_ = p.cfg.Store.UpdateOutboundStatus(ctx, tenantID, channelID, outMsg.MessageID, core.OutboundStatusFailed, p.now())
			return "", fmt.Errorf("erro ao enviar resposta no canal %s: %w", channelID, err)
		}
	}
	_ = p.cfg.Store.UpdateOutboundStatus(ctx, tenantID, channelID, outMsg.MessageID, core.OutboundStatusSent, p.now())

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
			"text":      text,
			"trace_ref": traceRef,
		},
	})
	return outMsg.MessageID, nil
}

func (p *Pipeline) enforcePreLLMGates(
	ctx context.Context,
	policy core.TenantPolicy,
	pk *pack.Pack,
	state *core.SourceState,
	latest core.NormalizedMessage,
	runID string,
	batchTexts []string,
) (bool, error) {
	gate := EvaluatePreLLMGates(PreGateInput{
		Policy:     policy,
		State:      state,
		Pack:       pk,
		BatchTexts: batchTexts,
		Now:        p.now(),
	})
	if !gate.Allowed {
		if gate.UpdateStatus != "" {
			if err := UpdateSourceStatus(ctx, p.cfg.Store, p.now(), latest.TenantID, latest.Channel, latest.SourceKey, gate.UpdateStatus); err != nil {
				return false, err
			}
			p.publishStatusUpdated(latest.TenantID, latest.Channel, latest.SourceKey, gate.UpdateStatus, gate.Reason)
		}
		p.publishRunSkipped(latest, runID, string(gate.Action), gate.Reason)
		return true, nil
	}
	if gate.AutoResumed && gate.UpdateStatus == core.SourceStatusActive {
		if err := UpdateSourceStatus(ctx, p.cfg.Store, p.now(), latest.TenantID, latest.Channel, latest.SourceKey, core.SourceStatusActive); err != nil {
			return false, err
		}
		if state != nil {
			state.Meta.Status = core.SourceStatusActive
			state.Meta.PausedUntil = nil
		}
		p.publishStatusUpdated(latest.TenantID, latest.Channel, latest.SourceKey, core.SourceStatusActive, gate.Reason)
	}

	allowed, limitReason := p.limiter.Allow(latest.TenantID, latest.Channel, latest.SourceKey, policy.RateLimit)
	if !allowed {
		p.publishRunSkipped(latest, runID, "rate_limited", limitReason)
		return true, nil
	}
	return false, nil
}

func (p *Pipeline) publishStatusUpdated(tenantID, channelID, sourceKey string, status core.SourceStatus, reason string) {
	p.publish(core.Event{
		ID:        newID("ev"),
		Type:      core.EventSourceStatusUpdated,
		TenantID:  tenantID,
		Channel:   channelID,
		SourceKey: sourceKey,
		At:        p.now(),
		Payload: map[string]any{
			"status": string(status),
			"reason": reason,
		},
	})
}

func (p *Pipeline) publishRunSkipped(latest core.NormalizedMessage, runID, action, reason string) {
	p.publish(core.Event{
		ID:        newID("ev"),
		Type:      core.EventRunSkipped,
		TenantID:  latest.TenantID,
		Channel:   latest.Channel,
		SourceKey: latest.SourceKey,
		MessageID: latest.MessageID,
		RunID:     runID,
		At:        p.now(),
		Payload: map[string]any{
			"action": action,
			"reason": reason,
		},
	})
}

func (p *Pipeline) saveAndPublishTask(ctx context.Context, task core.Task) error {
	if err := p.cfg.Store.SaveTask(ctx, task); err != nil {
		return err
	}
	p.publish(core.Event{
		ID:        newID("ev"),
		Type:      core.EventTaskCreated,
		TenantID:  task.TenantID,
		Channel:   task.Channel,
		SourceKey: task.SourceKey,
		At:        p.now(),
		Payload: map[string]any{
			"task_id": task.ID,
			"kind":    task.Kind,
			"type":    task.Type,
			"title":   task.Title,
		},
	})
	return nil
}

func (p *Pipeline) recordRouterOnlyRun(ctx context.Context, latest core.NormalizedMessage, runID, traceRef string, trace *RunTrace, status string) error {
	if trace.Analysis != nil {
		trace.TotalTokens = trace.Analysis.PromptTokens + trace.Analysis.CompletionTokens
		trace.TotalDurationMs = trace.Analysis.DurationMs
	}
	if err := p.saveTrace(ctx, traceRef, trace); err != nil {
		return err
	}
	return p.cfg.Store.SaveRun(ctx, core.Run{
		ID:               runID,
		TenantID:         latest.TenantID,
		Channel:          latest.Channel,
		SourceKey:        latest.SourceKey,
		MessageID:        latest.MessageID,
		Stage:            "router",
		Status:           status,
		Model:            p.cfg.AnalysisModel,
		PromptTokens:     trace.TotalTokens,
		CompletionTokens: 0,
		DurationMs:       trace.TotalDurationMs,
		TraceBlobRef:     traceRef,
		TTL:              p.runTTL(),
		CreatedAt:        trace.CreatedAt,
	})
}

func (p *Pipeline) runTTL() int64 {
	days := p.cfg.RunRetentionDays
	if days <= 0 {
		days = 90
	}
	return p.now().Add(time.Duration(days) * 24 * time.Hour).Unix()
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
		TTL:          p.runTTL(),
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

func (p *Pipeline) tryDiscardStaleAndExpandBatch(
	ctx context.Context,
	tenantID, channelID, sourceKey, runID, stage string,
	initialSeq *uint64,
	reprocessCount *int,
	batch *[]core.NormalizedMessage,
	latest *core.NormalizedMessage,
	excludeIDs *[]string,
	trace *RunTrace,
) bool {
	if p.dispatcher == nil {
		return false
	}
	currentSeq := p.dispatcher.CurrentSeq(tenantID, channelID, sourceKey)
	dec := ShouldDiscardReply(*initialSeq, currentSeq, *reprocessCount, DefaultStaleReplyPolicy())
	if !dec.Discard {
		return false
	}
	extra, newSeq := p.dispatcher.TakePending(tenantID, channelID, sourceKey)
	if len(extra) == 0 {
		*initialSeq = currentSeq
		return false
	}
	*batch = append(*batch, extra...)
	*latest = (*batch)[len(*batch)-1]
	for _, m := range extra {
		*excludeIDs = append(*excludeIDs, m.MessageID)
	}
	*initialSeq = newSeq
	*reprocessCount++
	if trace != nil {
		trace.ReprocessCount = *reprocessCount
		trace.DiscardedReplies++
		trace.BatchSize = len(*batch)
	}
	p.publish(core.Event{
		ID:        newID("ev"),
		Type:      core.EventReplyDiscardedStale,
		TenantID:  tenantID,
		Channel:   channelID,
		SourceKey: sourceKey,
		MessageID: latest.MessageID,
		RunID:     runID,
		At:        p.now(),
		Payload: map[string]any{
			"stage":           stage,
			"reason":          dec.Reason,
			"reprocess_count": *reprocessCount,
			"batch_size":      len(*batch),
		},
	})
	return true
}

func (p *Pipeline) cancelPendingOutboxForRun(ctx context.Context, tenantID, channelID, sourceKey, runID string) {
	if p.cfg.Store == nil {
		return
	}
	items, err := p.cfg.Store.ListOutboxItems(ctx, tenantID, channelID, sourceKey, 50)
	if err != nil {
		return
	}
	now := p.now()
	for _, it := range items {
		if it.RunID == runID && it.Status == core.OutboxStatusPending {
			it.Status = core.OutboxStatusDead
			it.LastError = "cancelled_stale_reply"
			it.UpdatedAt = now
			_ = p.cfg.Store.UpdateOutboxItem(ctx, it)
		}
	}
}
