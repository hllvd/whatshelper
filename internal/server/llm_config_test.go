package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"whatshelper/internal/brain"
	"whatshelper/internal/config"
	"whatshelper/internal/core"
	"whatshelper/internal/core/fakes"
	"whatshelper/internal/llm"
	"whatshelper/internal/server"
)

func TestLLMConfigAPI_PresetsGetAndPutOnTheFly(t *testing.T) {
	ctx := context.Background()
	store := fakes.NewMemoryStore()
	if err := brain.SeedTenantPolicy(ctx, store, core.TenantPolicy{
		ID:   "demo",
		Name: "Demo",
		Pack: "sample",
	}); err != nil {
		t.Fatal(err)
	}

	router := llm.NewRouterFromMap("openai", map[string]core.LLM{
		"openai":    fakes.NewOfflineDemoLLM(),
		"anthropic": fakes.NewOfflineDemoLLM(),
		"fake":      fakes.NewOfflineDemoLLM(),
	}, map[string]core.StageLLMConfig{
		"analysis": {Provider: "openai", Model: "gpt-4.1"},
		"response": {Provider: "openai", Model: "gpt-4.1-mini"},
		"vision":   {Provider: "openai", Model: "gpt-4.1-mini"},
		"audio":    {Provider: "openai", Model: "whisper-1"},
	})

	cfg := config.Config{
		HTTPHost:    "127.0.0.1",
		LLMProvider: "openai",
	}
	srv := server.NewWithDeps(cfg, nil, server.Dependencies{
		Store:     store,
		LLMRouter: router,
	})
	handler := srv.Handler()

	// 1. GET /api/llm/presets
	req := httptest.NewRequest(http.MethodGet, "/api/llm/presets", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/llm/presets status=%d body=%s", rec.Code, rec.Body.String())
	}
	var presetsResp struct {
		DefaultProvider string            `json:"default_provider"`
		ActiveProviders []string          `json:"active_providers"`
		Presets         []llm.ModelPreset `json:"presets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &presetsResp); err != nil {
		t.Fatal(err)
	}
	if presetsResp.DefaultProvider != "openai" || len(presetsResp.ActiveProviders) != 3 || len(presetsResp.Presets) == 0 {
		t.Fatalf("unexpected presets response: %+v", presetsResp)
	}

	// 2. PUT /api/llm/config?tenant=demo (on-the-fly override)
	putBody, _ := json.Marshal(map[string]any{
		"overrides": map[string]core.StageLLMConfig{
			"analysis": {Provider: "anthropic", Model: "claude-sonnet-4-20250514"},
			"vision":   {Provider: "openai", Model: "gpt-4.1"},
		},
	})
	reqPut := httptest.NewRequest(http.MethodPut, "/api/llm/config?tenant=demo", bytes.NewReader(putBody))
	recPut := httptest.NewRecorder()
	handler.ServeHTTP(recPut, reqPut)
	if recPut.Code != http.StatusOK {
		t.Fatalf("PUT /api/llm/config status=%d body=%s", recPut.Code, recPut.Body.String())
	}

	var cfgResp struct {
		Effective map[string]core.StageLLMConfig `json:"effective"`
		Overrides map[string]core.StageLLMConfig `json:"overrides"`
	}
	if err := json.Unmarshal(recPut.Body.Bytes(), &cfgResp); err != nil {
		t.Fatal(err)
	}
	if cfgResp.Effective["analysis"].Provider != "anthropic" || cfgResp.Effective["analysis"].Model != "claude-sonnet-4-20250514" {
		t.Fatalf("expected analysis override to anthropic, got %+v", cfgResp.Effective["analysis"])
	}
	if cfgResp.Effective["response"].Provider != "openai" || cfgResp.Effective["response"].Model != "gpt-4.1-mini" {
		t.Fatalf("expected response default to openai gpt-4.1-mini, got %+v", cfgResp.Effective["response"])
	}

	// 3. Invalid provider rejected with 400
	badBody, _ := json.Marshal(map[string]any{
		"overrides": map[string]core.StageLLMConfig{
			"analysis": {Provider: "unconfigured-provider", Model: "x"},
		},
	})
	reqBad := httptest.NewRequest(http.MethodPut, "/api/llm/config?tenant=demo", bytes.NewReader(badBody))
	recBad := httptest.NewRecorder()
	handler.ServeHTTP(recBad, reqBad)
	if recBad.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unconfigured provider, got %d", recBad.Code)
	}
}
