package brain_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"whatshelper/internal/brain"
	"whatshelper/internal/core"
)

type fakeTimerEntry struct {
	fn      func()
	stopped atomic.Bool
}

type fakeTimerHandle struct {
	entry *fakeTimerEntry
}

func (h *fakeTimerHandle) Stop() bool {
	return !h.entry.stopped.Swap(true)
}

type fakeScheduler struct {
	mu      sync.Mutex
	entries []*fakeTimerEntry
}

func (f *fakeScheduler) AfterFunc(d time.Duration, fn func()) brain.TimerHandle {
	f.mu.Lock()
	defer f.mu.Unlock()
	entry := &fakeTimerEntry{fn: fn}
	f.entries = append(f.entries, entry)
	return &fakeTimerHandle{entry: entry}
}

func (f *fakeScheduler) FireAllActive() {
	f.mu.Lock()
	entries := append([]*fakeTimerEntry(nil), f.entries...)
	f.entries = nil
	f.mu.Unlock()

	for _, e := range entries {
		if !e.stopped.Load() {
			e.fn()
		}
	}
}

func TestSourceDispatcher_DebounceBurst(t *testing.T) {
	fs := &fakeScheduler{}
	var mu sync.Mutex
	var batches [][]core.NormalizedMessage

	d := brain.NewSourceDispatcher(context.Background(), 1500*time.Millisecond, fs.AfterFunc, func(ctx context.Context, batch []core.NormalizedMessage) {
		mu.Lock()
		batches = append(batches, batch)
		mu.Unlock()
	})
	defer d.Stop()

	now := time.Now().UTC()
	for i, text := range []string{"Oi", "tudo bem?", "quero ajuda"} {
		d.Enqueue(core.NormalizedMessage{
			TenantID:  "demo",
			Channel:   "simulator",
			SourceKey: "user-1",
			MessageID: string(rune('a' + i)),
			Timestamp: now.Add(time.Duration(i) * time.Millisecond),
			Direction: core.DirectionInbound,
			Type:      core.MessageTypeText,
			Text:      text,
		})
	}

	// Dispara timers ativos (apenas o último não foi cancelado)
	fs.FireAllActive()

	mu.Lock()
	defer mu.Unlock()
	if len(batches) != 1 {
		t.Fatalf("expected 1 debounced batch, got %d", len(batches))
	}
	if len(batches[0]) != 3 {
		t.Fatalf("expected 3 messages in batch, got %d", len(batches[0]))
	}
}

func TestSourceDispatcher_ParallelAcrossSourcesAndSequentialPerSource(t *testing.T) {
	s1Entered := make(chan struct{})
	s2Entered := make(chan struct{})
	m3Started := make(chan struct{})
	releaseM3 := make(chan struct{})
	var s1Once, s2Once, m3Once sync.Once

	var source1Concurrent atomic.Int32
	var source1MaxConcurrent atomic.Int32
	var wg sync.WaitGroup
	wg.Add(2)

	d := brain.NewSourceDispatcher(context.Background(), 0, nil, func(ctx context.Context, batch []core.NormalizedMessage) {
		defer func() {
			for range batch {
				wg.Done()
			}
		}()
		src := batch[0].SourceKey
		msgID := batch[0].MessageID
		switch {
		case src == "s1" && msgID == "m1":
			s1Once.Do(func() { close(s1Entered) })
			// Só avança se s2 estiver executando em paralelo
			select {
			case <-s2Entered:
			case <-time.After(2 * time.Second):
				t.Errorf("timeout esperando s2 executar em paralelo com s1")
			}
		case src == "s2":
			s2Once.Do(func() { close(s2Entered) })
			select {
			case <-s1Entered:
			case <-time.After(2 * time.Second):
				t.Errorf("timeout esperando s1 executar em paralelo com s2")
			}
		case src == "s1":
			sc := source1Concurrent.Add(1)
			for {
				prev := source1MaxConcurrent.Load()
				if sc <= prev || source1MaxConcurrent.CompareAndSwap(prev, sc) {
					break
				}
			}
			if msgID == "m3" {
				m3Once.Do(func() { close(m3Started) })
				<-releaseM3
			}
			source1Concurrent.Add(-1)
		}
	})
	defer d.Stop()

	now := time.Now().UTC()
	d.Enqueue(core.NormalizedMessage{TenantID: "demo", Channel: "sim", SourceKey: "s1", MessageID: "m1", Timestamp: now})
	d.Enqueue(core.NormalizedMessage{TenantID: "demo", Channel: "sim", SourceKey: "s2", MessageID: "m2", Timestamp: now})
	wg.Wait()

	// Agora dispara m3 em s1, aguarda m3 estar executando, dispara m4 e libera m3 -> prova execução sequencial
	wg.Add(2)
	d.Enqueue(core.NormalizedMessage{TenantID: "demo", Channel: "sim", SourceKey: "s1", MessageID: "m3", Timestamp: now})
	<-m3Started
	d.Enqueue(core.NormalizedMessage{TenantID: "demo", Channel: "sim", SourceKey: "s1", MessageID: "m4", Timestamp: now})
	close(releaseM3)
	wg.Wait()

	if source1MaxConcurrent.Load() != 1 {
		t.Fatalf("expected sequential execution per source, got maxConcurrent=%d", source1MaxConcurrent.Load())
	}
}
