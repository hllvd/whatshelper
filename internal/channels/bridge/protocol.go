package bridge

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"whatshelper/internal/core"
)

const (
	// ProtocolVersion identifica a versão v1 do protocolo Connector <-> Brain.
	ProtocolVersion = "1"
	// HeaderProtocolVersion transporta a versão do protocolo nas requisições HTTP/WS.
	HeaderProtocolVersion = "X-Protocol-Version"
)

// Tipos de frames trocados na conexão WebSocket v1.
const (
	FrameSend   = "send"
	FrameTyping = "typing"
	FrameAck    = "ack"
	FrameHealth = "health"
	FramePing   = "ping"
	FramePong   = "pong"
)

var (
	ErrUnsupportedProtocol = errors.New("versão de protocolo de connector não suportada")
	ErrConnectorOffline    = errors.New("connector remoto offline no momento")
)

// InboundRequest define o contrato JSON de entrada recebido em POST /api/connectors/v1/inbound.
type InboundRequest struct {
	TenantID     string           `json:"tenant_id"`
	Channel      string           `json:"channel"`
	Account      string           `json:"account"`
	SourceKey    string           `json:"source_key"`
	MessageID    string           `json:"message_id"`
	Timestamp    time.Time        `json:"timestamp"`
	Type         core.MessageType `json:"type"`
	Text         string           `json:"text"`
	MediaRef     string           `json:"media_ref,omitempty"`
	RawContact   string           `json:"raw_contact,omitempty"`
	DisplayName  string           `json:"display_name,omitempty"`
	RouteToAdmin bool             `json:"route_to_admin,omitempty"`
}

// InboundResponse responde o status de ingestão idempotente.
type InboundResponse struct {
	Status    string `json:"status"` // "accepted" | "duplicate"
	MessageID string `json:"message_id"`
}

// MediaUploadResponse retorna a referência de blob gravada no BlobStore.
type MediaUploadResponse struct {
	MediaRef string `json:"media_ref"`
}

// WSFrame representa qualquer frame JSON trafegado em GET /api/connectors/v1/ws.
type WSFrame struct {
	Type      string                  `json:"type"`
	RequestID string                  `json:"request_id,omitempty"`
	Message   *core.NormalizedMessage `json:"message,omitempty"`
	SourceKey string                  `json:"source_key,omitempty"`
	Typing    bool                    `json:"typing,omitempty"`
	OK        bool                    `json:"ok,omitempty"`
	Error     string                  `json:"error,omitempty"`
	Connected bool                    `json:"connected,omitempty"`
	Detail    string                  `json:"detail,omitempty"`
}

// ToNormalizedMessage valida e converte um InboundRequest na entidade canônica de domínio.
func (r InboundRequest) ToNormalizedMessage() (core.NormalizedMessage, error) {
	ts := r.Timestamp
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	msgType := r.Type
	if msgType == "" {
		msgType = core.MessageTypeText
	}
	msg := core.NormalizedMessage{
		TenantID:    strings.TrimSpace(r.TenantID),
		Channel:     strings.TrimSpace(r.Channel),
		Account:     strings.TrimSpace(r.Account),
		SourceKey:   strings.TrimSpace(r.SourceKey),
		MessageID:   strings.TrimSpace(r.MessageID),
		Timestamp:   ts.UTC(),
		Direction:   core.DirectionInbound,
		Type:        msgType,
		Text:        r.Text,
		MediaRef:    strings.TrimSpace(r.MediaRef),
		RawContact:  strings.TrimSpace(r.RawContact),
		DisplayName: strings.TrimSpace(r.DisplayName),
	}
	if err := msg.Validate(); err != nil {
		return core.NormalizedMessage{}, err
	}
	return msg, nil
}

// ValidateProtocolHeader verifica se o cabeçalho X-Protocol-Version está presente (quando exigido) e compatível com v1.
func ValidateProtocolHeader(version string) error {
	trimmed := strings.TrimSpace(version)
	if trimmed == "" || trimmed == ProtocolVersion {
		return nil
	}
	return fmt.Errorf("%w: recebido %q, esperado %q", ErrUnsupportedProtocol, trimmed, ProtocolVersion)
}

// BackoffDelay calcula backoff exponencial determinístico limitado por maxDelay.
func BackoffDelay(attempt int, base, maxDelay time.Duration) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	if base <= 0 {
		base = 200 * time.Millisecond
	}
	if maxDelay < base {
		maxDelay = base
	}
	mult := math.Pow(2, float64(attempt))
	d := time.Duration(float64(base) * mult)
	if d > maxDelay || d <= 0 {
		return maxDelay
	}
	return d
}
