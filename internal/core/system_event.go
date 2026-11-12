package core

import "time"

// SystemEventType identifica os eventos unificados que alimentam a FSM de jornada (EVT-01).
type SystemEventType string

const (
	SysEventUserMessage       SystemEventType = "user_message"
	SysEventSessionStart      SystemEventType = "session_start"
	SysEventSystemTimeout     SystemEventType = "system_timeout"
	SysEventHumanMessageSent  SystemEventType = "human_message_sent"
	SysEventHumanTakeover     SystemEventType = "human_takeover"
	SysEventHumanRelease      SystemEventType = "human_release"
	SysEventIntegrationResult SystemEventType = "integration_result"
)

func (t SystemEventType) IsValid() bool {
	switch t {
	case SysEventUserMessage,
		SysEventSessionStart,
		SysEventSystemTimeout,
		SysEventHumanMessageSent,
		SysEventHumanTakeover,
		SysEventHumanRelease,
		SysEventIntegrationResult:
		return true
	default:
		return false
	}
}

// SystemEvent representa um estímulo normalizado e idempotente para o motor de jornada.
type SystemEvent struct {
	EventID    string            `json:"event_id"`
	Type       SystemEventType   `json:"type"`
	Origin     MessageOrigin     `json:"origin"`
	TenantID   string            `json:"tenant_id"`
	Channel    string            `json:"channel"`
	SourceKey  string            `json:"source_key"`
	MessageID  string            `json:"message_id,omitempty"`
	Text       string            `json:"text,omitempty"`
	OccurredAt time.Time         `json:"occurred_at"`
	Payload    map[string]string `json:"payload,omitempty"`
}
