package llm_test

import (
	"context"
	"testing"

	"whatshelper/internal/config"
	"whatshelper/internal/core"
	"whatshelper/internal/llm"
)

type stubLLM struct {
	name      string
	lastReq   core.LLMRequest
	respText  string
}

func (s *stubLLM) Generate(_ context.Context, req core.LLMRequest) (*core.LLMResponse, error) {
	s.lastReq = req
	return &core.LLMResponse{
		Content:  s.respText,
		Provider: s.name,
		Model:    req.Model,
	}, nil
}

func TestDynamicRouter_ResolveAndDispatch(t *testing.T) {
	openaiStub := &stubLLM{name: "openai", respText: `{"ok":"openai"}`}
	geminiStub := &stubLLM{name: "gemini", respText: `{"ok":"gemini"}`}

	router := llm.NewRouterFromMap("openai", map[string]core.LLM{
		"openai": openaiStub,
		"gemini": geminiStub,
	}, map[string]core.StageLLMConfig{
		"analysis": {Provider: "openai", Model: "gpt-4.1"},
		"response": {Provider: "openai", Model: "gpt-4.1-mini"},
		"vision":   {Provider: "openai", Model: "gpt-4.1-mini"},
	})

	// 1. Default resolution without overrides
	resDefault := router.Resolve("analysis", nil)
	if resDefault.Provider != "openai" || resDefault.Model != "gpt-4.1" {
		t.Fatalf("unexpected default resolve: %+v", resDefault)
	}

	// 2. On-the-fly override per stage (e.g. vision routed to gemini)
	overrides := map[string]core.StageLLMConfig{
		"vision": {Provider: "gemini", Model: "gemini-2.5-flash"},
	}
	resVision := router.Resolve("vision", overrides)
	if resVision.Provider != "gemini" || resVision.Model != "gemini-2.5-flash" {
		t.Fatalf("unexpected override resolve: %+v", resVision)
	}

	// 3. Dispatch using resolved provider/model
	resp, err := router.Generate(context.Background(), core.LLMRequest{
		Stage:    "vision",
		Provider: resVision.Provider,
		Model:    resVision.Model,
		Prompt:   "describe",
	})
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if resp.Provider != "gemini" || resp.Model != "gemini-2.5-flash" || geminiStub.lastReq.Prompt != "describe" {
		t.Fatalf("unexpected dispatch result: resp=%+v lastReq=%+v", resp, geminiStub.lastReq)
	}

	// 4. Unconfigured provider returns explicit error
	if _, err := router.Generate(context.Background(), core.LLMRequest{
		Stage:    "analysis",
		Provider: "anthropic",
		Model:    "claude-sonnet-4-20250514",
	}); err == nil {
		t.Fatal("expected error for unconfigured provider")
	}
}

func TestNewRouterFromConfig_MultiProviderInitAndPresets(t *testing.T) {
	cfg := config.Config{
		LLMProvider:      "openai",
		OpenAIAPIKey:     "sk-openai-test",
		OpenRouterAPIKey: "sk-or-test",
		AnalysisModel:    "gpt-4.1",
		ResponseModel:    "gpt-4.1-mini",
		NudgeModel:       "gpt-4.1-nano",
		VisionModel:      "gpt-4.1-mini",
		AudioModel:       "gpt-4o-transcribe",
	}

	router, err := llm.NewRouterFromConfig(context.Background(), cfg, core.SystemClock{})
	if err != nil {
		t.Fatalf("NewRouterFromConfig failed: %v", err)
	}

	active := router.ActiveProviders()
	hasOpenAI, hasOpenRouter, hasFake := false, false, false
	for _, p := range active {
		switch p {
		case "openai":
			hasOpenAI = true
		case "openrouter":
			hasOpenRouter = true
		case "fake":
			hasFake = true
		}
	}
	if !hasOpenAI || !hasOpenRouter || !hasFake {
		t.Fatalf("expected openai, openrouter and fake active; got %v", active)
	}

	if d := router.Resolve("vision", nil); d.Provider != "openai" || d.Model != "gpt-4.1-mini" {
		t.Fatalf("unexpected vision default: %+v", d)
	}
	if d := router.Resolve("audio", nil); d.Provider != "openai" || d.Model != "gpt-4o-transcribe" {
		t.Fatalf("unexpected audio default: %+v", d)
	}

	presets := llm.Presets()
	if len(presets) < 10 {
		t.Fatalf("expected >= 10 presets, got %d", len(presets))
	}
}
