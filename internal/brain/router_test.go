package brain_test

import (
	"testing"

	"whatshelper/internal/brain"
	"whatshelper/internal/core"
	"whatshelper/internal/pack"
)

func sampleRouterPack(t *testing.T) *pack.Pack {
	t.Helper()
	yamlCfg := `
name: generic
language: pt-BR
actors:
  business: agent
  customer: customer
intents:
  - name: ask_info
    description: Pergunta geral
    action: reply
  - name: schedule_meeting
    description: Quer agendar reunião
    action: create_task
  - name: exception_request
    description: Pedido especial fora do padrão
    action: escalate
  - name: talk_to_human
    description: Quer falar com humano
    action: handoff
  - name: stop_messages
    description: Quer parar de receber mensagens
    action: opt_out
prompts:
  analysis: prompts/analysis.tmpl
  reply: prompts/reply.tmpl
`
	pk, err := pack.ParsePackYAML([]byte(yamlCfg))
	if err != nil {
		t.Fatalf("ParsePackYAML failed: %v", err)
	}
	return pk
}

func basePolicy() core.TenantPolicy {
	return core.TenantPolicy{
		ID:         "t1",
		Pack:       "generic",
		Mode:       core.ModeAuto,
		BotEnabled: true,
		BusinessHours: core.BusinessHours{
			Enabled: true,
			Days:    []string{"mon", "tue", "wed", "thu", "fri"},
			Start:   "09:00",
			End:     "18:00",
		},
	}.WithDefaults()
}

func TestRoute_Decisions(t *testing.T) {
	pk := sampleRouterPack(t)
	activeState := &core.SourceState{Meta: core.SourceMeta{Status: core.SourceStatusActive}}

	cases := []struct {
		name           string
		in             brain.RouteInput
		wantAction     brain.DecisionAction
		wantReply      bool
		wantCreateTask bool
		wantStatus     core.SourceStatus
	}{
		{
			name: "bot disabled skips",
			in: brain.RouteInput{
				Pack:               pk,
				Analysis:           &pack.AnalysisResult{Intent: "ask_info"},
				State:              activeState,
				Policy:             func() core.TenantPolicy { p := basePolicy(); p.BotEnabled = false; return p }(),
				WithinBusinessHour: true,
			},
			wantAction: brain.DecisionSkip,
		},
		{
			name: "source human skips",
			in: brain.RouteInput{
				Pack:               pk,
				Analysis:           &pack.AnalysisResult{Intent: "ask_info"},
				State:              &core.SourceState{Meta: core.SourceMeta{Status: core.SourceStatusHuman}},
				Policy:             basePolicy(),
				WithinBusinessHour: true,
			},
			wantAction: brain.DecisionSkip,
		},
		{
			name: "rate limited skips",
			in: brain.RouteInput{
				Pack:               pk,
				Analysis:           &pack.AnalysisResult{Intent: "ask_info"},
				State:              activeState,
				Policy:             basePolicy(),
				WithinBusinessHour: true,
				RateLimited:        true,
			},
			wantAction: brain.DecisionSkip,
		},
		{
			name: "opt_out updates status and does not call reply LLM",
			in: brain.RouteInput{
				Pack:               pk,
				Analysis:           &pack.AnalysisResult{Intent: "stop_messages"},
				State:              activeState,
				Policy:             basePolicy(),
				WithinBusinessHour: true,
			},
			wantAction: brain.DecisionOptOut,
			wantStatus: core.SourceStatusOptedOut,
		},
		{
			name: "handoff marks human and creates task",
			in: brain.RouteInput{
				Pack:               pk,
				Analysis:           &pack.AnalysisResult{Intent: "talk_to_human"},
				State:              activeState,
				Policy:             basePolicy(),
				WithinBusinessHour: true,
			},
			wantAction:     brain.DecisionHandoff,
			wantCreateTask: true,
			wantStatus:     core.SourceStatusHuman,
		},
		{
			name: "escalate marks escalate and creates task",
			in: brain.RouteInput{
				Pack:               pk,
				Analysis:           &pack.AnalysisResult{Intent: "exception_request"},
				State:              activeState,
				Policy:             basePolicy(),
				WithinBusinessHour: true,
			},
			wantAction:     brain.DecisionEscalate,
			wantCreateTask: true,
		},
		{
			name: "create_task intent in auto mode replies and creates task",
			in: brain.RouteInput{
				Pack:               pk,
				Analysis:           &pack.AnalysisResult{Intent: "schedule_meeting"},
				State:              activeState,
				Policy:             basePolicy(),
				WithinBusinessHour: true,
			},
			wantAction:     brain.DecisionCreateTask,
			wantReply:      true,
			wantCreateTask: true,
		},
		{
			name: "approval mode awaits approval",
			in: brain.RouteInput{
				Pack:               pk,
				Analysis:           &pack.AnalysisResult{Intent: "ask_info"},
				State:              activeState,
				Policy:             func() core.TenantPolicy { p := basePolicy(); p.Mode = core.ModeApproval; return p }(),
				WithinBusinessHour: true,
			},
			wantAction: brain.DecisionAwaitApproval,
			wantReply:  true,
		},
		{
			name: "draft mode produces draft only even out of hours",
			in: brain.RouteInput{
				Pack:               pk,
				Analysis:           &pack.AnalysisResult{Intent: "ask_info"},
				State:              activeState,
				Policy:             func() core.TenantPolicy { p := basePolicy(); p.Mode = core.ModeDraft; return p }(),
				WithinBusinessHour: false,
			},
			wantAction: brain.DecisionDraftOnly,
			wantReply:  true,
		},
		{
			name: "out of hours triggers static reply + task",
			in: brain.RouteInput{
				Pack:               pk,
				Analysis:           &pack.AnalysisResult{Intent: "ask_info"},
				State:              activeState,
				Policy:             basePolicy(),
				WithinBusinessHour: false,
			},
			wantAction:     brain.DecisionOutOfHoursReply,
			wantCreateTask: true,
		},
		{
			name: "happy path auto reply",
			in: brain.RouteInput{
				Pack:               pk,
				Analysis:           &pack.AnalysisResult{Intent: "ask_info"},
				State:              activeState,
				Policy:             basePolicy(),
				WithinBusinessHour: true,
			},
			wantAction: brain.DecisionReply,
			wantReply:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := brain.Route(tc.in)
			if got.Action != tc.wantAction {
				t.Fatalf("Action=%s want %s (reason=%s)", got.Action, tc.wantAction, got.Reason)
			}
			if got.ShouldCallReply != tc.wantReply {
				t.Fatalf("ShouldCallReply=%v want %v", got.ShouldCallReply, tc.wantReply)
			}
			if got.ShouldCreateTask != tc.wantCreateTask {
				t.Fatalf("ShouldCreateTask=%v want %v", got.ShouldCreateTask, tc.wantCreateTask)
			}
			if got.UpdateStatus != tc.wantStatus {
				t.Fatalf("UpdateStatus=%s want %s", got.UpdateStatus, tc.wantStatus)
			}
		})
	}
}
