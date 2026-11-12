package humanizer_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"whatshelper/internal/channels/humanizer"
	"whatshelper/internal/core"
	"whatshelper/internal/core/fakes"
)

type recordedOutbound struct {
	mu     sync.Mutex
	events []string
}

func (r *recordedOutbound) record(ev string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *recordedOutbound) Send(ctx context.Context, msg core.NormalizedMessage) error {
	r.record("send:" + msg.SourceKey + ":" + msg.MessageID)
	return nil
}

func (r *recordedOutbound) SendTyping(ctx context.Context, sourceKey string, typing bool) error {
	r.record(fmt.Sprintf("typing:%s:%v", sourceKey, typing))
	return nil
}

func (r *recordedOutbound) Health() core.ChannelHealth {
	return core.ChannelHealth{Healthy: true}
}

func TestComputeTypingDelay(t *testing.T) {
	cfg := humanizer.Config{
		MinTypingDelay: 500 * time.Millisecond,
		MaxTypingDelay: 2500 * time.Millisecond,
		MsPerChar:      50 * time.Millisecond,
	}
	if d := humanizer.ComputeTypingDelay("oi", cfg); d != 500*time.Millisecond {
		t.Fatalf("expected min delay 500ms, got %v", d)
	}
	if d := humanizer.ComputeTypingDelay("12345678901234567890", cfg); d != 1*time.Second {
		t.Fatalf("expected 1s delay for 20 chars, got %v", d)
	}
	long := string(make([]byte, 200))
	for i := range long {
		_ = i
	}
	if d := humanizer.ComputeTypingDelay("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", cfg); d != 2500*time.Millisecond {
		t.Fatalf("expected capped 2.5s delay, got %v", d)
	}
}

func TestHumanizedHandler_OrderAntiBurstAndPerSourceQueue(t *testing.T) {
	clock := fakes.NewFakeClock(time.Date(2026, 11, 11, 16, 0, 0, 0, time.UTC))
	rec := &recordedOutbound{}

	var sleepsMu sync.Mutex
	var sleeps []time.Duration
	h := humanizer.New(rec, humanizer.Config{
		MinTypingDelay:       400 * time.Millisecond,
		MaxTypingDelay:       2 * time.Second,
		MsPerChar:            40 * time.Millisecond,
		MinIntervalPerSource: 1 * time.Second,
		Clock:                clock,
		Sleep: func(ctx context.Context, d time.Duration) error {
			sleepsMu.Lock()
			sleeps = append(sleeps, d)
			sleepsMu.Unlock()
			clock.Advance(d)
			return nil
		},
	})

	ctx := context.Background()
	msg1 := core.NormalizedMessage{
		TenantID: "demo", Channel: "whatsapp", SourceKey: "5511999998888",
		MessageID: "m1", Timestamp: clock.Now(), Direction: core.DirectionOutbound, Type: core.MessageTypeText,
		Text: "Olá, tudo bem?", // 14 chars * 40ms = 560ms
	}
	msg2 := core.NormalizedMessage{
		TenantID: "demo", Channel: "whatsapp", SourceKey: "5511999998888",
		MessageID: "m2", Timestamp: clock.Now(), Direction: core.DirectionOutbound, Type: core.MessageTypeText,
		Text: "Oi", // < min -> 400ms
	}

	if err := h.Send(ctx, msg1); err != nil {
		t.Fatal(err)
	}
	if err := h.Send(ctx, msg2); err != nil {
		t.Fatal(err)
	}

	// Esperado: msg1 -> typing true, sleep(560ms), typing false, send m1;
	// msg2 -> sleep anti-burst (1s), typing true, sleep(400ms), typing false, send m2
	wantEvents := []string{
		"typing:5511999998888:true",
		"typing:5511999998888:false",
		"send:5511999998888:m1",
		"typing:5511999998888:true",
		"typing:5511999998888:false",
		"send:5511999998888:m2",
	}
	if len(rec.events) != len(wantEvents) {
		t.Fatalf("expected %d events, got %d: %v", len(wantEvents), len(rec.events), rec.events)
	}
	for i := range wantEvents {
		if rec.events[i] != wantEvents[i] {
			t.Fatalf("event[%d]: expected %q, got %q", i, wantEvents[i], rec.events[i])
		}
	}

	wantSleeps := []time.Duration{560 * time.Millisecond, 1 * time.Second, 400 * time.Millisecond}
	if len(sleeps) != len(wantSleeps) {
		t.Fatalf("expected %d sleeps, got %v", len(wantSleeps), sleeps)
	}
	for i := range wantSleeps {
		if sleeps[i] != wantSleeps[i] {
			t.Fatalf("sleep[%d]: expected %v, got %v", i, wantSleeps[i], sleeps[i])
		}
	}
}
