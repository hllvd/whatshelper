package brain

import (
	"fmt"

	"whatshelper/internal/core"
	"whatshelper/internal/pack"
)

// DecisionAction representa a ação determinística decidida pelo roteador do cérebro.
type DecisionAction string

const (
	DecisionReply           DecisionAction = "reply"
	DecisionEscalate        DecisionAction = "escalate"
	DecisionCreateTask      DecisionAction = "create_task"
	DecisionHandoff         DecisionAction = "handoff"
	DecisionOptOut          DecisionAction = "opt_out"
	DecisionAwaitApproval   DecisionAction = "await_approval"
	DecisionDraftOnly       DecisionAction = "draft_only"
	DecisionOutOfHoursReply DecisionAction = "out_of_hours_reply"
	DecisionSkip            DecisionAction = "skip"
)

// RouteInput agrupa todas as entradas puras usadas para decidir a próxima ação após a análise.
type RouteInput struct {
	Pack               *pack.Pack
	Analysis           *pack.AnalysisResult
	State              *core.SourceState
	Policy             core.TenantPolicy
	WithinBusinessHour bool
	RateLimited        bool
}

// Decision descreve o resultado determinístico do roteador para a etapa de execução.
type Decision struct {
	Action           DecisionAction    `json:"action"`
	Intent           string            `json:"intent,omitempty"`
	Reason           string            `json:"reason,omitempty"`
	ShouldCallReply  bool              `json:"should_call_reply"`
	ShouldCreateTask bool              `json:"should_create_task"`
	UpdateStatus     core.SourceStatus `json:"update_status,omitempty"`
}

// Route decide de forma pura e determinística a ação a tomar a partir do pack, análise, estado e política.
func Route(in RouteInput) Decision {
	if !in.Policy.BotEnabled {
		return Decision{
			Action: DecisionSkip,
			Reason: "bot desabilitado pela política do tenant",
		}
	}

	if in.State != nil {
		switch in.State.Meta.Status {
		case core.SourceStatusPaused, core.SourceStatusHuman, core.SourceStatusOptedOut, core.SourceStatusArchived:
			return Decision{
				Action: DecisionSkip,
				Reason: fmt.Sprintf("fonte bloqueada para automação (status=%s)", in.State.Meta.Status),
			}
		}
	}

	if in.RateLimited {
		return Decision{
			Action: DecisionSkip,
			Reason: "rate limit atingido",
		}
	}

	if in.Analysis == nil {
		return Decision{
			Action:           DecisionEscalate,
			Reason:           "análise estruturada indisponível",
			ShouldCreateTask: true,
		}
	}

	intentName := in.Analysis.Intent
	intentAction := pack.IntentActionReply
	if in.Pack != nil && intentName != "" {
		if def, ok := in.Pack.IntentByName(intentName); ok {
			intentAction = def.Action
		}
	}

	switch intentAction {
	case pack.IntentActionOptOut:
		return Decision{
			Action:       DecisionOptOut,
			Intent:       intentName,
			Reason:       "cliente solicitou descadastro (opt-out)",
			UpdateStatus: core.SourceStatusOptedOut,
		}
	case pack.IntentActionHandoff:
		return Decision{
			Action:           DecisionHandoff,
			Intent:           intentName,
			Reason:           "handoff humano solicitado",
			ShouldCreateTask: true,
			UpdateStatus:     core.SourceStatusHuman,
		}
	case pack.IntentActionEscalate:
		return Decision{
			Action:           DecisionEscalate,
			Intent:           intentName,
			Reason:           fmt.Sprintf("intenção %q requer escalonamento humano", intentName),
			ShouldCreateTask: true,
		}
	}

	createTask := intentAction == pack.IntentActionCreateTask

	// Modo rascunho não envia mensagens automaticamente em nenhum horário.
	if in.Policy.Mode == core.ModeDraft {
		return Decision{
			Action:           DecisionDraftOnly,
			Intent:           intentName,
			Reason:           "política do tenant em modo draft",
			ShouldCallReply:  true,
			ShouldCreateTask: createTask,
		}
	}

	// Fora do horário comercial envia apenas resposta padrão estática (quando configurada) e agenda retorno.
	if in.Policy.BusinessHours.Enabled && !in.WithinBusinessHour {
		return Decision{
			Action:           DecisionOutOfHoursReply,
			Intent:           intentName,
			Reason:           "fora do horário comercial",
			ShouldCreateTask: true,
		}
	}

	if in.Policy.Mode == core.ModeApproval {
		return Decision{
			Action:           DecisionAwaitApproval,
			Intent:           intentName,
			Reason:           "política do tenant exige aprovação humana antes do envio",
			ShouldCallReply:  true,
			ShouldCreateTask: createTask,
		}
	}

	action := DecisionReply
	if createTask {
		action = DecisionCreateTask
	}
	return Decision{
		Action:           action,
		Intent:           intentName,
		Reason:           "resposta automática autorizada",
		ShouldCallReply:  true,
		ShouldCreateTask: createTask,
	}
}
