package contextmod

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const ContractVersion = "v1"

var (
	ErrInvalidScope      = errors.New("contextmod: escopo inválido (tenant_id, channel e source_key obrigatórios)")
	ErrInvalidView       = errors.New("contextmod: view de contexto inválida")
	ErrVersionConflict   = errors.New("contextmod: conflito de versão otimista (expected_version divergente)")
	ErrNotFound          = errors.New("contextmod: recurso não encontrado")
	ErrUnauthorizedScope = errors.New("contextmod: credencial sem permissão para o escopo solicitado")
	ErrApprovalRequired  = errors.New("contextmod: aprovação explícita obrigatória")
)

// ViewName identifica os perfis suportados de montagem de contexto (CTX-04).
type ViewName string

const (
	ViewAnalysis      ViewName = "analysis"
	ViewResponse      ViewName = "response"
	ViewAdmin         ViewName = "admin"
	ViewExternalAgent ViewName = "external_agent"
	ViewSummaryOnly   ViewName = "summary_only"
)

func (v ViewName) IsValid() bool {
	switch v {
	case ViewAnalysis, ViewResponse, ViewAdmin, ViewExternalAgent, ViewSummaryOnly:
		return true
	default:
		return false
	}
}

// Scope identifica de forma isolada o alvo de leitura/escrita de contexto.
type Scope struct {
	TenantID  string `json:"tenant_id"`
	Channel   string `json:"channel"`
	SourceKey string `json:"source_key"`
	ContactID string `json:"contact_id,omitempty"` // Reservado para leitura agregada futura (CTX-14)
}

// Validate verifica o escopo padrão por fonte (tenant + canal + source_key).
func (s Scope) Validate() error {
	if strings.TrimSpace(s.TenantID) == "" || strings.TrimSpace(s.Channel) == "" || strings.TrimSpace(s.SourceKey) == "" {
		return ErrInvalidScope
	}
	return nil
}

// GetContextRequest parametriza a leitura montada de contexto com view e orçamento.
type GetContextRequest struct {
	Scope             Scope    `json:"scope"`
	View              ViewName `json:"view"`
	BudgetTokens      int      `json:"budget_tokens,omitempty"`
	ExcludeMessageIDs []string `json:"exclude_message_ids,omitempty"`
	RecentLimit       int      `json:"recent_limit,omitempty"`
}

// DroppedItem registra exatamente o que foi cortado pelo ContextBuilder por limite de orçamento.
type DroppedItem struct {
	Block       string `json:"block"`
	ItemID      string `json:"item_id"`
	Reason      string `json:"reason"`
	TokensSaved int    `json:"tokens_saved"`
}

// TruncationReport resume o uso de tokens e os cortes determinísticos aplicados.
type TruncationReport struct {
	BudgetTokens int           `json:"budget_tokens"`
	UsedTokens   int           `json:"used_tokens"`
	Truncated    bool          `json:"truncated"`
	DroppedItems []DroppedItem `json:"dropped_items,omitempty"`
}

// ContextBlock representa um bloco formatado na ordem definida pela view.
type ContextBlock struct {
	Kind          string `json:"kind"`
	Format        string `json:"format"`
	Content       string `json:"content"`
	TokenEstimate int    `json:"token_estimate"`
}

// ContextBundle é o DTO v1 devolvido por GetContext.
type ContextBundle struct {
	Version           string                 `json:"version"`
	Scope             Scope                  `json:"scope"`
	View              ViewName               `json:"view"`
	SourceVersion     int64                  `json:"source_version"`
	SourceStatus      string                 `json:"source_status"`
	Summary           *SummaryView           `json:"summary,omitempty"`
	Facts             map[string]string      `json:"facts,omitempty"`
	Signals           map[string]bool        `json:"signals,omitempty"`
	StructuredContext *StructuredContextView `json:"structured_context,omitempty"`
	RecentMessages    []MessageView          `json:"recent_messages,omitempty"`
	Knowledge         []KnowledgeDocView     `json:"knowledge,omitempty"`
	QAContent         string                 `json:"qa_content,omitempty"`
	Blocks            []ContextBlock         `json:"blocks,omitempty"`
	Truncation        TruncationReport       `json:"truncation"`
	DegradedProviders []string               `json:"degraded_providers,omitempty"`
	GeneratedAt       time.Time              `json:"generated_at"`
}

