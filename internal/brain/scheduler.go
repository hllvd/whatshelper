package brain

import (
	"context"
	"sync"
	"time"

	"whatshelper/internal/core"
	"whatshelper/internal/store"
)

// TimerHandle abstrai um timer cancelável para testes determinísticos de debounce.
type TimerHandle interface {
	Stop() bool
}

// AfterFunc cria e agenda um timer que executará fn após d.
type AfterFunc func(d time.Duration, fn func()) TimerHandle

type realTimer struct {
	t *time.Timer
}

func (r realTimer) Stop() bool {
	return r.t.Stop()
}

// RealAfterFunc é a implementação padrão baseada em time.AfterFunc.
func RealAfterFunc(d time.Duration, fn func()) TimerHandle {
	return realTimer{t: time.AfterFunc(d, fn)}
}

// BatchHandler processa um lote de mensagens agrupadas para uma mesma fonte.
type BatchHandler func(ctx context.Context, batch []core.NormalizedMessage)

type sourceQueue struct {
	key     string
	mu      sync.Mutex
	execMu  sync.Mutex // garante processamento sequencial por conversa
	pending []core.NormalizedMessage
	timer   TimerHandle
	gen     uint64
	running int
}

// SourceDispatcher implementa debounce de rajadas e execução sequencial por fonte (paralela entre fontes).
type SourceDispatcher struct {
	debounce  time.Duration
	afterFunc AfterFunc
	handler   BatchHandler
	baseCtx   context.Context

	mu     sync.Mutex
	queues map[string]*sourceQueue
	closed bool
	wg     sync.WaitGroup
}

// NewSourceDispatcher cria um despachante com janela de debounce configurável.
func NewSourceDispatcher(ctx context.Context, debounce time.Duration, afterFunc AfterFunc, handler BatchHandler) *SourceDispatcher {
	if ctx == nil {
		ctx = context.Background()
	}
	if afterFunc == nil {
		afterFunc = RealAfterFunc
	}
	return &SourceDispatcher{
		debounce:  debounce,
		afterFunc: afterFunc,
		handler:   handler,
		baseCtx:   ctx,
		queues:    make(map[string]*sourceQueue),
	}
}

// Enqueue agenda a mensagem na fila da sua fonte, aplicando janela de debounce.
func (d *SourceDispatcher) Enqueue(msg core.NormalizedMessage) bool {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return false
	}
	key := store.SourcePK(msg.TenantID, msg.Channel, msg.SourceKey)
	q, ok := d.queues[key]
	if !ok {
		q = &sourceQueue{key: key}
		d.queues[key] = q
	}
	q.mu.Lock()
	d.mu.Unlock()

	if q.timer != nil {
		q.timer.Stop()
		q.timer = nil
	}
	q.pending = append(q.pending, msg)
	q.gen++
	myGen := q.gen

	if d.debounce <= 0 {
		q.mu.Unlock()
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			d.flush(q, myGen)
		}()
		return true
	}

	q.timer = d.afterFunc(d.debounce, func() {
		d.wg.Add(1)
		defer d.wg.Done()
		d.flush(q, myGen)
	})
	q.mu.Unlock()
	return true
}

// ActiveQueues retorna a quantidade de filas de conversa atualmente em memória.
func (d *SourceDispatcher) ActiveQueues() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.queues)
}

func (d *SourceDispatcher) flush(q *sourceQueue, expectedGen uint64) {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	d.mu.Unlock()

	q.mu.Lock()
	if q.gen != expectedGen || len(q.pending) == 0 {
		q.mu.Unlock()
		return
	}
	batch := q.pending
	q.pending = nil
	q.timer = nil
	q.running++
	q.mu.Unlock()

	// Serializa apenas dentro da mesma fonte; fontes diferentes executam em paralelo.
	q.execMu.Lock()
	if d.handler != nil {
		d.handler(d.baseCtx, batch)
	}
	q.execMu.Unlock()

	// Remove fila ociosa para evitar vazamento de memória em conversas encerradas
	d.mu.Lock()
	q.mu.Lock()
	q.running--
	if q.running == 0 && len(q.pending) == 0 && q.timer == nil && d.queues[q.key] == q {
		delete(d.queues, q.key)
	}
	q.mu.Unlock()
	d.mu.Unlock()
}

// Stop cancela timers pendentes e aguarda todos os processamentos em andamento encerrarem.
func (d *SourceDispatcher) Stop() {
	d.mu.Lock()
	d.closed = true
	for _, q := range d.queues {
		q.mu.Lock()
		if q.timer != nil {
			q.timer.Stop()
			q.timer = nil
		}
		q.pending = nil
		q.mu.Unlock()
	}
	d.mu.Unlock()

	d.wg.Wait()
}
