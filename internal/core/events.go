package core

import (
	"sync"
	"sync/atomic"
	"time"
)

// EventType identifica o tipo de evento emitido pelo pipeline do cérebro.
type EventType string

const (
	EventMessageReceived   EventType = "message.received"
	EventMessageDuplicate  EventType = "message.duplicate"
	EventAnalysisStarted   EventType = "analysis.started"
	EventPromptSent        EventType = "prompt.sent"
	EventLLMResponded      EventType = "llm.responded"
	EventAnalysisCompleted EventType = "analysis.completed"
	EventFactsDiff         EventType = "facts.diff"
	EventReplyGenerated    EventType = "reply.generated"
	EventReplySent         EventType = "reply.sent"
	EventPipelineFailed    EventType = "pipeline.failed"

	// Reservados / M2-M3:
	EventEscalated           EventType = "escalation.triggered"
	EventLearned             EventType = "learning.recorded"
	EventLearningProposed    EventType = "learning.proposed"
	EventAdminMessage        EventType = "admin.message"
	EventSourceStatusUpdated EventType = "source.status_updated"
	EventRunSkipped          EventType = "run.skipped"
	EventTaskCreated         EventType = "task.created"
	EventApprovalPending     EventType = "approval.pending"
	EventNudgeSent           EventType = "nudge.sent"
)

// Event representa um acontecimento observável no pipeline cognitivo.
type Event struct {
	ID        string         `json:"id"`
	Type      EventType      `json:"type"`
	TenantID  string         `json:"tenant_id"`
	Channel   string         `json:"channel"`
	SourceKey string         `json:"source_key"`
	MessageID string         `json:"message_id,omitempty"`
	RunID     string         `json:"run_id,omitempty"`
	At        time.Time      `json:"at"`
	Payload   map[string]any `json:"payload,omitempty"`
}

// EventFilter restringe quais eventos um assinante recebe. Campos vazios aceitam qualquer valor.
type EventFilter struct {
	TenantID  string
	Channel   string
	SourceKey string
}

// Matches retorna true se o evento satisfaz os critérios do filtro.
func (f EventFilter) Matches(e Event) bool {
	if f.TenantID != "" && f.TenantID != e.TenantID {
		return false
	}
	if f.Channel != "" && f.Channel != e.Channel {
		return false
	}
	if f.SourceKey != "" && f.SourceKey != e.SourceKey {
		return false
	}
	return true
}

// Subscription representa uma assinatura ativa no barramento de eventos.
type Subscription interface {
	Events() <-chan Event
	Dropped() uint64
	Cancel()
}

// EventBus define o barramento de eventos do pipeline para alimentação do visualizador e observabilidade.
type EventBus interface {
	Publish(event Event)
	Subscribe(filter EventFilter) Subscription
}

// InMemoryEventBus implementa EventBus de forma thread-safe e não-bloqueante.
type InMemoryEventBus struct {
	mu         sync.RWMutex
	nextID     uint64
	bufferSize int
	subs       map[uint64]*inMemorySub
}

type inMemorySub struct {
	id      uint64
	filter  EventFilter
	mu      sync.RWMutex
	ch      chan Event
	dropped atomic.Uint64
	closed  bool
	cancel  func()
}

func (s *inMemorySub) Events() <-chan Event {
	return s.ch
}

func (s *inMemorySub) Dropped() uint64 {
	return s.dropped.Load()
}

func (s *inMemorySub) Cancel() {
	s.cancel()
}

func (s *inMemorySub) trySend(event Event) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return
	}
	select {
	case s.ch <- event:
	default:
		s.dropped.Add(1)
	}
}

func (s *inMemorySub) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.ch)
	}
}

// NewInMemoryEventBus cria um barramento de eventos em memória com tamanho de buffer por assinante.
func NewInMemoryEventBus(bufferSize int) *InMemoryEventBus {
	if bufferSize <= 0 {
		bufferSize = 64
	}
	return &InMemoryEventBus{
		bufferSize: bufferSize,
		subs:       make(map[uint64]*inMemorySub),
	}
}

// Publish envia o evento para todos os assinantes compatíveis sem bloquear em consumidores lentos.
func (b *InMemoryEventBus) Publish(event Event) {
	b.mu.RLock()
	targets := make([]*inMemorySub, 0, len(b.subs))
	for _, sub := range b.subs {
		if sub.filter.Matches(event) {
			targets = append(targets, sub)
		}
	}
	b.mu.RUnlock()

	for _, sub := range targets {
		sub.trySend(event)
	}
}

// Subscribe registra um novo assinante com o filtro especificado.
func (b *InMemoryEventBus) Subscribe(filter EventFilter) Subscription {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.nextID++
	id := b.nextID

	sub := &inMemorySub{
		id:     id,
		filter: filter,
		ch:     make(chan Event, b.bufferSize),
		cancel: func() {
			b.unsubscribe(id)
		},
	}
	b.subs[id] = sub
	return sub
}

func (b *InMemoryEventBus) unsubscribe(id uint64) {
	b.mu.Lock()
	sub, ok := b.subs[id]
	if ok {
		delete(b.subs, id)
	}
	b.mu.Unlock()

	if ok {
		sub.close()
	}
}
