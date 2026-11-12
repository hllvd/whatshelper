package brain

import (
	"sort"
	"strings"
	"time"

	"whatshelper/internal/core"
	"whatshelper/internal/pack"
)

// NextJourneyInput reúne todos os dados imutáveis para a função pura de transição de jornada.
type NextJourneyInput struct {
	Journey    *pack.JourneyDef
	Current    *core.JourneyState
	Event      core.SystemEvent
	Analysis   *pack.AnalysisResult
	Facts      map[string]string
	Signals    map[string]bool
	Dimensions map[string]string
	Now        time.Time
}

// NextJourneyOutput descreve o resultado determinístico da transição de jornada.
type NextJourneyOutput struct {
	PrevState       string
	NextState       string
	ReturnToState   string
	Transitioned    bool
	RuleID          string
	Reason          string
	Actions         []pack.JourneyActionDef
	Directive       *pack.ResponseDirectiveDef
	StagnationCount int
	Stagnated       bool
	Digression      bool
	GuardErrors     []GuardEvalError
}

// NextJourney calcula a próxima etapa da jornada de forma 100% pura e determinística.
// Precedência: regras globais > regras do estado > estagnação > padrão (permanece no estado).
func NextJourney(in NextJourneyInput) NextJourneyOutput {
	if in.Journey == nil {
		return NextJourneyOutput{}
	}

	curState := in.Journey.InitialState
	var enteredAt time.Time
	stagnation := 0
	returnTo := ""
	if in.Current != nil {
		if strings.TrimSpace(in.Current.State) != "" {
			curState = in.Current.State
		}
		enteredAt = in.Current.EnteredAt
		stagnation = in.Current.StagnationCount
		returnTo = in.Current.ReturnToState
	}

	timeInStateMins := 0
	if !enteredAt.IsZero() && !in.Now.IsZero() && in.Now.After(enteredAt) {
		timeInStateMins = int(in.Now.Sub(enteredAt).Minutes())
	}

	intent := ""
	if in.Analysis != nil {
		intent = in.Analysis.Intent
	}
	dims := in.Dimensions
	if dims == nil && in.Analysis != nil {
		dims = in.Analysis.Dimensions
	}
	signals := in.Signals
	if signals == nil && in.Analysis != nil {
		signals = in.Analysis.Signals
	}

	env := pack.JourneyGuardEnv{
		Facts:              in.Facts,
		Signals:            signals,
		Dimensions:         dims,
		Intent:             intent,
		EventType:          string(in.Event.Type),
		State:              curState,
		StagnationCount:    stagnation,
		TimeInStateMinutes: timeInStateMins,
	}

	var guardErrs []GuardEvalError

	// 1. Regras globais (maior precedência)
	if matched, errs := matchJourneyRules(in.Journey.GlobalRules, env); len(errs) > 0 || matched != nil {
		guardErrs = append(guardErrs, errs...)
		if matched != nil {
			nextState := matched.To
			if nextState == "" {
				nextState = curState
			}
			transitioned := nextState != curState
			newStagnation := stagnation
			if transitioned {
				newStagnation = 0
			} else if in.Event.Type == core.SysEventUserMessage {
				newStagnation++
			}
			newReturnTo := returnTo
			digression := false
			if transitioned && curState != "" {
				newReturnTo = curState
				digression = true
			}
			directive := pickDirective(matched.Directive, in.Journey, nextState)
			reason := matched.Reason
			if reason == "" {
				reason = "global_rule:" + matched.ID
			}
			return NextJourneyOutput{
				PrevState:       curState,
				NextState:       nextState,
				ReturnToState:   newReturnTo,
				Transitioned:    transitioned,
				RuleID:          matched.ID,
				Reason:          reason,
				Actions:         matched.Actions,
				Directive:       directive,
				StagnationCount: newStagnation,
				Digression:      digression,
				GuardErrors:     guardErrs,
			}
		}
	}

	// 2. Regras do estado atual
	stateDef, hasState := in.Journey.States[curState]
	if hasState {
		if matched, errs := matchJourneyRules(stateDef.Rules, env); len(errs) > 0 || matched != nil {
			guardErrs = append(guardErrs, errs...)
			if matched != nil {
				nextState := matched.To
				if nextState == "" {
					nextState = curState
				}
				transitioned := nextState != curState
				newStagnation := stagnation
				if transitioned {
					newStagnation = 0
				} else if in.Event.Type == core.SysEventUserMessage {
					newStagnation++
				}
				newReturnTo := returnTo
				if transitioned && nextState == returnTo {
					newReturnTo = ""
				}
				directive := pickDirective(matched.Directive, in.Journey, nextState)
				reason := matched.Reason
				if reason == "" {
					reason = "state_rule:" + matched.ID
				}
				return NextJourneyOutput{
					PrevState:       curState,
					NextState:       nextState,
					ReturnToState:   newReturnTo,
					Transitioned:    transitioned,
					RuleID:          matched.ID,
					Reason:          reason,
					Actions:         matched.Actions,
					Directive:       directive,
					StagnationCount: newStagnation,
					GuardErrors:     guardErrs,
				}
			}
		}
	}

	// 3. Sem transição: incrementa estagnação em turno de usuário (JRN-05)
	newStagnation := stagnation
	if in.Event.Type == core.SysEventUserMessage && !stateDef.Terminal {
		newStagnation++
	}

	threshold := stateDef.StagnationThreshold
	if threshold <= 0 {
		threshold = in.Journey.Defaults.StagnationThreshold
	}
	stagnationTo := stateDef.StagnationTo
	if stagnationTo == "" {
		stagnationTo = in.Journey.Defaults.StagnationTo
	}

	if threshold > 0 && newStagnation >= threshold && !stateDef.Terminal {
		nextState := curState
		transitioned := false
		if stagnationTo != "" && stagnationTo != curState {
			nextState = stagnationTo
			transitioned = true
			newStagnation = 0
		}
		var actions []pack.JourneyActionDef
		if stateDef.StagnationAction != "" && pack.IsValidJourneyActionType(stateDef.StagnationAction) {
			actions = append(actions, pack.JourneyActionDef{Type: stateDef.StagnationAction})
		}
		return NextJourneyOutput{
			PrevState:       curState,
			NextState:       nextState,
			ReturnToState:   returnTo,
			Transitioned:    transitioned,
			RuleID:          "stagnation",
			Reason:          "stagnation_threshold_reached",
			Actions:         actions,
			Directive:       pickDirective(nil, in.Journey, nextState),
			StagnationCount: newStagnation,
			Stagnated:       true,
			GuardErrors:     guardErrs,
		}
	}

	// 4. Padrão: permanece no estado atual com a diretiva do estado
	return NextJourneyOutput{
		PrevState:       curState,
		NextState:       curState,
		ReturnToState:   returnTo,
		Transitioned:    false,
		Reason:          "default_stay",
		Directive:       pickDirective(nil, in.Journey, curState),
		StagnationCount: newStagnation,
		GuardErrors:     guardErrs,
	}
}

