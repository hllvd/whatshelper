package admin

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

const (
	ChannelID             = "admin"
	DefaultAdminSourceKey = "owner"
)

var (
	ErrChannelStopped = errors.New("admin: canal não iniciado")
	ErrEmptyText      = errors.New("admin: texto da mensagem é obrigatório")
)

// Channel implementa core.Channel dedicado para escalonamentos, correções e aprovação do dono.
type Channel struct {
	clock    core.Clock
	inbound  chan core.NormalizedMessage
	mu       sync.RWMutex
	started  bool
	outbound []core.NormalizedMessage
}

// New cria uma instância do canal admin.
func New(clock core.Clock, bufferSize int) *Channel {
	if clock == nil {
		clock = core.SystemClock{}
	}
	if bufferSize <= 0 {
		bufferSize = 128
	}
	return &Channel{
		clock:   clock,
		inbound: make(chan core.NormalizedMessage, bufferSize),
	}
}

func (c *Channel) ID() string {
	return ChannelID
}

func (c *Channel) Capabilities() core.ChannelCapabilities {
	return core.ChannelCapabilities{
		PlainTextOnly: true,
		Media:         false,
		Typing:        false,
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

// SubmitInbound normaliza uma mensagem/comando do dono enviado via HTTP/UI ou futuro adaptador WhatsApp.
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
		sourceKey = DefaultAdminSourceKey
	}
	cleanText := strings.TrimSpace(text)
	if cleanText == "" {
		return core.NormalizedMessage{}, ErrEmptyText
	}

	msg := core.NormalizedMessage{
		TenantID:  strings.TrimSpace(tenantID),
		Channel:   ChannelID,
		SourceKey: strings.TrimSpace(sourceKey),
		MessageID: newMessageID(),
		Timestamp: c.clock.Now().UTC(),
		Direction: core.DirectionInbound,
		Type:      core.MessageTypeText,
		Text:      cleanText,
	}

	select {
	case c.inbound <- msg:
		return msg, nil
	case <-ctx.Done():
		return core.NormalizedMessage{}, ctx.Err()
	}
}

func (c *Channel) Send(ctx context.Context, msg core.NormalizedMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.started {
		return ErrChannelStopped
	}
	c.outbound = append(c.outbound, msg)
	return nil
}

func (c *Channel) SendTyping(ctx context.Context, sourceKey string, typing bool) error {
	return nil
}

// SentMessages retorna cópia das mensagens enviadas ao dono (útil em testes e inspeção).
func (c *Channel) SentMessages() []core.NormalizedMessage {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]core.NormalizedMessage, len(c.outbound))
	copy(out, c.outbound)
	return out
}

func newMessageID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("adm-%d-%s", time.Now().UTC().UnixNano(), hex.EncodeToString(b[:]))
}
