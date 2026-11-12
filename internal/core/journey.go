package core

import "time"

// JourneyState representa o estado persistido da jornada (SK = JOURNEY) separado de META.status.
type JourneyState struct {
	TenantID       string            `json:"tenant_id"`
	Channel        string            `json:"channel"`
	SourceKey      string            `json:"source_key"`
	State          string            `json:"state"`
	EnteredAt      time.Time         `json:"entered_at"`
	TurnsInState   int               `json:"turns_in_state"`
	Version        int64             `json:"version"`
	JourneyVersion string            `json:"journey_version,omitempty"`
	Dimensions     map[string]string `json:"dimensions,omitempty"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

// JourneyTransitionRecord registra uma mudança de estado no histórico (SK = JSTATE#<ts>#<run_id>).
type JourneyTransitionRecord struct {
	TenantID  string    `json:"tenant_id"`
	Channel   string    `json:"channel"`
	SourceKey string    `json:"source_key"`
	FromState string    `json:"from_state"`
	ToState   string    `json:"to_state"`
	EventType string    `json:"event_type"`
	RuleID    string    `json:"rule_id,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	RunID     string    `json:"run_id"`
	CreatedAt time.Time `json:"created_at"`
}

// OutboxStatus define o ciclo de vida de um item do outbox transacional.
type OutboxStatus string

const (
	OutboxStatusPending OutboxStatus = "pending"
	OutboxStatusDone    OutboxStatus = "done"
	OutboxStatusFailed  OutboxStatus = "failed"
	OutboxStatusDead    OutboxStatus = "dead"
)

// OutboxItem representa um efeito colateral idempotente gravado na mesma transação da jornada.
type OutboxItem struct {
	ID            string         `json:"id"` // <run_id>#<index>
	TenantID      string         `json:"tenant_id"`
	Channel       string         `json:"channel"`
	SourceKey     string         `json:"source_key"`
	RunID         string         `json:"run_id"`
	Index         int            `json:"index"`
	ActionType    string         `json:"action_type"`
	ActionName    string         `json:"action_name,omitempty"`
	MessageID     string         `json:"message_id,omitempty"`
	Params        map[string]any `json:"params,omitempty"`
	Status        OutboxStatus   `json:"status"`
	Attempts      int            `json:"attempts"`
	MaxAttempts   int            `json:"max_attempts"`
	NextAttemptAt time.Time      `json:"next_attempt_at"`
	LastError     string         `json:"last_error,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

// CommitJourneyTurnInput agrupa a gravação atômica de JOURNEY, JSTATE#, OUTBOX# e OUTMSG# com ExpectedVersion.
type CommitJourneyTurnInput struct {
	TenantID        string
	Channel         string
	SourceKey       string
	ExpectedVersion int64
	Journey         JourneyState
	Transition      *JourneyTransitionRecord
	OutboxItems     []OutboxItem
	OutboundRecords []OutboundMessageRecord
	UpdatedAt       time.Time
}
