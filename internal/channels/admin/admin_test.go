package admin_test

import (
	"context"
	"testing"
	"time"

	"whatshelper/internal/channels/admin"
	"whatshelper/internal/core"
	"whatshelper/internal/core/fakes"
)

func TestAdminChannel_LifecycleAndMessaging(t *testing.T) {
	ctx := context.Background()
	clock := fakes.NewFakeClock(time.Date(2026, 11, 11, 14, 0, 0, 0, time.UTC))
	ch := admin.New(clock, 8)

	if ch.ID() != "admin" || !ch.Capabilities().PlainTextOnly {
		t.Fatalf("unexpected admin channel metadata")
	}
	if ch.Health().Healthy {
		t.Fatalf("expected unhealthy before Start")
	}

	if err := ch.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if !ch.Health().Healthy {
		t.Fatalf("expected healthy after Start")
	}

	msg, err := ch.SubmitInbound(ctx, "demo", "", "/aprovar")
	if err != nil {
		t.Fatalf("SubmitInbound failed: %v", err)
	}
	if msg.Channel != "admin" || msg.SourceKey != admin.DefaultAdminSourceKey {
		t.Fatalf("unexpected normalized message: %+v", msg)
	}

	select {
	case got := <-ch.Messages():
		if got.MessageID != msg.MessageID {
			t.Fatalf("unexpected message received from channel: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout reading inbound message")
	}

	out := core.NormalizedMessage{
		TenantID: "demo", Channel: "admin", SourceKey: "owner",
		MessageID: "adm-out-1", Timestamp: clock.Now(), Direction: core.DirectionOutbound,
		Type: core.MessageTypeText, Text: "Escalonamento pendente",
	}
	if err := ch.Send(ctx, out); err != nil {
		t.Fatal(err)
	}
	if len(ch.SentMessages()) != 1 || ch.SentMessages()[0].Text != "Escalonamento pendente" {
		t.Fatalf("unexpected sent messages: %+v", ch.SentMessages())
	}
}
