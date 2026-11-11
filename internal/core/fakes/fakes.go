package fakes

import (
	"context"
	"sync"
	"time"

	"whatshelper/internal/core"
)

// FakeLLMHandler é uma função customizada para simular lógica de geração por requisição.
type FakeLLMHandler func(ctx context.Context, req core.LLMRequest) (*core.LLMResponse, error)

// FakeLLM fornece implementação determinística de core.LLM para testes.
type FakeLLM struct {
	mu           sync.Mutex
	calls        []core.LLMRequest
	cannedOutput string
	handler      FakeLLMHandler
}

func NewFakeLLM(cannedOutput string) *FakeLLM {
	return &FakeLLM{
		cannedOutput: cannedOutput,
	}
}

func NewFakeLLMWithHandler(handler FakeLLMHandler) *FakeLLM {
	return &FakeLLM{
		handler: handler,
	}
}

func (f *FakeLLM) Generate(ctx context.Context, req core.LLMRequest) (*core.LLMResponse, error) {
	f.mu.Lock()
	f.calls = append(f.calls, req)
	f.mu.Unlock()

	if f.handler != nil {
		return f.handler(ctx, req)
	}

	return &core.LLMResponse{
		Content:          f.cannedOutput,
		PromptTokens:     10,
		CompletionTokens: 20,
		DurationMs:       15,
	}, nil
}

func (f *FakeLLM) Calls() []core.LLMRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	copied := make([]core.LLMRequest, len(f.calls))
	copy(copied, f.calls)
	return copied
}

func (f *FakeLLM) SetCannedOutput(content string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cannedOutput = content
}

// FakeClock permite controlar o tempo estaticamente em testes.
type FakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func NewFakeClock(initial time.Time) *FakeClock {
	return &FakeClock{now: initial}
}

func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *FakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// MemoryChannel implementa core.Channel em memória com buffers de canal Go.
type MemoryChannel struct {
	mu       sync.Mutex
	id       string
	inbound  chan core.NormalizedMessage
	outbound []core.NormalizedMessage
	typing   map[string]bool
	started  bool
}

func NewMemoryChannel(id string, bufferSize int) *MemoryChannel {
	if bufferSize <= 0 {
		bufferSize = 100
	}
	return &MemoryChannel{
		id:      id,
		inbound: make(chan core.NormalizedMessage, bufferSize),
		typing:  make(map[string]bool),
	}
}

func (c *MemoryChannel) ID() string {
	return c.id
}

func (c *MemoryChannel) Capabilities() core.ChannelCapabilities {
	return core.ChannelCapabilities{
		PlainTextOnly: true,
		Typing:        true,
	}
}

func (c *MemoryChannel) Health() core.ChannelHealth {
	c.mu.Lock()
	defer c.mu.Unlock()
	return core.ChannelHealth{Healthy: c.started}
}

func (c *MemoryChannel) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.started = true
	return nil
}

func (c *MemoryChannel) Stop(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.started = false
	return nil
}

func (c *MemoryChannel) Messages() <-chan core.NormalizedMessage {
	return c.inbound
}

func (c *MemoryChannel) EmitInbound(msg core.NormalizedMessage) {
	c.inbound <- msg
}

func (c *MemoryChannel) Send(ctx context.Context, msg core.NormalizedMessage) error {
	if err := msg.Validate(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.outbound = append(c.outbound, msg)
	return nil
}

func (c *MemoryChannel) SendTyping(ctx context.Context, sourceKey string, typing bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.typing[sourceKey] = typing
	return nil
}

func (c *MemoryChannel) SentMessages() []core.NormalizedMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	copied := make([]core.NormalizedMessage, len(c.outbound))
	copy(copied, c.outbound)
	return copied
}

func (c *MemoryChannel) IsTyping(sourceKey string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.typing[sourceKey]
}

// MemoryAdminNotifier grava alertas em memória para validação em testes.
type MemoryAdminNotifier struct {
	mu     sync.Mutex
	alerts []core.AdminAlert
}

func NewMemoryAdminNotifier() *MemoryAdminNotifier {
	return &MemoryAdminNotifier{}
}

func (n *MemoryAdminNotifier) Notify(ctx context.Context, alert core.AdminAlert) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.alerts = append(n.alerts, alert)
	return nil
}

func (n *MemoryAdminNotifier) Alerts() []core.AdminAlert {
	n.mu.Lock()
	defer n.mu.Unlock()
	copied := make([]core.AdminAlert, len(n.alerts))
	copy(copied, n.alerts)
	return copied
}
