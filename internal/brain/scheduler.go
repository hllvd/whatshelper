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

// TimingResolver resolve os tempos de debounce e espera máxima de digitação para um tenant.
type TimingResolver func(tenantID string) (debounce, typingMaxWait time.Duration)

type sourceQueue struct {
	key             string
	mu              sync.Mutex
	execMu          sync.Mutex // garante processamento sequencial por conversa
	pending         []core.NormalizedMessage
	timer           TimerHandle
	gen             uint64
	inboundSeq      uint64
	running         int
	typing          bool
	waitingTyping   bool
	cycleStart      time.Time
	typingMaxWait   time.Duration
	incompleteGrace time.Duration
	lastFlushReason string
}

// SourceDispatcher implementa debounce de rajadas, espera por digitação e execução sequencial por fonte.
type SourceDispatcher struct {
	debounce        time.Duration
	typingMaxWait   time.Duration
	incompleteGrace time.Duration
	afterFunc       AfterFunc
	nowFunc         func() time.Time
	timingResolver  TimingResolver
	handler         BatchHandler
	baseCtx         context.Context

	mu     sync.Mutex
	queues map[string]*sourceQueue
	closed bool
	wg     sync.WaitGroup
}

// DispatcherConfig configura o SourceDispatcher com suporte a espera de digitação e tempos por pack.
type DispatcherConfig struct {
	Debounce        time.Duration
	TypingMaxWait   time.Duration
	IncompleteGrace time.Duration
	AfterFunc       AfterFunc
	NowFunc         func() time.Time
	TimingResolver  TimingResolver
	Handler         BatchHandler
}

// NewSourceDispatcher cria um despachante com janela de debounce configurável.
func NewSourceDispatcher(ctx context.Context, debounce time.Duration, afterFunc AfterFunc, handler BatchHandler) *SourceDispatcher {
	return NewSourceDispatcherWithConfig(ctx, DispatcherConfig{
		Debounce:  debounce,
		AfterFunc: afterFunc,
		Handler:   handler,
	})
}

// NewSourceDispatcherWithConfig cria um despachante com suporte completo a debounce, typing e resolução por pack.
func NewSourceDispatcherWithConfig(ctx context.Context, cfg DispatcherConfig) *SourceDispatcher {
	if ctx == nil {
		ctx = context.Background()
	}
	if cfg.AfterFunc == nil {
		cfg.AfterFunc = RealAfterFunc
	}
	if cfg.NowFunc == nil {
		cfg.NowFunc = time.Now
	}
	return &SourceDispatcher{
		debounce:        cfg.Debounce,
		typingMaxWait:   cfg.TypingMaxWait,
		incompleteGrace: cfg.IncompleteGrace,
		afterFunc:       cfg.AfterFunc,
		nowFunc:         cfg.NowFunc,
		timingResolver:  cfg.TimingResolver,
		handler:         cfg.Handler,
		baseCtx:         ctx,
		queues:          make(map[string]*sourceQueue),
	}
}

func (d *SourceDispatcher) resolveTiming(tenantID string) (debounce, typingMaxWait time.Duration) {
	if d.timingResolver != nil {
		return d.timingResolver(tenantID)
	}
	return d.debounce, d.typingMaxWait
}

// Enqueue agenda a mensagem na fila da sua fonte, aplicando a decisão pura NextDebounceDecision (WIN-03).
func (d *SourceDispatcher) Enqueue(msg core.NormalizedMessage) bool {
	debounce, typingMaxWait := d.resolveTiming(msg.TenantID)

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

	now := d.nowFunc()
	dec := NextDebounceDecision(
		DebounceState{
			PendingCount:  len(q.pending),
			CycleStart:    q.cycleStart,
			Typing:        q.typing,
			WaitingTyping: q.waitingTyping,
		},
		DebounceEvent{
			Type: DebounceEventMessage,
			Text: msg.Text,
		},
		AdaptiveDebounceConfig{
			BaseDebounce:    debounce,
			IncompleteGrace: d.incompleteGrace,
			MaxWait:         typingMaxWait,
			Lexicon:         DefaultPTBRLexicon(),
		},
		now,
	)

	q.pending = append(q.pending, msg)
	q.inboundSeq++
	q.gen++
	q.waitingTyping = dec.NextState.WaitingTyping
	q.cycleStart = dec.NextState.CycleStart
	q.typingMaxWait = typingMaxWait
	q.incompleteGrace = d.incompleteGrace
	q.lastFlushReason = dec.Reason
	myGen := q.gen

	if dec.Flush {
		q.mu.Unlock()
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			d.flush(q, myGen)
		}()
		return true
	}

	q.timer = d.afterFunc(dec.WaitDuration, func() {
		d.onDebounceExpired(q, myGen)
	})
	q.mu.Unlock()
	return true
}

// CurrentSeq retorna a sequência monotônica de entrada para detecção de resposta defasada (WIN-04).
func (d *SourceDispatcher) CurrentSeq(tenantID, channel, sourceKey string) uint64 {
	d.mu.Lock()
	q, ok := d.queues[store.SourcePK(tenantID, channel, sourceKey)]
	if !ok {
		d.mu.Unlock()
		return 0
	}
	q.mu.Lock()
	d.mu.Unlock()
	seq := q.inboundSeq
	q.mu.Unlock()
	return seq
}

