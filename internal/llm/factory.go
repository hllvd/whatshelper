package llm

import (
	"context"
	"fmt"

	"whatshelper/internal/config"
	"whatshelper/internal/core"
	"whatshelper/internal/core/fakes"
	"whatshelper/internal/llm/anthropic"
	"whatshelper/internal/llm/deepseek"
	"whatshelper/internal/llm/gemini"
	"whatshelper/internal/llm/openrouter"
)

// NewFromConfig constrói o adapter core.LLM ativo e os nomes de modelo por estágio a partir de config.Config.
func NewFromConfig(ctx context.Context, cfg config.Config, clock core.Clock) (core.LLM, string, string, string, error) {
	if err := cfg.Validate(); err != nil {
		return nil, "", "", "", err
	}

	switch cfg.LLMProvider {
	case "anthropic":
		client, err := anthropic.New(cfg.AnthropicAPIKey, clock)
		if err != nil {
			return nil, "", "", "", err
		}
		return client, cfg.AnalysisModel, cfg.ResponseModel, cfg.NudgeModel, nil
	case "gemini":
		client, err := gemini.New(ctx, cfg.GeminiAPIKey, clock)
		if err != nil {
			return nil, "", "", "", err
		}
		return client, cfg.AnalysisModel, cfg.ResponseModel, cfg.NudgeModel, nil
	case "deepseek":
		client, err := deepseek.New(cfg.DeepSeekAPIKey, cfg.DeepSeekBaseURL, clock)
		if err != nil {
			return nil, "", "", "", err
		}
		return client, cfg.AnalysisModel, cfg.ResponseModel, cfg.NudgeModel, nil
	case "openrouter":
		client, err := openrouter.New(cfg.OpenRouterAPIKey, cfg.OpenRouterBaseURL, cfg.OpenRouterSiteURL, cfg.OpenRouterAppName, clock)
		if err != nil {
			return nil, "", "", "", err
		}
		return client, cfg.AnalysisModel, cfg.ResponseModel, cfg.NudgeModel, nil
	case "fake":
		analysisModel := firstNonEmpty(cfg.AnalysisModel, "fake-analysis")
		responseModel := firstNonEmpty(cfg.ResponseModel, "fake-response")
		nudgeModel := firstNonEmpty(cfg.NudgeModel, "fake-nudge")
		return fakes.NewOfflineDemoLLM(), analysisModel, responseModel, nudgeModel, nil
	default:
		return nil, "", "", "", fmt.Errorf("%w: %q", config.ErrInvalidLLMProvider, cfg.LLMProvider)
	}
}

func firstNonEmpty(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}
