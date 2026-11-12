package brain_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"whatshelper/internal/brain"
	"whatshelper/internal/core"
	"whatshelper/internal/core/fakes"
	"whatshelper/internal/pack"
)

// delayedEchoChannel simula um connector que entrega o eco from_me ANTES de Send() retornar.
type delayedEchoChannel struct {
	id        string
	caps      core.ChannelCapabilities
	inboundCh chan core.NormalizedMessage
	onSend    func(msg core.NormalizedMessage)
	mu        sync.Mutex
	sent      []core.NormalizedMessage
}

func newDelayedEchoChannel(id string, reportsOwn bool, onSend func(core.NormalizedMessage)) *delayedEchoChannel {
	return &delayedEchoChannel{
		id: id,
		caps: core.ChannelCapabilities{
			ReportsOwnMessages: reportsOwn,
		},
		inboundCh: make(chan core.NormalizedMessage, 16),
		onSend:    onSend,
	}
}

func (c *delayedEchoChannel) ID() string                             { return c.id }
func (c *delayedEchoChannel) Capabilities() core.ChannelCapabilities { return c.caps }
func (c *delayedEchoChannel) Health() core.ChannelHealth             { return core.ChannelHealth{Healthy: true} }
func (c *delayedEchoChannel) Start(context.Context) error            { return nil }
func (c *delayedEchoChannel) Stop(context.Context) error             { return nil }
func (c *delayedEchoChannel) Messages() <-chan core.NormalizedMessage { return c.inboundCh }
func (c *delayedEchoChannel) SendTyping(context.Context, string, bool) error { return nil }
func (c *delayedEchoChannel) Send(_ context.Context, msg core.NormalizedMessage) error {
	c.mu.Lock()
	c.sent = append(c.sent, msg)
	c.mu.Unlock()
	if c.onSend != nil {
		c.onSend(msg)
	}
	return nil
}

func newOriginTestPipeline(
	t *testing.T,
	st *fakes.MemoryStore,
	ch core.Channel,
	policy core.TenantPolicy,
	clock *fakes.FakeClock,
) (*brain.Pipeline, *core.InMemoryEventBus) {
	t.Helper()
	tmp := t.TempDir()
	packDir := filepath.Join(tmp, "packs", "generic")
	_ = os.MkdirAll(filepath.Join(packDir, "prompts"), 0o755)
	_ = os.WriteFile(filepath.Join(packDir, "pack.yaml"), []byte(gatePackYAML), 0o644)
	_ = os.WriteFile(filepath.Join(packDir, "prompts", "analysis.tmpl"), []byte("Analyze\n{{.OutputSchema}}\n<!-- USER_PROMPT -->\n{{.NewMessage.Text}}"), 0o644)
	_ = os.WriteFile(filepath.Join(packDir, "prompts", "reply.tmpl"), []byte("Reply\n<!-- USER_PROMPT -->\n{{.NewMessage.Text}}"), 0o644)

	kDir := filepath.Join(tmp, "tenants", "t1", "knowledge")
	_ = os.MkdirAll(kDir, 0o755)
	_ = os.WriteFile(filepath.Join(kDir, "info.md"), []byte("Plano custa 100."), 0o644)

	policy.ID = "t1"
	policy.Pack = "generic"
	if err := brain.SeedTenantPolicy(context.Background(), st, policy); err != nil {
		t.Fatalf("SeedTenantPolicy: %v", err)
	}

	bus := core.NewInMemoryEventBus(64)
	p := brain.NewPipeline(context.Background(), brain.PipelineConfig{
		Store:         st,
		Blobs:         fakes.NewMemoryBlobStore(),
		Bus:           bus,
		Clock:         clock,
		Tenants:       brain.NewStoreTenantPolicyLoader(st, nil),
		Knowledge:     brain.NewLocalKnowledgeSource(filepath.Join(tmp, "tenants")),
		PackLoader:    func(name string) (*pack.Pack, error) { return pack.LoadFromDir(filepath.Join(tmp, "packs", name)) },
		AnalysisModel: "m-a",
		ResponseModel: "m-r",
	})
	p.RegisterChannel(ch)
	return p, bus
}

func collectEvents(sub core.Subscription) []core.Event {
	var out []core.Event
	for {
		select {
		case ev := <-sub.Events():
			out = append(out, ev)
		default:
			return out
		}
	}
}

