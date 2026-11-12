package brain_test

import (
	"path/filepath"
	"testing"
	"time"

	"whatshelper/internal/brain"
	"whatshelper/internal/core"
	"whatshelper/internal/pack"
)

func TestJRN02_ValidateJourney_RejectsInvalidConfigs(t *testing.T) {
	cases := []struct {
		name string
		yaml string
	}{
		{
			name: "missing_initial_state",
			yaml: "version: \"1\"\nstates:\n  start: {}\n",
		},
		{
			name: "unknown_initial_state",
			yaml: "version: \"1\"\ninitial_state: ghost\nstates:\n  start: {}\n",
		},
		{
			name: "priority_tie_in_same_scope",
			yaml: `
version: "1"
initial_state: start
states:
  start:
    rules:
      - id: r1
        priority: 10
        on_event: user_message
        to: next
      - id: r2
        priority: 10
        on_event: user_message
        to: next
  next: {}
`,
		},
		{
			name: "invalid_guard_expression",
			yaml: `
version: "1"
initial_state: start
states:
  start:
    rules:
      - id: r1
        priority: 10
        guard: 'unknown_var == 123'
        to: next
  next: {}
`,
		},
		{
			name: "unknown_action_type",
			yaml: `
version: "1"
initial_state: start
states:
  start:
    rules:
      - id: r1
        priority: 10
        to: next
        actions:
          - type: launch_missiles
  next: {}
`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := pack.ParseJourneyYAML([]byte(tc.yaml), nil); err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
		})
	}
}

func TestJRN03_JRN04_JRN05_NextJourney_Table(t *testing.T) {
	jYAML := `
version: "1"
initial_state: start
defaults:
  stagnation_threshold: 2
  stagnation_to: escalate
global_rules:
  - id: g_frustrated
    priority: 100
    on_event: user_message
    guard: 'dimensions["sentiment"] == "frustrated"'
    to: escalate
    reason: "global_frustrated"
states:
  start:
    directive:
      goal: "Greet and qualify"
      must_ask: ["What is your budget?"]
    rules:
      - id: s_to_qualified
        priority: 20
        on_event: user_message
        guard: 'HasFact("Budget") && FactNum("Budget") > 0'
        to: qualified
        reason: "budget_provided"
      - id: s_to_info
        priority: 10
        on_event: user_message
        guard: 'signals["has_new_question"] == true'
        to: info
        reason: "question_asked"
  qualified:
    directive:
      goal: "Schedule next step"
    rules:
      - id: q_timeout
        priority: 10
        on_event: system_timeout
        to: follow_up
  info:
    directive:
      goal: "Answer question"
  follow_up: {}
  escalate:
    terminal: true
`
	j, err := pack.ParseJourneyYAML([]byte(jYAML), nil)
	if err != nil {
		t.Fatalf("ParseJourneyYAML: %v", err)
	}
	now := time.Date(2026, 11, 12, 18, 0, 0, 0, time.UTC)

	// 1. Precedência: regra global vence regra do estado mesmo quando ambas casam
	outGlobal := brain.NextJourney(brain.NextJourneyInput{
		Journey:    j,
		Current:    &core.JourneyState{State: "start", EnteredAt: now},
		Event:      core.SystemEvent{Type: core.SysEventUserMessage},
		Facts:      map[string]string{"Budget": "5000"},
		Dimensions: map[string]string{"sentiment": "frustrated"},
		Now:        now.Add(1 * time.Minute),
	})
	if outGlobal.NextState != "escalate" || outGlobal.RuleID != "g_frustrated" || !outGlobal.Digression {
		t.Fatalf("expected global rule to win, got %+v", outGlobal)
	}

	// 2. Prioridade dentro do estado: 20 > 10
	outState := brain.NextJourney(brain.NextJourneyInput{
		Journey: j,
		Current: &core.JourneyState{State: "start", EnteredAt: now},
		Event:   core.SystemEvent{Type: core.SysEventUserMessage},
		Facts:   map[string]string{"Budget": "5000"},
		Signals: map[string]bool{"has_new_question": true},
		Now:     now.Add(1 * time.Minute),
	})
	if outState.NextState != "qualified" || outState.RuleID != "s_to_qualified" || outState.StagnationCount != 0 {
		t.Fatalf("expected s_to_qualified (prio 20), got %+v", outState)
	}

	// 3. Estagnação: 2 turnos sem progresso em 'start' -> 'escalate'
	outStag1 := brain.NextJourney(brain.NextJourneyInput{
		Journey: j,
		Current: &core.JourneyState{State: "start", EnteredAt: now, StagnationCount: 0},
		Event:   core.SystemEvent{Type: core.SysEventUserMessage},
		Now:     now.Add(1 * time.Minute),
	})
	if outStag1.NextState != "start" || outStag1.StagnationCount != 1 || outStag1.Stagnated {
		t.Fatalf("expected stagnation=1 on turn 1, got %+v", outStag1)
	}
	outStag2 := brain.NextJourney(brain.NextJourneyInput{
		Journey: j,
		Current: &core.JourneyState{State: "start", EnteredAt: now, StagnationCount: 1},
		Event:   core.SystemEvent{Type: core.SysEventUserMessage},
		Now:     now.Add(2 * time.Minute),
	})
	if outStag2.NextState != "escalate" || !outStag2.Stagnated || outStag2.StagnationCount != 0 {
		t.Fatalf("expected stagnation transition to escalate on turn 2, got %+v", outStag2)
	}

	// 4. Evento de sistema (system_timeout) dispara regra de timeout
	outTimeout := brain.NextJourney(brain.NextJourneyInput{
		Journey: j,
		Current: &core.JourneyState{State: "qualified", EnteredAt: now},
		Event:   core.SystemEvent{Type: core.SysEventSystemTimeout},
		Now:     now.Add(120 * time.Minute),
	})
	if outTimeout.NextState != "follow_up" || outTimeout.RuleID != "q_timeout" {
		t.Fatalf("expected timeout transition to follow_up, got %+v", outTimeout)
	}
}

func TestJRN07_JRN08_VerticalPacksJourneyReachability(t *testing.T) {
	for _, name := range []string{"realestate", "support"} {
		pk, err := pack.LoadFromDir(filepath.Join("..", "..", "packs", name))
		if err != nil {
			t.Fatalf("LoadFromDir(%s): %v", name, err)
		}
		if pk.Journey == nil {
			t.Fatalf("pack %s missing journey.yaml", name)
		}
		// BFS reachability from InitialState
		visited := map[string]bool{pk.Journey.InitialState: true}
		queue := []string{pk.Journey.InitialState}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			st := pk.Journey.States[cur]
			for _, r := range st.Rules {
				if r.To != "" && !visited[r.To] {
					visited[r.To] = true
					queue = append(queue, r.To)
				}
			}
			for _, r := range pk.Journey.GlobalRules {
				if r.To != "" && !visited[r.To] {
					visited[r.To] = true
					queue = append(queue, r.To)
				}
			}
			if st.StagnationTo != "" && !visited[st.StagnationTo] {
				visited[st.StagnationTo] = true
				queue = append(queue, st.StagnationTo)
			}
		}
		for stateName := range pk.Journey.States {
			if !visited[stateName] {
				t.Fatalf("pack %s has unreachable journey state %q", name, stateName)
			}
		}
	}
}
