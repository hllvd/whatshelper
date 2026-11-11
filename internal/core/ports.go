package core

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrVersionConflict indica concorrência otimista detectada ao comitar estado da fonte.
	ErrVersionConflict = errors.New("store: conflito de versão otimista na fonte")
	// ErrDuplicateMessage indica que o message_id já foi persistido para esta fonte.
	ErrDuplicateMessage = errors.New("store: mensagem duplicada")
)

// ChannelCapabilities declara os recursos suportados pelo canal.
type ChannelCapabilities struct {
	PlainTextOnly bool `json:"plain_text_only"`
	Media         bool `json:"media"`
	Typing        bool `json:"typing"`
	Reactions     bool `json:"reactions"`
}

// ChannelHealth descreve o estado operacional de um canal.
type ChannelHealth struct {
	Healthy bool   `json:"healthy"`
	Detail  string `json:"detail,omitempty"`
}

// Channel define a abstração para qualquer meio de mensageria (Simulador, WhatsApp, Email, etc.).
type Channel interface {
	ID() string
	Capabilities() ChannelCapabilities
	Health() ChannelHealth
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Messages() <-chan NormalizedMessage
	Send(ctx context.Context, msg NormalizedMessage) error
	SendTyping(ctx context.Context, sourceKey string, typing bool) error
}

// CommitAnalysisInput agrupa a atualização atômica de estado após o estágio de análise.
type CommitAnalysisInput struct {
	TenantID        string
	Channel         string
	SourceKey       string
	ExpectedVersion int64
	Summary         Summary
	Facts           map[string]string
	Signals         map[string]bool
	Context         ConversationContext
	Run             Run
	UpdatedAt       time.Time
}

// Store define o contrato de persistência do estado estruturado no DynamoDB (ou in-memory).
type Store interface {
	GetSourceState(ctx context.Context, tenant, channel, sourceKey string) (*SourceState, error)
	SaveSourceMeta(ctx context.Context, meta SourceMeta) error
	CommitAnalysis(ctx context.Context, input CommitAnalysisInput) error
	PutMessageMeta(ctx context.Context, msg MessageMeta) error
	ListRecentMessages(ctx context.Context, tenant, channel, sourceKey string, limit int) ([]MessageMeta, error)
	ListSources(ctx context.Context, tenant string) ([]SourceMeta, error)
	ListRuns(ctx context.Context, tenant, channel, sourceKey string, limit int) ([]Run, error)
	SaveTask(ctx context.Context, task Task) error
	ListDueTasks(ctx context.Context, tenant string, dueBefore time.Time) ([]Task, error)
	SaveRun(ctx context.Context, run Run) error
	LinkSourceToContact(ctx context.Context, tenant, channel, sourceKey, contactID string) error
	ListContactSources(ctx context.Context, tenant, contactID string) ([]SourceLink, error)
}

// BlobStore define o contrato de armazenamento de objetos no S3/MinIO (ou in-memory).
type BlobStore interface {
	Put(ctx context.Context, key string, data []byte, contentType string) error
	Get(ctx context.Context, key string) ([]byte, error)
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) ([]string, error)
}

// LLMRequest estrutura a chamada para o provedor de modelo de linguagem.
type LLMRequest struct {
	Stage       string            `json:"stage"` // "analysis", "response", "nudge", etc.
	Model       string            `json:"model"`
	System      string            `json:"system"`
	Prompt      string            `json:"prompt"`
	JSONSchema  string            `json:"json_schema,omitempty"`
	Temperature float64           `json:"temperature"`
	MaxTokens   int               `json:"max_tokens"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// LLMResponse contém a resposta e métricas de execução do LLM.
type LLMResponse struct {
	Content          string `json:"content"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	DurationMs       int64  `json:"duration_ms"`
}

// LLM define a interface para chamada de modelos de inteligência artificial.
type LLM interface {
	Generate(ctx context.Context, req LLMRequest) (*LLMResponse, error)
}

// Clock abstrai a medição de tempo para permitir testes determinísticos.
type Clock interface {
	Now() time.Time
}

// SystemClock implementa Clock utilizando o relógio real do sistema.
type SystemClock struct{}

func (SystemClock) Now() time.Time {
	return time.Now().UTC()
}

// AdminAlert carrega uma notificação de escalonamento ou erro crítico para o dono do tenant.
type AdminAlert struct {
	TenantID    string            `json:"tenant_id"`
	Channel     string            `json:"channel"`
	SourceKey   string            `json:"source_key"`
	Reason      string            `json:"reason"`
	Message     string            `json:"message"`
	ContextData map[string]string `json:"context_data,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
}

// AdminNotifier define a interface de alerta e escalonamento para o operador/dono.
type AdminNotifier interface {
	Notify(ctx context.Context, alert AdminAlert) error
}
