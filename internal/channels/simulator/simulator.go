package simulator

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"whatshelper/internal/core"
)

var (
	ErrChannelStopped = errors.New("simulador: canal não iniciado")
	ErrEmptySource    = errors.New("simulador: source_key é obrigatório")
	ErrEmptyText      = errors.New("simulador: texto da mensagem é obrigatório")
)

// Channel implementa core.Channel em processo para o simulador web de desenvolvimento.
type Channel struct {
	clock    core.Clock
	inbound  chan core.NormalizedMessage
	mu       sync.RWMutex
	started  bool
	typing   map[string]bool
	outbound map[string][]core.NormalizedMessage
}

// New cria uma nova instância do canal simulador.
func New(clock core.Clock, bufferSize int) *Channel {
	if clock == nil {
		clock = core.SystemClock{}
	}
	if bufferSize <= 0 {
		bufferSize = 128
	}
	return &Channel{
		clock:    clock,
		inbound:  make(chan core.NormalizedMessage, bufferSize),
		typing:   make(map[string]bool),
		outbound: make(map[string][]core.NormalizedMessage),
	}
}

func (c *Channel) ID() string {
	return "simulator"
}

func (c *Channel) Capabilities() core.ChannelCapabilities {
	return core.ChannelCapabilities{
		PlainTextOnly: true,
		Media:         false,
		Typing:        true,
		Reactions:     false,
	}
}

func (c *Channel) Health() core.ChannelHealth {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.started {
		return core.ChannelHealth{Healthy: false, Detail: "parado"}
	}
	return core.ChannelHealth{Healthy: true, Detail: "ativo"}
}

func (c *Channel) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.started = true
	return nil
}

func (c *Channel) Stop(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.started = false
	return nil
}

func (c *Channel) Messages() <-chan core.NormalizedMessage {
	return c.inbound
}

// SubmitInbound normaliza uma entrada HTTP do usuário simulador e a envia no stream de mensagens recebidas.
func (c *Channel) SubmitInbound(ctx context.Context, tenantID, sourceKey, text string) (core.NormalizedMessage, error) {
	c.mu.RLock()
	started := c.started
	c.mu.RUnlock()
	if !started {
		return core.NormalizedMessage{}, ErrChannelStopped
	}
	if strings.TrimSpace(tenantID) == "" {
		tenantID = "demo"
	}
	if strings.TrimSpace(sourceKey) == "" {
		return core.NormalizedMessage{}, ErrEmptySource
	}
	if strings.TrimSpace(text) == "" {
		return core.NormalizedMessage{}, ErrEmptyText
	}

	now := c.clock.Now().UTC()
	msg := core.NormalizedMessage{
		TenantID:  strings.TrimSpace(tenantID),
		Channel:   c.ID(),
		Account:   "sim-web",
		SourceKey: strings.TrimSpace(sourceKey),
		MessageID: newSimMsgID(now),
		Timestamp: now,
		Direction: core.DirectionInbound,
		Type:      core.MessageTypeText,
		Text:      strings.TrimSpace(text),
	}
	if err := msg.Validate(); err != nil {
		return core.NormalizedMessage{}, err
	}

	select {
	case c.inbound <- msg:
		return msg, nil
	case <-ctx.Done():
		return core.NormalizedMessage{}, ctx.Err()
	}
}

func (c *Channel) Send(ctx context.Context, msg core.NormalizedMessage) error {
	if err := msg.Validate(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.started {
		return ErrChannelStopped
	}
	c.typing[msg.SourceKey] = false
	c.outbound[msg.SourceKey] = append(c.outbound[msg.SourceKey], msg)
	return nil
}

func (c *Channel) SendTyping(ctx context.Context, sourceKey string, typing bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.typing[sourceKey] = typing
	return nil
}

// SentMessages retorna o histórico de mensagens enviadas para uma sessão do simulador.
func (c *Channel) SentMessages(sourceKey string) []core.NormalizedMessage {
	c.mu.RLock()
	defer c.mu.RUnlock()
	list := c.outbound[sourceKey]
	copied := make([]core.NormalizedMessage, len(list))
	copy(copied, list)
	return copied
}

func newSimMsgID(now time.Time) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("sim_%d_%s", now.UnixNano(), hex.EncodeToString(b[:]))
}
