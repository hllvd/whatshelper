package brain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"whatshelper/internal/core"
	"whatshelper/internal/core/fakes"
	"whatshelper/internal/pack"
)

// ReplayMessage define uma entrada de mensagem para replay isolado.
type ReplayMessage struct {
	MessageID string `json:"message_id,omitempty"`
	Text      string `json:"text"`
}

// ReplayRequest configura uma simulação isolada em memória sem afetar o Store real.
type ReplayRequest struct {
	TenantID  string          `json:"tenant_id"`
	Channel   string          `json:"channel,omitempty"`
	SourceKey string          `json:"source_key,omitempty"`
	Messages  []ReplayMessage `json:"messages,omitempty"`
}

// ReplayTurnResult resume os efeitos de um turno processado durante o replay.
type ReplayTurnResult struct {
	MessageID string           `json:"message_id"`
	InputText string           `json:"input_text"`
	ReplyText string           `json:"reply_text,omitempty"`
	Decision  *Decision        `json:"decision,omitempty"`
	FactsDiff []FactChange     `json:"facts_diff,omitempty"`
	Run       *core.Run        `json:"run,omitempty"`
}

// ReplayResult agrega todo o resultado observado do replay isolado em memória.
type ReplayResult struct {
	TenantID    string             `json:"tenant_id"`
	Channel     string             `json:"channel"`
	SourceKey   string             `json:"source_key"`
	Turns       []ReplayTurnResult `json:"turns"`
	FinalState  *core.SourceState  `json:"final_state"`
	Tasks       []core.Task        `json:"tasks"`
	LLMCalls    int                `json:"llm_calls"`
}

