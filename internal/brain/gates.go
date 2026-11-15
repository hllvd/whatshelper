package brain

import (
	"context"
	"fmt"
	"time"

	"whatshelper/internal/core"
	"whatshelper/internal/pack"
)

// PreGateInput reúne os dados necessários para decidir antes de chamar qualquer LLM.
type PreGateInput struct {
	Policy     core.TenantPolicy
	State      *core.SourceState
	Pack       *pack.Pack
	BatchTexts []string
	Now        time.Time
}

// PreGateResult descreve se o pipeline pode prosseguir para os estágios cognitivos.
type PreGateResult struct {
	Allowed      bool
	AutoResumed  bool
	Action       DecisionAction
	Reason       string
	UpdateStatus core.SourceStatus
}

// EvaluatePreLLMGates executa verificações puras e determinísticas antes de gastar tokens de LLM.
func EvaluatePreLLMGates(in PreGateInput) PreGateResult {
	if !in.Policy.BotEnabled {
		return PreGateResult{
			Allowed: false,
			Action:  DecisionSkip,
			Reason:  "bot desabilitado para este tenant (bot_enabled=false)",
		}
	}

	autoResumed := false
	if in.State != nil {
		switch in.State.Meta.Status {
		case core.SourceStatusHuman:
			if in.State.Meta.PausedUntil != nil && !in.Now.IsZero() && !in.Now.Before(*in.State.Meta.PausedUntil) {
				autoResumed = true
			} else {
				return PreGateResult{
					Allowed: false,
					Action:  DecisionSkip,
					Reason:  fmt.Sprintf("fonte bloqueada (status=%s)", in.State.Meta.Status),
				}
			}
		case core.SourceStatusPaused, core.SourceStatusOptedOut, core.SourceStatusArchived:
			return PreGateResult{
				Allowed: false,
				Action:  DecisionSkip,
				Reason:  fmt.Sprintf("fonte bloqueada (status=%s)", in.State.Meta.Status),
			}
		}
	}

	if autoResumed {
		return PreGateResult{
			Allowed:      true,
			AutoResumed:  true,
			Reason:       "pausa humana expirada; retomando atendimento automático",
			UpdateStatus: core.SourceStatusActive,
		}
	}
	return PreGateResult{Allowed: true}
}

// MatchesOptOutKeyword está desativado (retorna sempre false) para evitar falsos positivos
// em frases de domínio (ex.: "vai sair da empresa"); o opt-out é tratado contextualmente pelo LLM.
func MatchesOptOutKeyword(_ string, _ []string) bool {
	return false
}

// UpdateSourceStatus atualiza o status no META da fonte preservando ContactID/Version/CreatedAt.
func UpdateSourceStatus(ctx context.Context, st core.Store, now time.Time, tenant, channel, sourceKey string, status core.SourceStatus) error {
	return UpdateSourceStatusWithPause(ctx, st, now, tenant, channel, sourceKey, status, nil)
}

// UpdateSourceStatusWithPause atualiza o status e opcionalmente PausedUntil no META da fonte.
func UpdateSourceStatusWithPause(ctx context.Context, st core.Store, now time.Time, tenant, channel, sourceKey string, status core.SourceStatus, pausedUntil *time.Time) error {
	state, err := st.GetSourceState(ctx, tenant, channel, sourceKey)
	if err != nil {
		return err
	}
	meta := state.Meta
	meta.TenantID = tenant
	meta.Channel = channel
	meta.SourceKey = sourceKey
	meta.Status = status
	if status != core.SourceStatusHuman {
		meta.PausedUntil = nil
	} else if pausedUntil != nil {
		t := pausedUntil.UTC()
		meta.PausedUntil = &t
	}
	if meta.CreatedAt.IsZero() {
		meta.CreatedAt = now.UTC()
	}
	meta.UpdatedAt = now.UTC()
	return st.SaveSourceMeta(ctx, meta)
}
