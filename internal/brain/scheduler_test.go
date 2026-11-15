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

	// Aguarda limpeza da fila ociosa após execução
	deadline := time.Now().Add(time.Second)
	for d.ActiveQueues() > 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if d.ActiveQueues() != 0 {
		t.Fatalf("expected 0 active queues after idle cleanup, got %d", d.ActiveQueues())
	}
}

func TestSourceDispatcher_TypingStandbyAndMaxWait(t *testing.T) {
	fs := &fakeScheduler{}
	currentTime := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	var batches [][]core.NormalizedMessage

	d := brain.NewSourceDispatcherWithConfig(context.Background(), brain.DispatcherConfig{
		Debounce:      8 * time.Second,
		TypingMaxWait: 16 * time.Second,
		AfterFunc:     fs.AfterFunc,
		NowFunc:       func() time.Time { return currentTime },
		Handler: func(ctx context.Context, batch []core.NormalizedMessage) {
			mu.Lock()
			batches = append(batches, batch)
			mu.Unlock()
		},
	})
	defer d.Stop()

	d.Enqueue(core.NormalizedMessage{TenantID: "demo", Channel: "whatsapp", SourceKey: "5511999", MessageID: "m1", Timestamp: currentTime})
	d.SetTyping("demo", "whatsapp", "5511999", true)

	// 8s debounce vence, mas cliente ainda está digitando -> não processa ainda
	currentTime = currentTime.Add(8 * time.Second)
	fs.FireAllActive()

	mu.Lock()
	if len(batches) != 0 {
		mu.Unlock()
		t.Fatalf("expected 0 batches while typing, got %d", len(batches))
	}
	mu.Unlock()

	// Aos 16s (cap máximo), dispara mesmo com typing=true
	currentTime = currentTime.Add(8 * time.Second)
	fs.FireAllActive()

	mu.Lock()
	defer mu.Unlock()
	if len(batches) != 1 || len(batches[0]) != 1 {
		t.Fatalf("expected 1 batch after 16s max wait, got %+v", batches)
	}
}

func TestSourceDispatcher_TypingStopFlushesImmediately(t *testing.T) {
	fs := &fakeScheduler{}
	currentTime := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	flushed := make(chan int, 1)

	d := brain.NewSourceDispatcherWithConfig(context.Background(), brain.DispatcherConfig{
		Debounce:      8 * time.Second,
		TypingMaxWait: 16 * time.Second,
		AfterFunc:     fs.AfterFunc,
		NowFunc:       func() time.Time { return currentTime },
		Handler: func(ctx context.Context, batch []core.NormalizedMessage) {
			flushed <- len(batch)
		},
	})
	defer d.Stop()

	d.Enqueue(core.NormalizedMessage{TenantID: "demo", Channel: "simulator", SourceKey: "u1", MessageID: "m1", Timestamp: currentTime})
	d.SetTyping("demo", "simulator", "u1", true)

	// 8s debounce vence em standby
	currentTime = currentTime.Add(8 * time.Second)
	fs.FireAllActive()

	select {
	case <-flushed:
		t.Fatal("should not flush while typing is true")
	default:
	}

	// Cliente para de digitar aos 10s -> libera imediatamente
	currentTime = currentTime.Add(2 * time.Second)
	d.SetTyping("demo", "simulator", "u1", false)

	select {
	case n := <-flushed:
		if n != 1 {
			t.Fatalf("expected batch size 1, got %d", n)
		}
	case <-time.After(time.Second):
		t.Fatal("expected immediate flush when typing stopped after debounce")
	}
}
