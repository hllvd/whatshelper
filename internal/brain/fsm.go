package brain

import (
	"fmt"
	"strings"

	"whatshelper/internal/pack"
)

// TransitionOutcome descreve o resultado do cálculo determinístico de transição da FSM.
type TransitionOutcome struct {
	NextState       string         `json:"next_state"`
	ActionToTrigger string         `json:"action_to_trigger"`
	ActionDef       pack.ActionDef `json:"action_def"`
	Reason          string         `json:"reason"`
	StayedInState   bool           `json:"stayed_in_state"`
}

// CalculateNextState é uma função pura e determinística que avalia o estado atual da conversa
// e os dados extraídos pelo sensor (LLM) contra a máquina de estados declarada no pack.
func CalculateNextState(currentState string, analysis pack.AnalysisResult, fsm *pack.FSMDef) TransitionOutcome {
	if fsm == nil || len(fsm.States) == 0 {
		return TransitionOutcome{
			NextState:     currentState,
			Reason:        "fsm_not_configured",
			StayedInState: true,
		}
	}

	state := strings.TrimSpace(currentState)
	if state == "" {
		state = fsm.InitialState
	}

	for _, tr := range fsm.Transitions {
		if !matchesFromState(tr.From, state) {
			continue
		}
		if !matchesCondition(tr.When, analysis) {
			continue
		}

		actDef := fsm.Actions[tr.Action]
		return TransitionOutcome{
			NextState:       tr.To,
			ActionToTrigger: tr.Action,
			ActionDef:       actDef,
			Reason:          fmt.Sprintf("matched transition %s -> %s (action=%s)", tr.From, tr.To, tr.Action),
			StayedInState:   tr.To == state,
		}
	}

	// Fallback padrão se nenhuma transição específica for disparada
	fallbackAct := defaultFallbackAction(fsm)
	return TransitionOutcome{
		NextState:       state,
		ActionToTrigger: fallbackAct,
		ActionDef:       fsm.Actions[fallbackAct],
		Reason:          fmt.Sprintf("no matching transition for state %s; staying in current state", state),
		StayedInState:   true,
	}
}

func matchesFromState(from, current string) bool {
	return from == "*" || from == current
}

func matchesCondition(when pack.TransitionCondition, analysis pack.AnalysisResult) bool {
	if when.Intent != "" && when.Intent != analysis.Intent {
		return false
	}
	if when.Urgency != "" && !strings.EqualFold(when.Urgency, analysis.UrgencyLevel) {
		return false
	}
	if when.Sentiment != "" && !strings.EqualFold(when.Sentiment, analysis.Sentiment) {
		return false
	}
	for _, reqFact := range when.RequiredFacts {
		val, ok := analysis.Facts[reqFact]
		if !ok || strings.TrimSpace(val) == "" || strings.EqualFold(val, "null") {
			return false
		}
	}
	for _, reqSig := range when.RequiredSignals {
		if !analysis.Signals[reqSig] {
			return false
		}
	}
	return true
}

func defaultFallbackAction(fsm *pack.FSMDef) string {
	if fsm == nil || len(fsm.Actions) == 0 {
		return ""
	}
	// Prioriza resposta por conhecimento se declarada
	for name, act := range fsm.Actions {
		if act.Type == "knowledge_reply" {
			return name
		}
	}
	// Ou primeira ação disponível
	for name := range fsm.Actions {
		return name
	}
	return ""
}
