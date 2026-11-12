package brain_test

import (
	"testing"

	"whatshelper/internal/brain"
	"whatshelper/internal/pack"
)

func TestCalculateNextState(t *testing.T) {
	fsm := &pack.FSMDef{
		InitialState: "state_start",
		States:       []string{"state_start", "state_qualifying", "state_scheduled", "state_escalated", "state_optout"},
		Actions: map[string]pack.ActionDef{
			"ask_budget": {
				Type: "message_template",
				Text: "Qual o seu orçamento?",
			},
			"send_calendar": {
				Type: "message_template",
				Text: "Link para agendamento: https://example.com/cal",
			},
			"reply_info": {
				Type: "knowledge_reply",
			},
			"escalate_action": {
				Type: "escalate",
			},
			"opt_out_action": {
				Type: "opt_out",
			},
		},
		Transitions: []pack.TransitionDef{
			{
				From: "*",
				To:   "state_optout",
				When: pack.TransitionCondition{
					Intent: "opt_out",
				},
				Action: "opt_out_action",
			},
			{
				From: "*",
				To:   "state_escalated",
				When: pack.TransitionCondition{
					Urgency: "critical",
				},
				Action: "escalate_action",
			},
			{
				From: "state_start",
				To:   "state_qualifying",
				When: pack.TransitionCondition{
					Intent: "inquiry_pricing",
				},
				Action: "ask_budget",
			},
			{
				From: "state_qualifying",
				To:   "state_scheduled",
				When: pack.TransitionCondition{
					Intent:        "schedule_meeting",
					RequiredFacts: []string{"Budget"},
				},
				Action: "send_calendar",
			},
			{
				From: "state_qualifying",
				To:   "state_qualifying",
				When: pack.TransitionCondition{
					Intent: "schedule_meeting",
				},
				Action: "ask_budget",
			},
		},
	}

	t.Run("nil FSM returns current state", func(t *testing.T) {
		outcome := brain.CalculateNextState("state_start", pack.AnalysisResult{}, nil)
		if outcome.NextState != "state_start" || !outcome.StayedInState {
			t.Errorf("unexpected outcome: %+v", outcome)
		}
	})

	t.Run("empty current state falls back to initial state", func(t *testing.T) {
		analysis := pack.AnalysisResult{
			Intent: "inquiry_pricing",
		}
		outcome := brain.CalculateNextState("", analysis, fsm)
		if outcome.NextState != "state_qualifying" || outcome.ActionToTrigger != "ask_budget" {
			t.Errorf("expected transition to state_qualifying via ask_budget, got %+v", outcome)
		}
	})

	t.Run("wildcard transition matches any state on opt_out", func(t *testing.T) {
		analysis := pack.AnalysisResult{
			Intent: "opt_out",
		}
		outcome := brain.CalculateNextState("state_scheduled", analysis, fsm)
		if outcome.NextState != "state_optout" || outcome.ActionToTrigger != "opt_out_action" {
			t.Errorf("expected state_optout, got %+v", outcome)
		}
	})

	t.Run("critical urgency triggers escalate wildcard", func(t *testing.T) {
		analysis := pack.AnalysisResult{
			UrgencyLevel: "critical",
		}
		outcome := brain.CalculateNextState("state_qualifying", analysis, fsm)
		if outcome.NextState != "state_escalated" || outcome.ActionToTrigger != "escalate_action" {
			t.Errorf("expected state_escalated, got %+v", outcome)
		}
	})

	t.Run("qualifying without required fact stays in qualifying", func(t *testing.T) {
		analysis := pack.AnalysisResult{
			Intent: "schedule_meeting",
			Facts:  map[string]string{}, // Budget missing
		}
		outcome := brain.CalculateNextState("state_qualifying", analysis, fsm)
		if outcome.NextState != "state_qualifying" || outcome.ActionToTrigger != "ask_budget" {
			t.Errorf("expected state_qualifying via ask_budget, got %+v", outcome)
		}
		if !outcome.StayedInState {
			t.Errorf("expected stayedInState=true")
		}
	})

	t.Run("qualifying with required fact advances to scheduled", func(t *testing.T) {
		analysis := pack.AnalysisResult{
			Intent: "schedule_meeting",
			Facts: map[string]string{
				"Budget": "500000",
			},
		}
		outcome := brain.CalculateNextState("state_qualifying", analysis, fsm)
		if outcome.NextState != "state_scheduled" || outcome.ActionToTrigger != "send_calendar" {
			t.Errorf("expected state_scheduled via send_calendar, got %+v", outcome)
		}
		if outcome.StayedInState {
			t.Errorf("expected stayedInState=false")
		}
	})

	t.Run("unmatched intent falls back to knowledge reply", func(t *testing.T) {
		analysis := pack.AnalysisResult{
			Intent: "random_unknown_intent",
		}
		outcome := brain.CalculateNextState("state_start", analysis, fsm)
		if outcome.NextState != "state_start" || outcome.ActionToTrigger != "reply_info" {
			t.Errorf("expected state_start with fallback reply_info, got %+v", outcome)
		}
	})
}
