package simulator_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"whatshelper/internal/channels/simulator"
	"whatshelper/internal/core"
	"whatshelper/internal/core/fakes"
)

func TestSimulatorChannel_LifecycleAndMessaging(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 11, 15, 0, 0, 0, time.UTC)
	ch := simulator.New(fakes.NewFakeClock(now), 8)

	if ch.ID() != "simulator" {
		t.Fatalf("expected id simulator, got %s", ch.ID())
	}
	if !ch.Capabilities().PlainTextOnly {
		t.Fatalf("expected PlainTextOnly=true")
	}
	if ch.Health().Healthy {
		t.Fatalf("expected healthy=false before Start")
	}

	// Antes do Start deve rejeitar envio
	if _, err := ch.SubmitInbound(ctx, "demo", "s1", "oi"); !errors.Is(err, simulator.ErrChannelStopped) {
		t.Fatalf("expected ErrChannelStopped, got %v", err)
	}

	if err := ch.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if !ch.Health().Healthy {
		t.Fatalf("expected healthy=true after Start")
	}

	msg, err := ch.SubmitInbound(ctx, "demo", "sess-1", "Olá, quero informações")
	if err != nil {
		t.Fatalf("SubmitInbound failed: %v", err)
	}
	if msg.Channel != "simulator" || msg.SourceKey != "sess-1" || msg.Direction != core.DirectionInbound {
		t.Fatalf("unexpected normalized message: %+v", msg)
	}

	received := <-ch.Messages()
	if received.MessageID != msg.MessageID {
		t.Fatalf("expected %s, got %s", msg.MessageID, received.MessageID)
	}

	// Outbound Send
	out := core.NormalizedMessage{
		TenantID:  "demo",
		Channel:   "simulator",
		SourceKey: "sess-1",
		MessageID: "out-1",
		Timestamp: now,
		Direction: core.DirectionOutbound,
		Type:      core.MessageTypeText,
		Text:      "Claro! Como posso ajudar?",
	}
	if err := ch.Send(ctx, out); err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	sent := ch.SentMessages("sess-1")
	if len(sent) != 1 || sent[0].Text != out.Text {
		t.Fatalf("unexpected sent messages: %+v", sent)
	}

	if err := ch.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if ch.Health().Healthy {
		t.Fatalf("expected healthy=false after Stop")
	}
}