// ReplayIsolated executa um replay completo contra MemoryStore/MemoryBlobStore sem tocar nos dados reais.
// Quando req.Messages estiver vazio e SourceKey for informado, lê apenas as mensagens inbound originais do Store/Blobs.
func (p *Pipeline) ReplayIsolated(ctx context.Context, req ReplayRequest) (*ReplayResult, error) {
	tenantID := req.TenantID
	if tenantID == "" {
		tenantID = "demo"
	}
	channelID := req.Channel
	if channelID == "" {
		channelID = "simulator"
	}
	sourceKey := req.SourceKey
	if sourceKey == "" {
		sourceKey = "replay-session"
	}

	messages := req.Messages
	if len(messages) == 0 && p.cfg.Store != nil && p.cfg.Blobs != nil {
		loaded, err := p.loadInboundHistoryForReplay(ctx, tenantID, channelID, sourceKey)
		if err != nil {
			return nil, err
		}
		messages = loaded
	}
	if len(messages) == 0 {
		return nil, errors.New("nenhuma mensagem informada ou encontrada para replay")
	}

	// Carrega política e conhecimento read-only antes de isolar o Store efêmero
	policy, err := p.cfg.Tenants.LoadTenant(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("replay: falha ao carregar tenant %q: %w", tenantID, err)
	}
	bundle, err := p.cfg.Knowledge.Load(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("replay: falha ao carregar conhecimento do tenant %q: %w", tenantID, err)
	}

	clock := fakes.NewFakeClock(p.now())
	ephemeralStore := fakes.NewMemoryStoreWithClock(clock)
	ephemeralBlobs := fakes.NewMemoryBlobStore()
	ephemeralBus := core.NewInMemoryEventBus(64)
	ephemeralCh := fakes.NewMemoryChannel(channelID, 64)
	_ = ephemeralCh.Start(ctx)
	defer func() { _ = ephemeralCh.Stop(context.Background()) }()

	ephemeralPipeline := NewPipeline(ctx, PipelineConfig{
		Store:         ephemeralStore,
		Blobs:         ephemeralBlobs,
		LLM:           p.cfg.LLM,
		Bus:           ephemeralBus,
		Clock:         clock,
		Tenants:       staticTenantLoader{policy: policy},
		Knowledge:     staticKnowledgeSource{bundle: bundle},
		PackLoader:    p.cfg.PackLoader,
		AnalysisModel: p.cfg.AnalysisModel,
		ResponseModel: p.cfg.ResponseModel,
		NudgeModel:    p.cfg.NudgeModel,
	})
	ephemeralPipeline.RegisterChannel(ephemeralCh)
	defer ephemeralPipeline.Stop()

	turns := make([]ReplayTurnResult, 0, len(messages))
	prevSentCount := 0

	for i, m := range messages {
		if m.Text == "" {
			continue
		}
		msgID := m.MessageID
		if msgID == "" {
			msgID = fmt.Sprintf("replay-%d", i+1)
		}
		clock.Advance(1 * time.Second)
		norm := core.NormalizedMessage{
			TenantID:  tenantID,
			Channel:   channelID,
			SourceKey: sourceKey,
			MessageID: msgID,
			Timestamp: clock.Now(),
			Direction: core.DirectionInbound,
			Type:      core.MessageTypeText,
			Text:      m.Text,
		}
		if err := ephemeralPipeline.Ingest(ctx, norm); err != nil {
			return nil, err
		}
		if err := ephemeralPipeline.ProcessBatch(ctx, []core.NormalizedMessage{norm}); err != nil {
			return nil, err
		}

		sent := ephemeralCh.SentMessages()
		replyText := ""
		if len(sent) > prevSentCount {
			replyText = sent[len(sent)-1].Text
			prevSentCount = len(sent)
		}

		runs, _ := ephemeralStore.ListRuns(ctx, tenantID, channelID, sourceKey, 1)
		var latestRun *core.Run
		var traceDecision *Decision
		var factsDiff []FactChange
		if len(runs) > 0 {
			r := runs[0]
			latestRun = &r
			if r.TraceBlobRef != "" {
				if rawTrace, err := ephemeralBlobs.Get(ctx, r.TraceBlobRef); err == nil {
					var tr RunTrace
					if json.Unmarshal(rawTrace, &tr) == nil {
						traceDecision = tr.Decision
						factsDiff = tr.FactsDiff
					}
				}
			}
		}

		turns = append(turns, ReplayTurnResult{
			MessageID: msgID,
			InputText: m.Text,
			ReplyText: replyText,
			Decision:  traceDecision,
			FactsDiff: factsDiff,
			Run:       latestRun,
		})
	}

	finalState, err := ephemeralStore.GetSourceState(ctx, tenantID, channelID, sourceKey)
	if err != nil {
		return nil, err
	}
	tasks, err := ephemeralStore.ListSourceTasks(ctx, tenantID, channelID, sourceKey)
	if err != nil {
		return nil, err
	}

	return &ReplayResult{
		TenantID:   tenantID,
		Channel:    channelID,
		SourceKey:  sourceKey,
		Turns:      turns,
		FinalState: finalState,
		Tasks:      tasks,
	}, nil
}

func (p *Pipeline) loadInboundHistoryForReplay(ctx context.Context, tenantID, channelID, sourceKey string) ([]ReplayMessage, error) {
	metas, err := p.cfg.Store.ListRecentMessages(ctx, tenantID, channelID, sourceKey, 50)
	if err != nil {
		return nil, err
	}
	var out []ReplayMessage
	for i := len(metas) - 1; i >= 0; i-- {
		m := metas[i]
		if m.Direction != core.DirectionInbound || m.RawRef == "" {
			continue
		}
		raw, err := p.cfg.Blobs.Get(ctx, m.RawRef)
		if err != nil {
			continue
		}
		var norm core.NormalizedMessage
		if json.Unmarshal(raw, &norm) == nil && norm.Text != "" {
			out = append(out, ReplayMessage{MessageID: norm.MessageID, Text: norm.Text})
		}
	}
	return out, nil
}

type staticTenantLoader struct {
	policy core.TenantPolicy
}

func (l staticTenantLoader) LoadTenant(context.Context, string) (TenantConfig, error) {
	return l.policy, nil
}

type staticKnowledgeSource struct {
	bundle KnowledgeBundle
}

func (s staticKnowledgeSource) Load(context.Context, string) (KnowledgeBundle, error) {
	return s.bundle, nil
}

// unused compile guard
var _ = (*pack.Pack)(nil)