// MessageView expõe metadados e texto da mensagem sem expor PII bruta de contato.
type MessageView struct {
	MessageID string    `json:"message_id"`
	Timestamp time.Time `json:"timestamp"`
	Direction string    `json:"direction"`
	Type      string    `json:"type"`
	Sender    string    `json:"sender,omitempty"`
	Text      string    `json:"text"`
	MediaRef  string    `json:"media_ref,omitempty"`
	RawRef    string    `json:"raw_ref,omitempty"`
}

// MessagePage representa uma página determinística de mensagens com cursor.
type MessagePage struct {
	Version    string        `json:"version"`
	Scope      Scope         `json:"scope"`
	Items      []MessageView `json:"items"`
	NextCursor string        `json:"next_cursor,omitempty"`
	HasMore    bool          `json:"has_more"`
}

// FactSet representa o conjunto atual de fatos com versão otimista.
type FactSet struct {
	Version       string            `json:"version"`
	Scope         Scope             `json:"scope"`
	SourceVersion int64             `json:"source_version"`
	Facts         map[string]string `json:"facts"`
	UpdatedAt     time.Time         `json:"updated_at"`
}

// FactDiff registra alteração aplicada a um fato.
type FactDiff struct {
	Key      string `json:"key"`
	Action   string `json:"action"` // "added" | "updated" | "removed"
	OldValue string `json:"old_value,omitempty"`
	NewValue string `json:"new_value,omitempty"`
}