func TestEVT05_ResolveMessageOrigin_AllPureCases(t *testing.T) {
	rec := &core.OutboundMessageRecord{MessageID: "out-1", Status: core.OutboundStatusReserved}

	// 1. Outbound do brain -> OriginBot
	res := brain.ResolveMessageOrigin(brain.OriginResolutionInput{
		Direction: core.DirectionOutbound,
		Channel:   "whatsapp",
		MessageID: "out-1",
	})
	if res.Origin != core.OriginBot {
		t.Fatalf("expected OriginBot for outbound, got %s", res.Origin)
	}

	// 2. Canal admin -> OriginHumanAdmin
	res = brain.ResolveMessageOrigin(brain.OriginResolutionInput{
		Direction: core.DirectionInbound,
		Channel:   brain.AdminChannelID,
		FromMe:    false,
		MessageID: "adm-1",
	})
	if res.Origin != core.OriginHumanAdmin {
		t.Fatalf("expected OriginHumanAdmin, got %s", res.Origin)
	}

	// 3. Histórico antigo -> OriginUnknown (nunca pausa)
	res = brain.ResolveMessageOrigin(brain.OriginResolutionInput{
		Direction:          core.DirectionInbound,
		Channel:            "whatsapp",
		FromMe:             true,
		MessageID:          "hist-1",
		ReportsOwnMessages: true,
		IsOldHistory:       true,
	})
	if res.Origin != core.OriginUnknown {
		t.Fatalf("expected OriginUnknown for old history, got %s", res.Origin)
	}

	// 4. Inbound do cliente (from_me=false) -> OriginCustomer
	res = brain.ResolveMessageOrigin(brain.OriginResolutionInput{
		Direction: core.DirectionInbound,
		Channel:   "whatsapp",
		FromMe:    false,
		MessageID: "in-1",
	})
	if res.Origin != core.OriginCustomer {
		t.Fatalf("expected OriginCustomer, got %s", res.Origin)
	}

	// 5. from_me=true com ID pré-registrado -> OriginBot
	res = brain.ResolveMessageOrigin(brain.OriginResolutionInput{
		Direction:          core.DirectionInbound,
		Channel:            "whatsapp",
		FromMe:             true,
		MessageID:          "out-1",
		ReportsOwnMessages: true,
		OutboundRecord:     rec,
	})
	if res.Origin != core.OriginBot {
		t.Fatalf("expected OriginBot for pre-registered echo, got %s", res.Origin)
	}

	// 6. from_me=true sem ID ou ambíguo -> OriginUnknown
	res = brain.ResolveMessageOrigin(brain.OriginResolutionInput{
		Direction:          core.DirectionInbound,
		Channel:            "whatsapp",
		FromMe:             true,
		MessageID:          "",
		ReportsOwnMessages: true,
	})
	if res.Origin != core.OriginUnknown {
		t.Fatalf("expected OriginUnknown for empty message_id, got %s", res.Origin)
	}
	res = brain.ResolveMessageOrigin(brain.OriginResolutionInput{
		Direction:          core.DirectionInbound,
		Channel:            "whatsapp",
		FromMe:             true,
		MessageID:          "msg-amb",
		ReportsOwnMessages: true,
		IsAmbiguous:        true,
	})
	if res.Origin != core.OriginUnknown {
		t.Fatalf("expected OriginUnknown for ambiguous message, got %s", res.Origin)
	}

	// 7. from_me=true quando canal não reporta mensagens próprias -> OriginUnknown
	res = brain.ResolveMessageOrigin(brain.OriginResolutionInput{
		Direction:          core.DirectionInbound,
		Channel:            "simulator",
		FromMe:             true,
		MessageID:          "sim-1",
		ReportsOwnMessages: false,
	})
	if res.Origin != core.OriginUnknown {
		t.Fatalf("expected OriginUnknown when ReportsOwnMessages=false, got %s", res.Origin)
	}

	// 8. from_me=true com ID desconhecido e ReportsOwnMessages=true -> OriginHumanPhone
	res = brain.ResolveMessageOrigin(brain.OriginResolutionInput{
		Direction:          core.DirectionInbound,
		Channel:            "whatsapp",
		FromMe:             true,
		MessageID:          "phone-999",
		ReportsOwnMessages: true,
	})
	if res.Origin != core.OriginHumanPhone {
		t.Fatalf("expected OriginHumanPhone, got %s", res.Origin)
	}
}

