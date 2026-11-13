package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"whatshelper/internal/brain"
	"whatshelper/internal/config"
	"whatshelper/internal/core"
	"whatshelper/internal/core/fakes"
	"whatshelper/internal/pack"
	"whatshelper/internal/server"
)

func TestWorkflowsAPI_ListAndSwitchViaPolicy(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	pDir := filepath.Join(tmp, "packs", "generic")
	_ = os.MkdirAll(filepath.Join(pDir, "prompts"), 0o755)
	_ = os.MkdirAll(filepath.Join(pDir, "journeys"), 0o755)
	_ = os.WriteFile(filepath.Join(pDir, "pack.yaml"), []byte(`name: generic
language: pt-BR
actors:
  customer: Cliente
  business: Atendente
intents:
  - name: ask_info
    action: reply
    description: Dúvida
prompts:
  analysis: prompts/analysis.tmpl
  reply: prompts/reply.tmpl
`), 0o644)
	_ = os.WriteFile(filepath.Join(pDir, "prompts", "analysis.tmpl"), []byte("sys\n<!-- USER_PROMPT -->\nuser"), 0o644)
	_ = os.WriteFile(filepath.Join(pDir, "prompts", "reply.tmpl"), []byte("sys\n<!-- USER_PROMPT -->\nuser"), 0o644)
	_ = os.WriteFile(filepath.Join(pDir, "journeys", "support.yaml"), []byte(`version: "1"
initial_state: entender_pergunta
states:
  entender_pergunta:
    description: Entender
`), 0o644)
	_ = os.WriteFile(filepath.Join(pDir, "journeys", "leads.yaml"), []byte(`version: "1"
initial_state: entender_dor
states:
  entender_dor:
    description: Dor
  marcar_reuniao:
    terminal: true
`), 0o644)

	st := fakes.NewMemoryStore()
	_ = brain.SeedTenantPolicy(ctx, st, core.TenantPolicy{
		ID:   "demo",
		Name: "Demo",
		Pack: "generic",
	})

	clock := fakes.NewFakeClock(time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC))
	p := brain.NewPipeline(ctx, brain.PipelineConfig{
		Store:      st,
		Blobs:      fakes.NewMemoryBlobStore(),
		LLM:        fakes.NewOfflineDemoLLM(),
		Bus:        core.NewInMemoryEventBus(16),
		Clock:      clock,
		Tenants:    brain.NewStoreTenantPolicyLoader(st, nil),
		Knowledge:  brain.NewLocalKnowledgeSource(filepath.Join(tmp, "tenants")),
		PackLoader: func(name string) (*pack.Pack, error) { return pack.LoadFromDir(filepath.Join(tmp, "packs", name)) },
	})

	srv := server.NewWithDeps(config.Config{HTTPHost: "127.0.0.1"}, nil, server.Dependencies{
		Store:    st,
		Pipeline: p,
	})
	h := srv.Handler()

	// 1. GET /api/workflows?tenant=demo defaults to support
	req := httptest.NewRequest(http.MethodGet, "/api/workflows?tenant=demo", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/workflows status=%d body=%s", rec.Code, rec.Body.String())
	}
	var wfResp struct {
		ActiveWorkflow string `json:"active_workflow"`
		Workflows      []struct {
			ID           string `json:"id"`
			InitialState string `json:"initial_state"`
			StatesCount  int    `json:"states_count"`
		} `json:"workflows"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &wfResp); err != nil {
		t.Fatal(err)
	}
	if wfResp.ActiveWorkflow != "support" || len(wfResp.Workflows) != 2 {
		t.Fatalf("unexpected workflows response: %+v", wfResp)
	}

	// 2. Switch workflow to leads via PUT /api/tenants/demo/policy
	polBody, _ := json.Marshal(core.TenantPolicy{
		ID:       "demo",
		Name:     "Demo",
		Pack:     "generic",
		Workflow: "leads",
	})
	putReq := httptest.NewRequest(http.MethodPut, "/api/tenants/demo/policy", bytes.NewReader(polBody))
	putRec := httptest.NewRecorder()
	h.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT /api/tenants/demo/policy status=%d body=%s", putRec.Code, putRec.Body.String())
	}

	// 3. GET /api/sources/simulator/u1/journey?tenant=demo reflects leads (initial_state=entender_dor)
	jReq := httptest.NewRequest(http.MethodGet, "/api/sources/simulator/u1/journey?tenant=demo", nil)
	jRec := httptest.NewRecorder()
	h.ServeHTTP(jRec, jReq)
	if jRec.Code != http.StatusOK {
		t.Fatalf("GET journey status=%d body=%s", jRec.Code, jRec.Body.String())
	}
	var jResp struct {
		Workflow   string         `json:"workflow"`
		Validation map[string]any `json:"validation"`
	}
	if err := json.Unmarshal(jRec.Body.Bytes(), &jResp); err != nil {
		t.Fatal(err)
	}
	if jResp.Workflow != "leads" || jResp.Validation["initial_state"] != "entender_dor" {
		t.Fatalf("expected leads journey (entender_dor), got workflow=%q validation=%+v", jResp.Workflow, jResp.Validation)
	}
}