// RunWriteInput permite registrar metadados da execução atomicamente junto ao commit de estado.
type RunWriteInput struct {
	ID               string    `json:"id"`
	MessageID        string    `json:"message_id"`
	Stage            string    `json:"stage"`
	Status           string    `json:"status"`
	Model            string    `json:"model"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	DurationMs       int64     `json:"duration_ms"`
	TraceBlobRef     string    `json:"trace_blob_ref,omitempty"`
	TTL              int64     `json:"ttl,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

// ApplyFactsRequest aplica mudanças em fatos (e opcionalmente resumo/sinais/contexto/run) com ExpectedVersion.
type ApplyFactsRequest struct {
	Scope             Scope                  `json:"scope"`
	ExpectedVersion   int64                  `json:"expected_version"`
	Facts             map[string]string      `json:"facts,omitempty"`
	ClearedFacts      []string               `json:"cleared_facts,omitempty"`
	Actor             string                 `json:"actor,omitempty"`
	Summary           *string                `json:"summary,omitempty"`
	Signals           map[string]bool        `json:"signals,omitempty"`
	StructuredContext *StructuredContextView `json:"structured_context,omitempty"`
	Run               *RunWriteInput         `json:"run,omitempty"`
}

// ApplyFactsResult devolve a nova versão e o diff aplicado após commit atômico.
type ApplyFactsResult struct {
	Version       string            `json:"version"`
	Scope         Scope             `json:"scope"`
	SourceVersion int64             `json:"source_version"`
	Facts         map[string]string `json:"facts"`
	Diff          []FactDiff        `json:"diff,omitempty"`
	UpdatedAt     time.Time         `json:"updated_at"`
}

// SummaryView representa o resumo cumulativo da conversa.
type SummaryView struct {
	Version   string    `json:"version"`
	Scope     Scope     `json:"scope"`
	Text      string    `json:"text"`
	TurnCount int       `json:"turn_count"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SignalsView representa os sinais booleanos conhecidos da conversa.
type SignalsView struct {
	Version string          `json:"version"`
	Scope   Scope           `json:"scope"`
	Signals map[string]bool `json:"signals"`
}

// StructuredContextView representa o contexto estruturado extraído pelo pack.
type StructuredContextView struct {
	Version        string                       `json:"version"`
	Scope          Scope                        `json:"scope"`
	LastActionType string                       `json:"last_action_type,omitempty"`
	LastActionBy   string                       `json:"last_action_by,omitempty"`
	Description    string                       `json:"description,omitempty"`
	Details        map[string]string            `json:"details,omitempty"`
	Sections       map[string]map[string]string `json:"sections,omitempty"`
	UpdatedAt      time.Time                    `json:"updated_at"`
}

// KnowledgeDocView representa um documento Markdown da base de conhecimento.
type KnowledgeDocView struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// KnowledgeSearchHit representa um trecho ranqueado de busca no conhecimento.
type KnowledgeSearchHit struct {
	DocName string  `json:"doc_name"`
	Score   float64 `json:"score"`
	Snippet string  `json:"snippet"`
}

// KnowledgeSearchResult agrupa os resultados ordenados de SearchKnowledge.
type KnowledgeSearchResult struct {
	Version string               `json:"version"`
	Query   string               `json:"query"`
	Hits    []KnowledgeSearchHit `json:"hits"`
}

// LearningProposalView representa uma proposta de entrada em QA.md.
type LearningProposalView struct {
	Version         string     `json:"version"`
	ID              string     `json:"id"`
	TenantID        string     `json:"tenant_id"`
	OriginChannel   string     `json:"origin_channel"`
	OriginSourceKey string     `json:"origin_source_key"`
	Question        string     `json:"question"`
	ProposedAnswer  string     `json:"proposed_answer"`
	FinalAnswer     string     `json:"final_answer,omitempty"`
	Status          string     `json:"status"`
	CreatedAt       time.Time  `json:"created_at"`
	DecidedAt       *time.Time `json:"decided_at,omitempty"`
}

// RunView representa o resumo de uma execução do pipeline.
type RunView struct {
	ID               string    `json:"id"`
	MessageID        string    `json:"message_id"`
	Stage            string    `json:"stage"`
	Status           string    `json:"status"`
	Model            string    `json:"model"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	DurationMs       int64     `json:"duration_ms"`
	TraceBlobRef     string    `json:"trace_blob_ref,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

// RunTraceView expõe um run junto ao trace JSON bruto salvo no BlobStore (quando existente).
type RunTraceView struct {
	Version  string  `json:"version"`
	Scope    Scope   `json:"scope"`
	Run      RunView `json:"run"`
	RawTrace []byte  `json:"raw_trace,omitempty"`
}

// Service define o contrato único v1 do módulo de contexto (usado por brain, REST e MCP).
type Service interface {
	GetContext(ctx context.Context, req GetContextRequest) (*ContextBundle, error)
	ListMessages(ctx context.Context, scope Scope, cursor string, limit int) (*MessagePage, error)
	GetFacts(ctx context.Context, scope Scope) (*FactSet, error)
	ApplyFacts(ctx context.Context, req ApplyFactsRequest) (*ApplyFactsResult, error)
	GetSummary(ctx context.Context, scope Scope) (*SummaryView, error)
	GetSignals(ctx context.Context, scope Scope) (*SignalsView, error)
	GetStructuredContext(ctx context.Context, scope Scope) (*StructuredContextView, error)
	ListKnowledge(ctx context.Context, tenantID string) ([]KnowledgeDocView, error)
	GetKnowledgeDoc(ctx context.Context, tenantID, filename string) (*KnowledgeDocView, error)
	SearchKnowledge(ctx context.Context, tenantID, query string, limit int) (*KnowledgeSearchResult, error)
	ListLearningProposals(ctx context.Context, tenantID string) ([]LearningProposalView, error)
	ApproveProposal(ctx context.Context, tenantID, proposalID, actor, overrideAnswer string) (*LearningProposalView, error)
	RejectProposal(ctx context.Context, tenantID, proposalID, actor, reason string) (*LearningProposalView, error)
	GetRun(ctx context.Context, scope Scope, runID string) (*RunTraceView, error)
	ListRuns(ctx context.Context, scope Scope, limit int) ([]RunView, error)
}

func formatETag(version int64) string {
	return fmt.Sprintf(`"v%d"`, version)
}

// FormatETag gera o cabeçalho ETag correspondente à versão otimista da fonte.
func FormatETag(version int64) string {
	return formatETag(version)
}
