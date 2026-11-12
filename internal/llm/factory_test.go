package llm_test

import (
	"context"
	"errors"
	"testing"

	"whatshelper/internal/config"
	"whatshelper/internal/core"
	"whatshelper/internal/llm"
)

func TestNewFromConfig_Providers(t *testing.T) {
	ctx := context.Background()

	clientFake, a, r, n, err := llm.NewFromConfig(ctx, config.Config{LLMProvider: "fake"}, nil)
	if err != nil || clientFake == nil || a == "" || r == "" || n == "" {
		t.Fatalf("expected fake provider initialized, got client=%v models=%s/%s/%s err=%v", clientFake, a, r, n, err)
	}

	if _, _, _, _, err := llm.NewFromConfig(ctx, config.Config{LLMProvider: "gemini"}, nil); !errors.Is(err, config.ErrMissingGeminiKey) {
		t.Fatalf("expected ErrMissingGeminiKey, got %v", err)
	}

	if _, _, _, _, err := llm.NewFromConfig(ctx, config.Config{
		LLMProvider:    "gemini",
		GeminiAPIKey:   "gem-k",
		AnalysisModel:  "g-a",
		ResponseModel:  "g-r",
		NudgeModel:     "g-n",
	}, core.SystemClock{}); err != nil {
		t.Fatalf("expected gemini factory initialization success, got %v", err)
	}

	if _, _, _, _, err := llm.NewFromConfig(ctx, config.Config{
		LLMProvider:    "deepseek",
		DeepSeekAPIKey: "ds-k",
		AnalysisModel:  "d-a",
		ResponseModel:  "d-r",
		NudgeModel:     "d-n",
	}, core.SystemClock{}); err != nil {
		t.Fatalf("expected deepseek factory initialization success, got %v", err)
	}
}