// TakePending drena atomicamente mensagens novas que chegaram durante o processamento de um lote em andamento (WIN-04).
func (d *SourceDispatcher) TakePending(tenantID, channel, sourceKey string) ([]core.NormalizedMessage, uint64) {
	d.mu.Lock()
	q, ok := d.queues[store.SourcePK(tenantID, channel, sourceKey)]
	if !ok {
		d.mu.Unlock()
		return nil, 0
	}
	q.mu.Lock()
	d.mu.Unlock()

	if q.timer != nil {
		q.timer.Stop()
		q.timer = nil
	}
	extra := q.pending
	q.pending = nil
	q.waitingTyping = false
	q.cycleStart = time.Time{}
	q.gen++
	seq := q.inboundSeq
	q.mu.Unlock()
	return extra, seq
}

// ApplyMutation aplica de forma atômica uma edição ou exclusão sobre o lote pendente antes do flush (WIN-07).
func (d *SourceDispatcher) ApplyMutation(tenantID, channel, sourceKey string, evt MessageMutationEvent, policy EditPolicy) EditApplicationResult {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return EditApplicationResult{}
	}
	key := store.SourcePK(tenantID, channel, sourceKey)
	q, ok := d.queues[key]
	if !ok {
		d.mu.Unlock()
		return ApplyMessageEdit(nil, evt, policy)
	}
	q.mu.Lock()
	d.mu.Unlock()

	res := ApplyMessageEdit(q.pending, evt, policy)
	if res.AppliedInBatch {
		q.pending = res.UpdatedBatch
		if len(q.pending) == 0 && q.timer != nil {
			q.timer.Stop()
			q.timer = nil
			q.waitingTyping = false
			q.cycleStart = time.Time{}
		}
	}
	q.mu.Unlock()
	return res
}

// SetTyping atualiza o estado de digitação do cliente para uma conversa usando NextDebounceDecision.
func (d *SourceDispatcher) SetTyping(tenantID, channel, sourceKey string, typing bool) {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	key := store.SourcePK(tenantID, channel, sourceKey)
	q, ok := d.queues[key]
	if !ok {
		if !typing {
			d.mu.Unlock()
			return
		}
		q = &sourceQueue{key: key}
		d.queues[key] = q
	}
	q.mu.Lock()
	d.mu.Unlock()

	dec := NextDebounceDecision(
		DebounceState{
			PendingCount:  len(q.pending),
			CycleStart:    q.cycleStart,
			Typing:        q.typing,
			WaitingTyping: q.waitingTyping,
		},
		DebounceEvent{
			Type:   DebounceEventTypingChanged,
			Typing: typing,
		},
		AdaptiveDebounceConfig{
			MaxWait: q.typingMaxWait,
		},
		d.nowFunc(),
	)

	q.typing = dec.NextState.Typing
	q.waitingTyping = dec.NextState.WaitingTyping
	if dec.Flush && len(q.pending) > 0 {
		if q.timer != nil {
			q.timer.Stop()
			q.timer = nil
		}
		q.lastFlushReason = dec.Reason
		myGen := q.gen
		q.mu.Unlock()
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			d.flush(q, myGen)
		}()
		return
	}
	if !typing && q.running == 0 && len(q.pending) == 0 && q.timer == nil {
		q.mu.Unlock()
		d.cleanupIfIdle(q)
		return
	}
	q.mu.Unlock()
}

func (d *SourceDispatcher) onDebounceExpired(q *sourceQueue, expectedGen uint64) {
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
	dec := NextDebounceDecision(
		DebounceState{
			PendingCount:  len(q.pending),
			CycleStart:    q.cycleStart,
			Typing:        q.typing,
			WaitingTyping: q.waitingTyping,
		},
		DebounceEvent{
			Type: DebounceEventTimerExpired,
		},
		AdaptiveDebounceConfig{
			MaxWait: q.typingMaxWait,
		},
		d.nowFunc(),
	)
	q.waitingTyping = dec.NextState.WaitingTyping
	q.lastFlushReason = dec.Reason
	if !dec.Flush {
		q.timer = d.afterFunc(dec.WaitDuration, func() {
			d.wg.Add(1)
			defer d.wg.Done()
			d.flush(q, expectedGen)
		})
		q.mu.Unlock()
		return
	}
	q.mu.Unlock()

	d.wg.Add(1)
	defer d.wg.Done()
	d.flush(q, expectedGen)
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
	q.waitingTyping = false
	q.cycleStart = time.Time{}
	q.running++
	q.mu.Unlock()

	// Serializa apenas dentro da mesma fonte; fontes diferentes executam em paralelo.
	q.execMu.Lock()
	if d.handler != nil {
		d.handler(d.baseCtx, batch)
	}
	q.execMu.Unlock()

	q.mu.Lock()
	q.running--
	q.mu.Unlock()

	d.cleanupIfIdle(q)
}

func (d *SourceDispatcher) cleanupIfIdle(q *sourceQueue) {
	d.mu.Lock()
	q.mu.Lock()
	if q.running == 0 && len(q.pending) == 0 && q.timer == nil && !q.typing && d.queues[q.key] == q {
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