func TestEVT05_Pipeline_EchoBeforeSendReturns_AndTakeoverFlows(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 12, 15, 0, 0, 0, time.UTC)
	clock := fakes.NewFakeClock(now)
	st := fakes.NewMemoryStoreWithClock(clock)
	var p *brain.Pipeline

	// Cenário 2: eco chega ANTES de Send() retornar (com delay real antes do retorno de Send).
	echoArrivedBeforeReturn := make(chan struct{}, 1)
	ch := newDelayedEchoChannel("whatsapp", true, func(out core.NormalizedMessage) {
		dup, err := p.IngestWithStatus(ctx, core.NormalizedMessage{
			TenantID:  out.TenantID,
			Channel:   out.Channel,
			SourceKey: out.SourceKey,
			MessageID: out.MessageID,
			Timestamp: now,
			Direction: core.DirectionInbound,
			Type:      core.MessageTypeText,
			Text:      out.Text,
			FromMe:    true,
		})
		if err != nil {
			t.Errorf("unexpected echo ingest error=%v dup=%v", err, dup)
		}
		time.Sleep(25 * time.Millisecond)
		echoArrivedBeforeReturn <- struct{}{}
	})

	policy := core.TenantPolicy{
		BotEnabled: true,
		Takeover: core.TakeoverPolicy{
			Mode:                 core.TakeoverModePause,
			PauseDurationMinutes: 30,
		},
	}
	var bus *core.InMemoryEventBus
	p, bus = newOriginTestPipeline(t, st, ch, policy, clock)
	sub := bus.Subscribe(core.EventFilter{})

	msgID, err := p.DeliverTextOutbound(ctx, "t1", "whatsapp", "main", "5511999990001", "bot", "Olá, como posso ajudar?", "run-1", "")
	if err != nil {
		t.Fatalf("DeliverTextOutbound: %v", err)
	}
	<-echoArrivedBeforeReturn

	state, err := st.GetSourceState(ctx, "t1", "whatsapp", "5511999990001")
	if err != nil {
		t.Fatalf("GetSourceState: %v", err)
	}
	if state.Meta.Status == core.SourceStatusHuman {
		t.Fatalf("bot echo arriving before Send() returns must NOT pause source")
	}
	rec, err := st.GetOutboundRecord(ctx, "t1", "whatsapp", msgID)
	if err != nil || rec == nil || rec.Status != core.OutboundStatusSent {
		t.Fatalf("expected outbound record sent, got %+v err=%v", rec, err)
	}

	// Cenário 4: humano envia pelo celular o MESMO texto que o robô enviou, mas com ID não registrado.
	if _, err := p.IngestWithStatus(ctx, core.NormalizedMessage{
		TenantID:  "t1",
		Channel:   "whatsapp",
		SourceKey: "5511999990001",
		MessageID: "phone-msg-1",
		Timestamp: now.Add(1 * time.Minute),
		Direction: core.DirectionInbound,
		Type:      core.MessageTypeText,
		Text:      "Olá, como posso ajudar?",
		FromMe:    true,
	}); err != nil {
		t.Fatalf("Ingest human phone message: %v", err)
	}

	state, _ = st.GetSourceState(ctx, "t1", "whatsapp", "5511999990001")
	if state.Meta.Status != core.SourceStatusHuman {
		t.Fatalf("expected SourceStatusHuman after human_phone message with identical text, got %s", state.Meta.Status)
	}
	if state.Meta.PausedUntil == nil || !state.Meta.PausedUntil.Equal(now.Add(30*time.Minute)) {
		t.Fatalf("expected PausedUntil=+30m, got %v", state.Meta.PausedUntil)
	}

	foundTakeover := false
	for _, ev := range collectEvents(sub) {
		if ev.Type == core.EventHumanTakeover {
			foundTakeover = true
			break
		}
	}
	if !foundTakeover {
		t.Fatalf("expected EventHumanTakeover to be published")
	}

	// Cenário 6: cliente envia mensagem enquanto pausado -> bloqueado no gate.
	gateBlocked := brain.EvaluatePreLLMGates(brain.PreGateInput{
		Policy: policy,
		State:  state,
		Now:    now.Add(10 * time.Minute),
	})
	if gateBlocked.Allowed {
		t.Fatalf("expected pre-LLM gate to block while PausedUntil is in the future")
	}

	// Cenário 7: após expirar PausedUntil -> auto-retomada no gate.
	gateResumed := brain.EvaluatePreLLMGates(brain.PreGateInput{
		Policy: policy,
		State:  state,
		Now:    now.Add(31 * time.Minute),
	})
	if !gateResumed.Allowed || !gateResumed.AutoResumed || gateResumed.UpdateStatus != core.SourceStatusActive {
		t.Fatalf("expected auto-resume after PausedUntil expiry, got %+v", gateResumed)
	}

	// Cenário 8: retomada manual via comando /retomar no canal admin.
	if err := p.HandleAdminInbound(ctx, core.NormalizedMessage{
		TenantID:  "t1",
		Channel:   brain.AdminChannelID,
		SourceKey: brain.AdminOwnerKey,
		MessageID: "adm-retomar-1",
		Timestamp: now.Add(2 * time.Minute),
		Direction: core.DirectionInbound,
		Type:      core.MessageTypeText,
		Text:      "/retomar whatsapp:5511999990001",
	}); err != nil {
		t.Fatalf("HandleAdminInbound /retomar: %v", err)
	}
	state, _ = st.GetSourceState(ctx, "t1", "whatsapp", "5511999990001")
	if state.Meta.Status != core.SourceStatusActive || state.Meta.PausedUntil != nil {
		t.Fatalf("expected active status and nil PausedUntil after /retomar, got status=%s paused=%v", state.Meta.Status, state.Meta.PausedUntil)
	}
}

