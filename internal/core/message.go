package core

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Direction define o fluxo da mensagem em relação ao sistema.
type Direction string

const (
	DirectionInbound  Direction = "inbound"
	DirectionOutbound Direction = "outbound"
)

func (d Direction) IsValid() bool {
	return d == DirectionInbound || d == DirectionOutbound
}

// MessageType define a natureza do conteúdo transmitido.
type MessageType string

const (
	MessageTypeText  MessageType = "text"
	MessageTypeAudio MessageType = "audio"
	MessageTypeImage MessageType = "image"
	MessageTypeDoc   MessageType = "doc"
)

func (t MessageType) IsValid() bool {
	switch t {
	case MessageTypeText, MessageTypeAudio, MessageTypeImage, MessageTypeDoc:
		return true
	default:
		return false
	}
}

// NormalizedMessage é a estrutura unificada de transporte e persistência de mensagens.
type NormalizedMessage struct {
	TenantID  string      `json:"tenant_id"`
	Channel   string      `json:"channel"`
	Account   string      `json:"account"`
	SourceKey string      `json:"source_key"`
	MessageID string      `json:"message_id"`
	Timestamp time.Time   `json:"timestamp"`
	Direction   Direction   `json:"direction"`
	Type        MessageType `json:"type"`
	Text        string      `json:"text"`
	MediaRef    string      `json:"media_ref,omitempty"`
	RawRef      string      `json:"raw_ref,omitempty"`
	RawContact  string      `json:"-"` // Metadado transiente; persistido apenas cifrado no SourceMeta.
	DisplayName string      `json:"-"` // Nome bruto de exibição; persistido apenas cifrado no SourceMeta.
}

var (
	ErrEmptyTenantID      = errors.New("tenant_id is required")
	ErrEmptyChannel       = errors.New("channel is required")
	ErrEmptySourceKey     = errors.New("source_key is required")
	ErrEmptyMessageID     = errors.New("message_id is required")
	ErrZeroTimestamp      = errors.New("timestamp must not be zero")
	ErrInvalidDirection   = errors.New("invalid direction: must be inbound or outbound")
	ErrInvalidMessageType = errors.New("invalid message type: must be text, audio, image or doc")
	ErrEmptyContent       = errors.New("text content or media reference is required")
)

// Validate verifica a validade da mensagem nas fronteiras do sistema.
func (m NormalizedMessage) Validate() error {
	if strings.TrimSpace(m.TenantID) == "" {
		return ErrEmptyTenantID
	}
	if strings.TrimSpace(m.Channel) == "" {
		return ErrEmptyChannel
	}
	if strings.TrimSpace(m.SourceKey) == "" {
		return ErrEmptySourceKey
	}
	if strings.TrimSpace(m.MessageID) == "" {
		return ErrEmptyMessageID
	}
	if m.Timestamp.IsZero() {
		return ErrZeroTimestamp
	}
	if !m.Direction.IsValid() {
		return fmt.Errorf("%w: %s", ErrInvalidDirection, m.Direction)
	}
	if !m.Type.IsValid() {
		return fmt.Errorf("%w: %s", ErrInvalidMessageType, m.Type)
	}
	if m.Type == MessageTypeText && strings.TrimSpace(m.Text) == "" {
		return ErrEmptyContent
	}
	if (m.Type == MessageTypeAudio || m.Type == MessageTypeImage || m.Type == MessageTypeDoc) &&
		strings.TrimSpace(m.MediaRef) == "" && strings.TrimSpace(m.Text) == "" {
		return ErrEmptyContent
	}
	return nil
}
