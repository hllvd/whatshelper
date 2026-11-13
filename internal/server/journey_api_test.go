package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"whatshelper/internal/brain"
	"whatshelper/internal/channels/simulator"
	"whatshelper/internal/config"
	"whatshelper/internal/core"
	"whatshelper/internal/core/fakes"
	"whatshelper/internal/pack"
	"whatshelper/internal/server"
)

func setupJourneyTestServer(t *testing.T) (*server.Server, *fakes.MemoryStore, *fakes.FakeClock) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 11, 13, 8, 0, 0, 0, time.UTC)
	clock := fakes.NewFakeClock(now)
	st := fakes.NewMemoryStoreWithClock(clock)
	blobs := fakes.NewMemoryBlobStore()
	bus := core.NewInMemoryEventBus(64)
	simCh := simulator.New(clock, 16)
	_ = simCh.Start(ctx)

	tmp := t.TempDir()
	packDir := filepath.Join(tmp, "packs", "generic")
	_ = os.MkdirAll(filepath.Join(packDir, "prompts"), 0o755)
	_ = os.WriteFile(filepath.Join(packDir, "pack.yaml"), []byte(`
name: generic
language: pt-BR
actors:
  business: agent
  customer: customer
facts:
  - key: Budget
    type: number
    description: Budget
signals:
  - key: has_new_question
    description: Question
context: []
prompts:
  analysis: prompts/analysis.tmpl
  reply: prompts/reply.tmpl
`), 0o644)
	_ = os.WriteFile(filepath.Join(packDir, "prompts", "analysis.tmpl"), []byte("A\n<!-- USER_PROMPT -->\n{{.NewMessage.Text}}"), 0o644)
	_ = os.WriteFile(filepath.Join(packDir, "prompts", "reply.tmpl"), []byte("R\n<!-- USER_PROMPT -->\n{{.NewMessage.Text}}"), 0o644)
	_ = os.WriteFile(filepath.Join(packDir, "journey.yaml"), []byte(`
version: "1"
initial_state: start
defaults:
  stagnation_threshold: 2
  stagnation_to: closed_lost
states:
  start:
    rules:
      - id: qualify_budget
        priority: 10
        on_event: user_message
        guard: 'HasFact("Budget")'
        to: qualified
        actions:
          - type: create_task
            params:
              type: follow_up
              title: "Follow up"
  qualified: {}
  closed_lost:
    terminal: true
`), 0o644)

	_ = brain.SeedTenantPolicy(ctx, st, core.TenantPolicy{
		ID:         "demo",
		Pack:       "generic",
		BotEnabled: true,
	})

	p := brain.NewPipeline(ctx, brain.PipelineConfig{
		Store:         st,
		Blobs:         blobs,
		Bus:           bus,
		Clock:         clock,
		Tenants:       brain.NewStoreTenantPolicyLoader(st, nil),
		Knowledge:     brain.NewLocalKnowledgeSource(filepath.Join(tmp, "tenants")),
		PackLoader:    func(name string) (*pack.Pack, error) { return pack.LoadFromDir(filepath.Join(tmp, "packs", name)) },
		AnalysisModel: "m-a",
		ResponseModel: "m-r",
	})
	p.RegisterChannel(simCh)

	srv := server.NewWithDeps(config.Config{}, nil, server.Dependencies{
		Store:     st,
		Blobs:     blobs,
		Bus:       bus,
		Simulator: simCh,
		Pipeline:  p,
	})
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
	return srv, st, clock
}

