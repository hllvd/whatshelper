package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"whatshelper/internal/core"
)

type eventBuffer struct {
	mu     sync.RWMutex
	max    int
	events []core.Event
}

func newEventBuffer(max int) *eventBuffer {
	if max <= 0 {
		max = 200
	}
	return &eventBuffer{
		max:    max,
		events: make([]core.Event, 0, max),
	}
}

func (b *eventBuffer) add(ev core.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.events) >= b.max {
		copy(b.events, b.events[1:])
		b.events[len(b.events)-1] = ev
		return
	}
	b.events = append(b.events, ev)
}

func (b *eventBuffer) since(lastID string, filter core.EventFilter) []core.Event {
	if lastID == "" {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()

	foundIdx := -1
	for i, ev := range b.events {
		if ev.ID == lastID {
			foundIdx = i
			break
		}
	}
	if foundIdx == -1 || foundIdx+1 >= len(b.events) {
		return nil
	}

	var out []core.Event
	for _, ev := range b.events[foundIdx+1:] {
		if filter.Matches(ev) {
			out = append(out, ev)
		}
	}
	return out
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if s.deps.Bus == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "event bus não configurado")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "streaming não suportado")
		return
	}

	// Remove o WriteTimeout global apenas para esta conexão SSE de longa duração
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})

	q := r.URL.Query()
	filter := core.EventFilter{
		TenantID:  q.Get("tenant"),
		Channel:   q.Get("channel"),
		SourceKey: q.Get("source"),
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	sub := s.deps.Bus.Subscribe(filter)
	defer sub.Cancel()

	// Replay best-effort para reconexão com Last-Event-ID
	lastID := r.Header.Get("Last-Event-ID")
	if lastID == "" {
		lastID = q.Get("last_event_id")
	}
	if s.eventRing != nil && lastID != "" {
		for _, missed := range s.eventRing.since(lastID, filter) {
			if err := writeSSEEvent(w, missed); err != nil {
				return
			}
		}
	}
	_, _ = fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.stopCh:
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprintf(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case ev, open := <-sub.Events():
			if !open {
				return
			}
			if err := writeSSEEvent(w, ev); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeSSEEvent(w http.ResponseWriter, ev core.Event) error {
	payload, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if ev.ID != "" {
		if _, err := fmt.Fprintf(w, "id: %s\n", ev.ID); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, payload); err != nil {
		return err
	}
	return nil
}