func matchJourneyRules(rules []pack.JourneyRuleDef, env pack.JourneyGuardEnv) (*pack.JourneyRuleDef, []GuardEvalError) {
	if len(rules) == 0 {
		return nil, nil
	}
	sorted := make([]pack.JourneyRuleDef, len(rules))
	copy(sorted, rules)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Priority > sorted[j].Priority
	})

	var errs []GuardEvalError
	for i := range sorted {
		r := &sorted[i]
		if r.OnEvent != "" && r.OnEvent != "*" && r.OnEvent != env.EventType {
			continue
		}
		if r.Intent != "" && r.Intent != "*" && r.Intent != env.Intent {
			continue
		}
		ok, err := EvalJourneyGuard(*r, env)
		if err != nil {
			errs = append(errs, GuardEvalError{
				RuleID: r.ID,
				Guard:  r.Guard,
				Error:  err.Error(),
			})
			continue
		}
		if ok {
			return r, errs
		}
	}
	return nil, errs
}

func pickDirective(ruleDir *pack.ResponseDirectiveDef, j *pack.JourneyDef, state string) *pack.ResponseDirectiveDef {
	if ruleDir != nil {
		return ruleDir
	}
	if j != nil {
		if st, ok := j.States[state]; ok && st.Directive != nil {
			return st.Directive
		}
		if j.Defaults.FallbackDirective != nil {
			return j.Defaults.FallbackDirective
		}
	}
	return nil
}