func TestM4f_JourneyPage_Mermaid_WhatIf_Outbox_Funnel_And_SimOrigin(t *testing.T) {
	srv, st, clock := setupJourneyTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	ctx := context.Background()
	now := clock.Now()

	// Seed a journey + transition + failed outbox item + unknown origin message
	_ = st.CommitJourneyTurn(ctx, core.CommitJourneyTurnInput{
		TenantID:  "demo",
		Channel:   "simulator",
		SourceKey: "sim-1",
		Journey: core.JourneyState{
			TenantID:  "demo",
			Channel:   "simulator",
			SourceKey: "sim-1",
			State:     "qualified",
			EnteredAt: now.Add(-15 * time.Minute),
			Version:   1,
			UpdatedAt: now,
		},
		Transition: &core.JourneyTransitionRecord{
			TenantID:  "demo",
			Channel:   "simulator",
			SourceKey: "sim-1",
			FromState: "start",
			ToState:   "qualified",
			EventType: "user_message",
			RuleID:    "qualify_budget",
			RunID:     "run-1",
			CreatedAt: now,
		},
		OutboxItems: []core.OutboxItem{{
			ID:            "run-1#0",
			TenantID:      "demo",
			Channel:       "simulator",
			SourceKey:     "sim-1",
			RunID:         "run-1",
			Index:         0,
			ActionType:    "create_task",
			Status:        core.OutboxStatusFailed,
			Attempts:      1,
			MaxAttempts:   5,
			NextAttemptAt: now.Add(1 * time.Minute),
			LastError:     "transient",
			CreatedAt:     now,
			UpdatedAt:     now,
		}},
	})

	_ = st.PutMessageMeta(ctx, core.MessageMeta{
		TenantID:  "demo",
		Channel:   "simulator",
		SourceKey: "sim-1",
		MessageID: "unk-msg-1",
		Timestamp: now,
		Direction: core.DirectionInbound,
		Type:      core.MessageTypeText,
		Origin:    core.OriginUnknown,
	})

	// 1. UIX-10: GET /journey page
	resp, err := http.Get(ts.URL + "/journey")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /journey failed: status=%v err=%v", resp, err)
	}
	resp.Body.Close()

	// 2. UIX-09 & UIX-10: GET /api/sources/simulator/sim-1/journey
	resp, err = http.Get(ts.URL + "/api/sources/simulator/sim-1/journey?tenant=demo")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET journey api failed: %v", err)
	}
	var jResp map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&jResp)
	resp.Body.Close()
	if !strings.Contains(jResp["mermaid"].(string), "class qualified currentState") {
		t.Fatalf("expected mermaid to highlight qualified, got:\n%s", jResp["mermaid"])
	}

	// 3. UIX-11: POST /api/journey/what-if (dry run)
	wiBody, _ := json.Marshal(map[string]any{
		"tenant_id":     "demo",
		"current_state": "start",
		"event_type":    "user_message",
		"facts":         map[string]string{"Budget": "5000"},
	})
	resp, err = http.Post(ts.URL+"/api/journey/what-if", "application/json", bytes.NewReader(wiBody))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("POST what-if failed: %v", err)
	}
	var wiOut brain.WhatIfResult
	_ = json.NewDecoder(resp.Body).Decode(&wiOut)
	resp.Body.Close()
	if wiOut.Output.NextState != "qualified" || len(wiOut.GuardTrace) == 0 {
		t.Fatalf("expected what-if NextState=qualified and GuardTrace, got %+v", wiOut)
	}

	// 4. UIX-12: GET /api/outbox & POST reprocess
	resp, err = http.Get(ts.URL + "/api/outbox?tenant=demo&status=failed")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET outbox failed: %v", err)
	}
	var obList struct {
		Items []core.OutboxItem `json:"items"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&obList)
	resp.Body.Close()
	if len(obList.Items) != 1 || obList.Items[0].ID != "run-1#0" {
		t.Fatalf("expected 1 failed outbox item, got %+v", obList.Items)
	}

	resp, err = http.Post(ts.URL+"/api/outbox/simulator/sim-1/"+strings.ReplaceAll("run-1#0", "#", "%23")+"/reprocess?tenant=demo", "application/json", nil)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("POST reprocess failed: status=%v err=%v", resp.StatusCode, err)
	}
	resp.Body.Close()
	reprocessed, _ := st.GetOutboxItem(ctx, "demo", "simulator", "sim-1", "run-1#0")
	if reprocessed.Status != core.OutboxStatusDone {
		t.Fatalf("expected outbox item done after reprocess, got %s", reprocessed.Status)
	}

	// 5. OBS-06: GET /api/metrics/funnel
	resp, err = http.Get(ts.URL + "/api/metrics/funnel?tenant=demo")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET funnel metrics failed: %v", err)
	}
	var metrics brain.FunnelMetrics
	_ = json.NewDecoder(resp.Body).Decode(&metrics)
	resp.Body.Close()
	if metrics.TotalConversations != 1 || len(metrics.Conversions) != 1 {
		t.Fatalf("unexpected funnel metrics: %+v", metrics)
	}

	// 6. UIX-13: POST /api/sim/origin (human_phone) + GET unknown-origin
	origBody, _ := json.Marshal(map[string]any{
		"tenant_id":  "demo",
		"channel":    "simulator",
		"source_key": "sim-1",
		"mode":       "human_phone",
		"text":       "Eu assumo por aqui",
	})
	resp, err = http.Post(ts.URL+"/api/sim/origin", "application/json", bytes.NewReader(origBody))
	if err != nil || resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST sim/origin failed: %v", err)
	}
	resp.Body.Close()
	stAfter, _ := st.GetSourceState(ctx, "demo", "simulator", "sim-1")
	if stAfter.Meta.Status != core.SourceStatusHuman {
		t.Fatalf("expected SourceStatusHuman after simulated human_phone, got %s", stAfter.Meta.Status)
	}

	resp, err = http.Get(ts.URL + "/api/messages/unknown-origin?tenant=demo")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET unknown-origin failed: %v", err)
	}
	var unkResp struct {
		Messages []map[string]any `json:"messages"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&unkResp)
	resp.Body.Close()
	if len(unkResp.Messages) != 1 {
		t.Fatalf("expected 1 unknown-origin message, got %+v", unkResp.Messages)
	}
}
