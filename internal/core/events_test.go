package core_test

import (
	"sync"
	"testing"
	"time"

	"whatshelper/internal/core"
)

func TestInMemoryEventBus_FanOutAndFilter(t *testing.T) {
	bus := core.NewInMemoryEventBus(16)

	allSub := bus.Subscribe(core.EventFilter{})
	defer allSub.Cancel()

	simUser1Sub := bus.Subscribe(core.EventFilter{
		TenantID:  "demo",
		Channel:   "simulator",
		SourceKey: "user-1",
	})
	defer simUser1Sub.Cancel()

	now := time.Now().UTC()
	ev1 := core.Event{
		ID:        "ev-1",
		Type:      core.EventMessageReceived,
		TenantID:  "demo",
		Channel:   "simulator",
		SourceKey: "user-1",
		At:        now,
	}
	ev2 := core.Event{
		ID:        "ev-2",
		Type:      core.EventMessageReceived,
		TenantID:  "demo",
		Channel:   "simulator",
		SourceKey: "user-2",
		At:        now,
	}

	bus.Publish(ev1)
	bus.Publish(ev2)

	// allSub deve receber ambos
	got1 := <-allSub.Events()
	got2 := <-allSub.Events()
	if got1.ID != "ev-1" || got2.ID != "ev-2" {
		t.Fatalf("allSub expected ev-1 and ev-2, got %s and %s", got1.ID, got2.ID)
	}

	// simUser1Sub deve receber apenas ev-1
	gotFiltered := <-simUser1Sub.Events()
	if gotFiltered.ID != "ev-1" {
		t.Fatalf("expected ev-1, got %s", gotFiltered.ID)
	}
	select {
	case extra := <-simUser1Sub.Events():
		t.Fatalf("did not expect extra event, got %+v", extra)
	default:
	}
}

func TestInMemoryEventBus_SlowSubscriberDropsWithoutBlocking(t *testing.T) {
	bus := core.NewInMemoryEventBus(2)

	slowSub := bus.Subscribe(core.EventFilter{})
	defer slowSub.Cancel()

	fastSub := bus.Subscribe(core.EventFilter{})
	defer fastSub.Cancel()

	// Publica 5 eventos sem consumir slowSub (buffer = 2)
	for i := 0; i < 5; i++ {
		bus.Publish(core.Event{
			ID:       "ev",
			Type:     core.EventPromptSent,
			TenantID: "demo",
		})
	}

	if slowSub.Dropped() != 3 {
		t.Fatalf("expected 3 dropped events on slowSub, got %d", slowSub.Dropped())
	}

	// fastSub tem os 2 primeiros eventos e 3 drops também porque não leu ainda
	if len(fastSub.Events()) != 2 {
		t.Fatalf("expected 2 buffered events, got %d", len(fastSub.Events()))
	}
}

func TestInMemoryEventBus_CancelAndConcurrency(t *testing.T) {
	bus := core.NewInMemoryEventBus(8)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sub := bus.Subscribe(core.EventFilter{})
			for j := 0; j < 20; j++ {
				bus.Publish(core.Event{Type: core.EventAnalysisStarted, TenantID: "demo"})
			}
			sub.Cancel()
			// Cancelamento idempotente
			sub.Cancel()
		}()
	}
	wg.Wait()
}
