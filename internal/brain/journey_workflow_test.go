package brain_test

import (
	"testing"
	"time"

	"whatshelper/internal/brain"
	"whatshelper/internal/core"
	"whatshelper/internal/pack"
)

func TestNextJourney_ResetsStateWhenWorkflowChanged(t *testing.T) {
	supportJourney := &pack.JourneyDef{
		Version:      "1",
		InitialState: "entender_pergunta",
		States: map[string]pack.JourneyStateDef{
			"entender_pergunta": {
				Description: "Entender",
				Rules: []pack.JourneyRuleDef{
					{
						ID:       "to_answer",
						Priority: 10,
						OnEvent:  string(core.SysEventUserMessage),
						Intent:   "ask_info",
						To:       "responder_pergunta",
					},
				},
			},
			"responder_pergunta": {
				Description: "Responder",
			},
		},
	}

	// Simulate an existing session whose state belonged to another workflow ("entender_dor")
	out := brain.NextJourney(brain.NextJourneyInput{
		Journey: supportJourney,
		Current: &core.JourneyState{
			State:           "entender_dor",
			StagnationCount: 2,
		},
		Event: core.SystemEvent{
			Type: core.SysEventUserMessage,
		},
		Analysis: &pack.AnalysisResult{
			Intent: "ask_info",
		},
		Now: time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC),
	})

	if out.PrevState != "entender_pergunta" || out.NextState != "responder_pergunta" {
		t.Fatalf("expected reset to entender_pergunta -> responder_pergunta, got %s -> %s", out.PrevState, out.NextState)
	}
}