func TestEVT05_UnknownAndPolicyModesNeverFalsePause(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 12, 16, 0, 0, 0, time.UTC)
	clock := fakes.NewFakeClock(now)

	// Cenário 10 & 11: ReportsOwnMessages=false -> OriginUnknown -> nunca pausa.
	st1 := fakes.NewMemoryStoreWithClock(clock)
	chNoOwn := newDelayedEchoChannel("whatsapp", false, nil)
	p1, _ := newOriginTestPipeline(t, st1, chNoOwn, core.TenantPolicy{
		BotEnabled: true,
		Takeover:   core.TakeoverPolicy{Mode: core.TakeoverModePause, PauseDurationMinutes: 60},
	}, clock)

	if _, err := p1.IngestWithStatus(ctx, core.NormalizedMessage{
		TenantID:  "t1",
		Channel:   "whatsapp",
		SourceKey: "5511999990002",
		MessageID: "unk-1",
		Timestamp: now,
		Direction: core.DirectionInbound,
		Type:      core.MessageTypeText,
		Text:      "oi",
		FromMe:    true,
	}); err != nil {
		t.Fatalf("Ingest unknown origin: %v", err)
	}
	stState1, _ := st1.GetSourceState(ctx, "t1", "whatsapp", "5511999990002")
	if stState1.Meta.Status == core.SourceStatusHuman {
		t.Fatalf("unknown origin must NEVER pause")
	}

	// Cenário 13a: TakeoverModeRecordOnly -> registra evento mas não pausa.
	st2 := fakes.NewMemoryStoreWithClock(clock)
	chOwn := newDelayedEchoChannel("whatsapp", true, nil)
	p2, bus2 := newOriginTestPipeline(t, st2, chOwn, core.TenantPolicy{
		BotEnabled: true,
		Takeover:   core.TakeoverPolicy{Mode: core.TakeoverModeRecordOnly},
	}, clock)
	sub2 := bus2.Subscribe(core.EventFilter{})

	if _, err := p2.IngestWithStatus(ctx, core.NormalizedMessage{
		TenantID:  "t1",
		Channel:   "whatsapp",
		SourceKey: "5511999990003",
		MessageID: "phone-rec-1",
		Timestamp: now,
		Direction: core.DirectionInbound,
		Type:      core.MessageTypeText,
		Text:      "eu assumo",
		FromMe:    true,
	}); err != nil {
		t.Fatalf("Ingest record_only: %v", err)
	}
	stState2, _ := st2.GetSourceState(ctx, "t1", "whatsapp", "5511999990003")
	if stState2.Meta.Status == core.SourceStatusHuman {
		t.Fatalf("record_only takeover must NOT pause")
	}
	hasTakeover := false
	for _, ev := range collectEvents(sub2) {
		if ev.Type == core.EventHumanTakeover {
			hasTakeover = true
		}
	}
	if !hasTakeover {
		t.Fatalf("record_only takeover must still emit EventHumanTakeover")
	}

	// Cenário 13b: TakeoverModeIgnore -> não pausa e não emite EventHumanTakeover.
	st3 := fakes.NewMemoryStoreWithClock(clock)
	p3, bus3 := newOriginTestPipeline(t, st3, chOwn, core.TenantPolicy{
		BotEnabled: true,
		Takeover:   core.TakeoverPolicy{Mode: core.TakeoverModeIgnore},
	}, clock)
	sub3 := bus3.Subscribe(core.EventFilter{})

	if _, err := p3.IngestWithStatus(ctx, core.NormalizedMessage{
		TenantID:  "t1",
		Channel:   "whatsapp",
		SourceKey: "5511999990004",
		MessageID: "phone-ign-1",
		Timestamp: now,
		Direction: core.DirectionInbound,
		Type:      core.MessageTypeText,
		Text:      "ignorar",
		FromMe:    true,
	}); err != nil {
		t.Fatalf("Ingest ignore: %v", err)
	}
	stState3, _ := st3.GetSourceState(ctx, "t1", "whatsapp", "5511999990004")
	if stState3.Meta.Status == core.SourceStatusHuman {
		t.Fatalf("ignore takeover must NOT pause")
	}
	for _, ev := range collectEvents(sub3) {
		if ev.Type == core.EventHumanTakeover {
			t.Fatalf("ignore takeover must NOT emit EventHumanTakeover")
		}
	}
}
