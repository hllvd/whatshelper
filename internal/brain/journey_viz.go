package brain

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"whatshelper/internal/core"
	"whatshelper/internal/pack"
)

// RenderJourneyMermaid gera um diagrama Mermaid (flowchart LR) a partir do journey.yaml,
// destacando o estado atual (se informado) com a classe CSS `currentState`.
func RenderJourneyMermaid(j *pack.JourneyDef, currentState string) string {
	if j == nil {
		return "flowchart LR\n  empty[\"Jornada não configurada\"]"
	}

	var b strings.Builder
	b.WriteString("flowchart LR\n")
	b.WriteString("  classDef currentState fill:#2563eb,stroke:#1d4ed8,color:#ffffff,stroke-width:3px\n")
	b.WriteString("  classDef terminalState fill:#334155,stroke:#64748b,color:#f8fafc\n")

	stateNames := make([]string, 0, len(j.States))
	for k := range j.States {
		stateNames = append(stateNames, k)
	}
	sort.Strings(stateNames)

	for _, name := range stateNames {
		st := j.States[name]
		label := name
		if name == j.InitialState {
			label += " (inicial)"
		}
		if st.Terminal {
			b.WriteString(fmt.Sprintf("  %s([\"%s\"]):::terminalState\n", name, label))
		} else {
			b.WriteString(fmt.Sprintf("  %s[\"%s\"]\n", name, label))
		}
	}

	for _, name := range stateNames {
		st := j.States[name]
		for _, r := range st.Rules {
			target := r.To
			if target == "" {
				target = name
			}
			edgeLabel := r.ID
			if r.OnEvent != "" && r.OnEvent != "user_message" {
				edgeLabel += " [" + r.OnEvent + "]"
			}
			b.WriteString(fmt.Sprintf("  %s -->|\"%s\"| %s\n", name, edgeLabel, target))
		}
		if st.StagnationTo != "" {
			b.WriteString(fmt.Sprintf("  %s -.->|\"stagnation\"| %s\n", name, st.StagnationTo))
		}
	}

	for _, gr := range j.GlobalRules {
		if gr.To != "" {
			b.WriteString(fmt.Sprintf("  GLOBAL[\"*\"] ==>|\"%s\"| %s\n", gr.ID, gr.To))
		}
	}

	if currentState != "" {
		if _, ok := j.States[currentState]; ok {
			b.WriteString(fmt.Sprintf("  class %s currentState\n", currentState))
		}
	}
	return b.String()
}

// GuardTraceEntry registra o resultado individual da avaliação de uma regra/guard para UIX-09 e UIX-11.
type GuardTraceEntry struct {
	Scope   string `json:"scope"`
	RuleID  string `json:"rule_id"`
	Guard   string `json:"guard,omitempty"`
	Matched bool   `json:"matched"`
	Reason  string `json:"reason,omitempty"`
	Error   string `json:"error,omitempty"`
}

// WhatIfResult representa a simulação pura (sem efeitos colaterais) de uma transição de jornada (UIX-11).
type WhatIfResult struct {
	Output     NextJourneyOutput `json:"output"`
	GuardTrace []GuardTraceEntry `json:"guard_trace"`
}

// SimulateJourneyWhatIf executa NextJourney sem persistir nada e registra a avaliação de cada guard.
func SimulateJourneyWhatIf(
	j *pack.JourneyDef,
	currentState string,
	stagnationCount int,
	eventType core.SystemEventType,
	intent string,
	facts map[string]string,
	signals map[string]bool,
	dims map[string]string,
	now time.Time,
) WhatIfResult {
	if j == nil {
		return WhatIfResult{}
	}
	if currentState == "" {
		currentState = j.InitialState
	}
	if eventType == "" {
		eventType = core.SysEventUserMessage
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	env := pack.JourneyGuardEnv{
		Facts:           facts,
		Signals:         signals,
		Dimensions:      dims,
		Intent:          intent,
		EventType:       string(eventType),
		State:           currentState,
		StagnationCount: stagnationCount,
	}

	var trace []GuardTraceEntry
	trace = append(trace, traceRuleSlice("global", j.GlobalRules, env)...)
	if st, ok := j.States[currentState]; ok {
		trace = append(trace, traceRuleSlice("state:"+currentState, st.Rules, env)...)
	}

	out := NextJourney(NextJourneyInput{
		Journey: j,
		Current: &core.JourneyState{
			State:           currentState,
			StagnationCount: stagnationCount,
			EnteredAt:       now,
		},
		Event:      core.SystemEvent{Type: eventType, OccurredAt: now},
		Analysis:   &pack.AnalysisResult{Intent: intent, Signals: signals, Dimensions: dims},
		Facts:      facts,
		Signals:    signals,
		Dimensions: dims,
		Now:        now,
	})
	return WhatIfResult{
		Output:     out,
		GuardTrace: trace,
	}
}

func traceRuleSlice(scope string, rules []pack.JourneyRuleDef, env pack.JourneyGuardEnv) []GuardTraceEntry {
	sorted := make([]pack.JourneyRuleDef, len(rules))
	copy(sorted, rules)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Priority > sorted[j].Priority
	})

	out := make([]GuardTraceEntry, 0, len(sorted))
	for _, r := range sorted {
		entry := GuardTraceEntry{
			Scope:  scope,
			RuleID: r.ID,
			Guard:  r.Guard,
		}
		if r.OnEvent != "" && r.OnEvent != "*" && r.OnEvent != env.EventType {
			entry.Reason = "event_mismatch"
			out = append(out, entry)
			continue
		}
		if r.Intent != "" && r.Intent != "*" && r.Intent != env.Intent {
			entry.Reason = "intent_mismatch"
			out = append(out, entry)
			continue
		}
		ok, err := EvalJourneyGuard(r, env)
		if err != nil {
			entry.Error = err.Error()
			entry.Reason = "guard_error"
		} else {
			entry.Matched = ok
			if ok {
				entry.Reason = "matched"
			} else {
				entry.Reason = "guard_false"
			}
		}
		out = append(out, entry)
	}
	return out
}
