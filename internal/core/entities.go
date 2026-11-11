package core

import (
	"errors"
	"strings"
	"time"
)

// Tenant representa um cliente/organização no sistema multi-tenant.
type Tenant struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	DefaultPack string            `json:"default_pack"`
	CreatedAt   time.Time         `json:"created_at"`
	Config      map[string]string `json:"config,omitempty"`
}

// SourceStatus define o estado de uma conversa em um canal específico.
type SourceStatus string

const (
	SourceStatusActive   SourceStatus = "active"
	SourceStatusPaused   SourceStatus = "paused"
	SourceStatusOptedOut SourceStatus = "opted_out"
	SourceStatusArchived SourceStatus = "archived"
)

// SourceMeta contém os metadados da fonte (conversa em canal).
type SourceMeta struct {
	TenantID             string       `json:"tenant_id"`
	Channel              string       `json:"channel"`
	SourceKey            string       `json:"source_key"`
	ContactID            string       `json:"contact_id,omitempty"`
	Status               SourceStatus `json:"status"`
	Version              int64        `json:"version"`
	EncryptedContactInfo string       `json:"encrypted_contact_info,omitempty"`
	CreatedAt            time.Time    `json:"created_at"`
	UpdatedAt            time.Time    `json:"updated_at"`
}

// SourceLink representa a ligação de uma fonte a um contato unificado.
type SourceLink struct {
	TenantID  string    `json:"tenant_id"`
	ContactID string    `json:"contact_id"`
	Channel   string    `json:"channel"`
	SourceKey string    `json:"source_key"`
	LinkedAt  time.Time `json:"linked_at"`
}

// Summary representa o resumo cumulativo da conversa.
type Summary struct {
	TenantID  string    `json:"tenant_id"`
	Channel   string    `json:"channel"`
	SourceKey string    `json:"source_key"`
	Text      string    `json:"text"`
	TurnCount int       `json:"turn_count"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Fact representa um fato extraído da conversa.
type Fact struct {
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Signal representa um sinal comportamental/objetivo detectado na conversa.
type Signal struct {
	Key       string    `json:"key"`
	Value     bool      `json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ConversationContext guarda o contexto estruturado da conversa (última ação, agendamentos).
type ConversationContext struct {
	LastActionType string                       `json:"last_action_type,omitempty"`
	LastActionBy   string                       `json:"last_action_by,omitempty"`
	Description    string                       `json:"description,omitempty"`
	Details        map[string]string            `json:"details,omitempty"`
	Sections       map[string]map[string]string `json:"sections,omitempty"`
	UpdatedAt      time.Time                    `json:"updated_at"`
}

// TaskStatus define o estado de uma tarefa ou pendência.
type TaskStatus string

const (
	TaskStatusPending   TaskStatus = "pending"
	TaskStatusCompleted TaskStatus = "completed"
	TaskStatusCancelled TaskStatus = "cancelled"
)

// Task representa uma tarefa ou pendência gerada a partir do atendimento.
type Task struct {
	ID          string     `json:"id"`
	TenantID    string     `json:"tenant_id"`
	Channel     string     `json:"channel"`
	SourceKey   string     `json:"source_key"`
	Title       string     `json:"title"`
	Type        string     `json:"type"`
	Status      TaskStatus `json:"status"`
	DueAt       *time.Time `json:"due_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// NudgeStatus define o estado do agendamento de follow-up proativo.
type NudgeStatus string

const (
	NudgeStatusPending   NudgeStatus = "pending"
	NudgeStatusSent      NudgeStatus = "sent"
	NudgeStatusCancelled NudgeStatus = "cancelled"
	NudgeStatusExhausted NudgeStatus = "exhausted"
)

// Nudge representa um follow-up agendado para quando o cliente para de responder.
type Nudge struct {
	ID           string      `json:"id"`
	TenantID     string      `json:"tenant_id"`
	Channel      string      `json:"channel"`
	SourceKey    string      `json:"source_key"`
	Attempt      int         `json:"attempt"`
	MaxAttempts  int         `json:"max_attempts"`
	ScheduledFor time.Time   `json:"scheduled_for"`
	Status       NudgeStatus `json:"status"`
	LastSentAt   *time.Time  `json:"last_sent_at,omitempty"`
	CreatedAt    time.Time   `json:"created_at"`
}

// Run representa um trace de execução do pipeline cognitivo (análise, resposta, etc.).
type Run struct {
	ID               string    `json:"id"`
	TenantID         string    `json:"tenant_id"`
	Channel          string    `json:"channel"`
	SourceKey        string    `json:"source_key"`
	MessageID        string    `json:"message_id"`
	Stage            string    `json:"stage"`  // ex: "analysis", "response", "nudge"
	Status           string    `json:"status"` // ex: "ok", "repaired", "failed"
	Model            string    `json:"model"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	DurationMs       int64     `json:"duration_ms"`
	TraceBlobRef     string    `json:"trace_blob_ref,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

// MessageMeta contém os metadados de uma mensagem indexada no DynamoDB (sem corpo cru longo).
type MessageMeta struct {
	TenantID  string      `json:"tenant_id"`
	Channel   string      `json:"channel"`
	SourceKey string      `json:"source_key"`
	MessageID string      `json:"message_id"`
	Timestamp time.Time   `json:"timestamp"`
	Direction Direction   `json:"direction"`
	Type      MessageType `json:"type"`
	MediaRef  string      `json:"media_ref,omitempty"`
	RawRef    string      `json:"raw_ref,omitempty"`
	Sender    string      `json:"sender,omitempty"`
}

// SourceState agrega todo o estado conhecido de uma conversa em um instante.
type SourceState struct {
	Version int64               `json:"version"`
	Meta    SourceMeta          `json:"meta"`
	Summary Summary             `json:"summary"`
	Facts   map[string]string   `json:"facts"`
	Signals map[string]bool     `json:"signals"`
	Context ConversationContext `json:"context"`
}

var (
	ErrEmptyTaskTitle = errors.New("task title is required")
	ErrEmptyTaskID    = errors.New("task id is required")
)

func (t Task) Validate() error {
	if strings.TrimSpace(t.ID) == "" {
		return ErrEmptyTaskID
	}
	if strings.TrimSpace(t.TenantID) == "" {
		return ErrEmptyTenantID
	}
	if strings.TrimSpace(t.SourceKey) == "" {
		return ErrEmptySourceKey
	}
	if strings.TrimSpace(t.Title) == "" {
		return ErrEmptyTaskTitle
	}
	return nil
}
