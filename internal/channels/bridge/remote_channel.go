package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"whatshelper/internal/core"
)

// RemoteChannel representa um canal externo conectado ao Brain via WebSocket v1 (CH-02).
type RemoteChannel struct {
	channelID    string
	capabilities core.ChannelCapabilities
	logger       *slog.Logger
	ackTimeout   time.Duration
	upgrader     websocket.Upgrader

	inboundCh chan core.NormalizedMessage

	connMu   sync.Mutex
	conn     *websocket.Conn
	connSeq  uint64
	stopCh   chan struct{}
	stopOnce sync.Once

	pendingMu sync.Mutex
	pending   map[string]chan error

	healthMu sync.RWMutex
	health   core.ChannelHealth

	reqCounter atomic.Uint64
}

// NewRemoteChannel cria um adaptador core.Channel para um conector remoto via WebSocket.
func NewRemoteChannel(channelID string, caps core.ChannelCapabilities, logger *slog.Logger) *RemoteChannel {
	if logger == nil {
		logger = slog.Default()
	}
	return &RemoteChannel{
		channelID:    channelID,
		capabilities: caps,
		logger:       logger,
		ackTimeout:   15 * time.Second,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
		inboundCh: make(chan core.NormalizedMessage, 128),
		stopCh:    make(chan struct{}),
		pending:   make(map[string]chan error),
		health: core.ChannelHealth{
			Healthy: false,
			Detail:  "aguardando conexão do connector",
		},
	}
}

func (c *RemoteChannel) ID() string {
	return c.channelID
}

func (c *RemoteChannel) Capabilities() core.ChannelCapabilities {
	return c.capabilities
}

func (c *RemoteChannel) Health() core.ChannelHealth {
	c.healthMu.RLock()
	defer c.healthMu.RUnlock()
	return c.health
}

func (c *RemoteChannel) setHealth(healthy bool, detail string) {
	c.healthMu.Lock()
	defer c.healthMu.Unlock()
	c.health = core.ChannelHealth{
		Healthy: healthy,
		Detail:  detail,
	}
}

func (c *RemoteChannel) Start(ctx context.Context) error {
	return nil
}

func (c *RemoteChannel) Stop(ctx context.Context) error {
	c.stopOnce.Do(func() {
		close(c.stopCh)
		c.Disconnect()
		close(c.inboundCh)
	})
	return nil
}

// Disconnect fecha a conexão WebSocket ativa (se houver), forçando o connector a reconectar.
func (c *RemoteChannel) Disconnect() {
	c.connMu.Lock()
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
	c.connMu.Unlock()
}

func (c *RemoteChannel) Messages() <-chan core.NormalizedMessage {
	return c.inboundCh
}

// Send envia a mensagem ao connector remoto via WebSocket e aguarda ACK explícito.
func (c *RemoteChannel) Send(ctx context.Context, msg core.NormalizedMessage) error {
	reqID := fmt.Sprintf("req-%d", c.reqCounter.Add(1))
	ackCh := make(chan error, 1)

	c.pendingMu.Lock()
	c.pending[reqID] = ackCh
	c.pendingMu.Unlock()

	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, reqID)
		c.pendingMu.Unlock()
	}()

	msgCopy := msg
	frame := WSFrame{
		Type:      FrameSend,
		RequestID: reqID,
		Message:   &msgCopy,
	}
	if err := c.writeFrame(frame); err != nil {
		return err
	}

	timer := time.NewTimer(c.ackTimeout)
	defer timer.Stop()

	select {
	case err := <-ackCh:
		return err
	case <-timer.C:
		return fmt.Errorf("timeout aguardando ack do connector (%s)", reqID)
	case <-ctx.Done():
		return ctx.Err()
	case <-c.stopCh:
		return ErrConnectorOffline
	}
}

// SendTyping sinaliza digitação no canal remoto (best-effort caso offline).
func (c *RemoteChannel) SendTyping(ctx context.Context, sourceKey string, typing bool) error {
	if !c.capabilities.Typing {
		return nil
	}
	return c.writeFrame(WSFrame{
		Type:      FrameTyping,
		SourceKey: sourceKey,
		Typing:    typing,
	})
}

func (c *RemoteChannel) writeFrame(frame WSFrame) error {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	if c.conn == nil {
		return ErrConnectorOffline
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := c.conn.WriteJSON(frame); err != nil {
		return fmt.Errorf("erro ao escrever frame WS no canal %s: %w", c.channelID, err)
	}
	return nil
}

// HandleWebSocket aceita a conexão do connector e processa ACKs e eventos de saúde até desconectar.
func (c *RemoteChannel) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	if err := ValidateProtocolHeader(r.Header.Get(HeaderProtocolVersion)); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	wsConn, err := c.upgrader.Upgrade(w, r, http.Header{
		HeaderProtocolVersion: []string{ProtocolVersion},
	})
	if err != nil {
		c.logger.Warn("falha no upgrade WS do connector", slog.String("channel", c.channelID), slog.Any("error", err))
		return
	}

	c.connMu.Lock()
	prev := c.conn
	c.conn = wsConn
	c.connSeq++
	mySeq := c.connSeq
	c.connMu.Unlock()

	if prev != nil {
		_ = prev.Close()
	}

	c.setHealth(true, "connector conectado")
	c.logger.Info("connector WebSocket conectado", slog.String("channel", c.channelID))

	defer func() {
		_ = wsConn.Close()
		c.connMu.Lock()
		isCurrent := c.connSeq == mySeq && c.conn == wsConn
		if isCurrent {
			c.conn = nil
			c.setHealth(false, "connector desconectado")
		}
		c.connMu.Unlock()
		c.failPendingOnDisconnect(isCurrent)
	}()

	for {
		_, data, err := wsConn.ReadMessage()
		if err != nil {
			return
		}
		var frame WSFrame
		if err := json.Unmarshal(data, &frame); err != nil {
			c.logger.Warn("frame WS inválido recebido do connector", slog.String("channel", c.channelID), slog.Any("error", err))
			continue
		}
		c.handleClientFrame(frame)
	}
}

func (c *RemoteChannel) handleClientFrame(frame WSFrame) {
	switch frame.Type {
	case FrameAck:
		c.pendingMu.Lock()
		ch, ok := c.pending[frame.RequestID]
		c.pendingMu.Unlock()
		if !ok {
			return
		}
		var ackErr error
		if !frame.OK {
			if frame.Error != "" {
				ackErr = fmt.Errorf("connector falhou no envio: %s", frame.Error)
			} else {
				ackErr = fmt.Errorf("connector rejeitou envio %s", frame.RequestID)
			}
		}
		select {
		case ch <- ackErr:
		default:
		}
	case FrameHealth:
		c.setHealth(frame.Connected, frame.Detail)
	case FramePing:
		_ = c.writeFrame(WSFrame{Type: FramePong})
	}
}

func (c *RemoteChannel) failPendingOnDisconnect(wasCurrent bool) {
	if !wasCurrent {
		return
	}
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	for id, ch := range c.pending {
		select {
		case ch <- ErrConnectorOffline:
		default:
		}
		delete(c.pending, id)
	}
}
