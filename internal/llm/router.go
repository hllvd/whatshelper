package llm

import (
	"context"
	"fmt"
	"sort"

	"whatshelper/internal/config"
	"whatshelper/internal/core"
	"whatshelper/internal/core/fakes"
	"whatshelper/internal/llm/anthropic"
	"whatshelper/internal/llm/deepseek"
	"whatshelper/internal/llm/gemini"
	"whatshelper/internal/llm/openai"
	"whatshelper/internal/llm/openrouter"
)

// ModelPreset descreve um modelo recomendado por provedor e suas capacidades.
type ModelPreset struct {
	Provider     string   `json:"provider"`
	Model        string   `json:"model"`
	Label        string   `json:"label"`
	Capabilities []string `json:"capabilities"`
}

// DynamicRouter roteia chamadas core.LLM entre múltiplos provedores configurados,
// aplicando defaults por estágio e overrides em tempo real sem reinício.
type DynamicRouter struct {
	defaultProvider string
	providers       map[string]core.LLM
	stageDefaults   map[string]core.StageLLMConfig
}

// NewRouterFromConfig inicializa o DynamicRouter com o provedor principal validado
// e registra automaticamente quaisquer outros provedores cujas chaves estejam no ambiente.
func NewRouterFromConfig(ctx context.Context, cfg config.Config, clock core.Clock) (*DynamicRouter, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	providers := make(map[string]core.LLM)
	providers["fake"] = fakes.NewOfflineDemoLLM()

	if cfg.OpenAIAPIKey != "" {
		c, err := openai.New(cfg.OpenAIAPIKey, cfg.OpenAIBaseURL, clock)
		if err != nil && cfg.LLMProvider == "openai" {
			return nil, err
		}
		if err == nil {
			providers["openai"] = c
		}
	}
	if cfg.OpenRouterAPIKey != "" {
		c, err := openrouter.New(cfg.OpenRouterAPIKey, cfg.OpenRouterBaseURL, cfg.OpenRouterSiteURL, cfg.OpenRouterAppName, clock)
		if err != nil && cfg.LLMProvider == "openrouter" {
			return nil, err
		}
		if err == nil {
			providers["openrouter"] = c
		}
	}
	if cfg.AnthropicAPIKey != "" {
		c, err := anthropic.New(cfg.AnthropicAPIKey, clock)
		if err != nil && cfg.LLMProvider == "anthropic" {
			return nil, err
		}
		if err == nil {
			providers["anthropic"] = c
		}
	}
	if cfg.GeminiAPIKey != "" {
		c, err := gemini.New(ctx, cfg.GeminiAPIKey, clock)
		if err != nil && cfg.LLMProvider == "gemini" {
			return nil, err
		}
		if err == nil {
			providers["gemini"] = c
		}
	}
	if cfg.DeepSeekAPIKey != "" {
		c, err := deepseek.New(cfg.DeepSeekAPIKey, cfg.DeepSeekBaseURL, clock)
		if err != nil && cfg.LLMProvider == "deepseek" {
			return nil, err
		}
		if err == nil {
			providers["deepseek"] = c
		}
	}

	if _, ok := providers[cfg.LLMProvider]; !ok {
		return nil, fmt.Errorf("%w: %q", config.ErrInvalidLLMProvider, cfg.LLMProvider)
	}

	analysisModel := cfg.AnalysisModel
	responseModel := cfg.ResponseModel
	nudgeModel := cfg.NudgeModel
	if cfg.LLMProvider == "fake" {
		analysisModel = firstNonEmpty(analysisModel, "fake-analysis")
		responseModel = firstNonEmpty(responseModel, "fake-response")
		nudgeModel = firstNonEmpty(nudgeModel, "fake-nudge")
	}

	stageDefaults := map[string]core.StageLLMConfig{
		"analysis": {Provider: cfg.LLMProvider, Model: analysisModel},
		"response": {Provider: cfg.LLMProvider, Model: responseModel},
		"nudge":    {Provider: cfg.LLMProvider, Model: nudgeModel},
		"learning": {Provider: cfg.LLMProvider, Model: firstNonEmpty(cfg.LearningModel, analysisModel)},
		"vision":   {Provider: cfg.LLMProvider, Model: firstNonEmpty(cfg.VisionModel, analysisModel)},
		"audio":    {Provider: cfg.LLMProvider, Model: firstNonEmpty(cfg.AudioModel, cfg.TranscriptionModel)},
	}

	return &DynamicRouter{
		defaultProvider: cfg.LLMProvider,
		providers:       providers,
		stageDefaults:   stageDefaults,
	}, nil
}

// NewRouterFromMap cria um DynamicRouter explícito (útil para testes unitários).
func NewRouterFromMap(defaultProvider string, providers map[string]core.LLM, stageDefaults map[string]core.StageLLMConfig) *DynamicRouter {
	pCopy := make(map[string]core.LLM, len(providers))
	for k, v := range providers {
		pCopy[k] = v
	}
	dCopy := make(map[string]core.StageLLMConfig, len(stageDefaults))
	for k, v := range stageDefaults {
		dCopy[k] = v
	}
	return &DynamicRouter{
		defaultProvider: defaultProvider,
		providers:       pCopy,
		stageDefaults:   dCopy,
	}
}

// Resolve calcula provider e model efetivos para um estágio considerando overrides do tenant e defaults do .env.
func (r *DynamicRouter) Resolve(stage string, overrides map[string]core.StageLLMConfig) core.StageLLMConfig {
	def := r.stageDefaults[stage]
	if def.Provider == "" {
		def.Provider = r.defaultProvider
	}
	if overrides == nil {
		return def
	}
	ov, ok := overrides[stage]
	if !ok {
		return def
	}
	return core.StageLLMConfig{
		Provider: firstNonEmpty(ov.Provider, def.Provider),
		Model:    firstNonEmpty(ov.Model, def.Model),
	}
}

// Generate despacha a chamada para o provedor especificado em req.Provider (ou default do estágio).
func (r *DynamicRouter) Generate(ctx context.Context, req core.LLMRequest) (*core.LLMResponse, error) {
	resolved := r.Resolve(req.Stage, nil)
	provider := firstNonEmpty(req.Provider, resolved.Provider)
	model := firstNonEmpty(req.Model, resolved.Model)

	client, ok := r.providers[provider]
	if !ok || client == nil {
		return nil, fmt.Errorf("llm provider %q is not configured", provider)
	}

	req.Provider = provider
	req.Model = model
	resp, err := client.Generate(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, nil
	}
	if resp.Provider == "" {
		resp.Provider = provider
	}
	if resp.Model == "" {
		resp.Model = model
	}
	return resp, nil
}

// ActiveProviders retorna a lista ordenada de provedores configurados no router.
func (r *DynamicRouter) ActiveProviders() []string {
	out := make([]string, 0, len(r.providers))
	for k := range r.providers {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// DefaultProvider retorna o provedor padrão configurado no .env.
func (r *DynamicRouter) DefaultProvider() string {
	return r.defaultProvider
}

// StageDefaults retorna uma cópia dos defaults por estágio.
func (r *DynamicRouter) StageDefaults() map[string]core.StageLLMConfig {
	out := make(map[string]core.StageLLMConfig, len(r.stageDefaults))
	for k, v := range r.stageDefaults {
		out[k] = v
	}
	return out
}

// Presets retorna os modelos recomendados por provedor e suas capacidades suportadas.
func Presets() []ModelPreset {
	return []ModelPreset{
		{Provider: "openai", Model: "gpt-4.1", Label: "GPT-4.1 (OpenAI)", Capabilities: []string{"text", "thinking", "vision"}},
		{Provider: "openai", Model: "gpt-4.1-mini", Label: "GPT-4.1 Mini (OpenAI)", Capabilities: []string{"text", "thinking", "vision"}},
		{Provider: "openai", Model: "gpt-4.1-nano", Label: "GPT-4.1 Nano (OpenAI)", Capabilities: []string{"text", "vision"}},
		{Provider: "openai", Model: "o4-mini", Label: "o4-mini (OpenAI Reasoning)", Capabilities: []string{"text", "thinking", "vision"}},
		{Provider: "openai", Model: "gpt-4o-transcribe", Label: "GPT-4o Transcribe (OpenAI Audio)", Capabilities: []string{"audio"}},
		{Provider: "openai", Model: "whisper-1", Label: "Whisper-1 (OpenAI Audio)", Capabilities: []string{"audio"}},

		{Provider: "openrouter", Model: "openai/gpt-4.1", Label: "GPT-4.1 via OpenRouter", Capabilities: []string{"text", "thinking", "vision"}},
		{Provider: "openrouter", Model: "anthropic/claude-sonnet-4", Label: "Claude Sonnet 4 via OpenRouter", Capabilities: []string{"text", "thinking", "vision"}},
		{Provider: "openrouter", Model: "google/gemini-2.5-flash", Label: "Gemini 2.5 Flash via OpenRouter", Capabilities: []string{"text", "thinking", "vision", "audio"}},
		{Provider: "openrouter", Model: "google/gemini-2.5-pro", Label: "Gemini 2.5 Pro via OpenRouter", Capabilities: []string{"text", "thinking", "vision"}},
		{Provider: "openrouter", Model: "deepseek/deepseek-r1", Label: "DeepSeek R1 via OpenRouter", Capabilities: []string{"text", "thinking"}},

		{Provider: "anthropic", Model: "claude-sonnet-4-20250514", Label: "Claude Sonnet 4 (Anthropic)", Capabilities: []string{"text", "thinking", "vision"}},
		{Provider: "anthropic", Model: "claude-haiku-4-5-20251001", Label: "Claude Haiku 4.5 (Anthropic)", Capabilities: []string{"text", "vision"}},

		{Provider: "gemini", Model: "gemini-2.5-flash", Label: "Gemini 2.5 Flash (Google)", Capabilities: []string{"text", "thinking", "vision", "audio"}},
		{Provider: "gemini", Model: "gemini-2.5-pro", Label: "Gemini 2.5 Pro (Google)", Capabilities: []string{"text", "thinking", "vision"}},

		{Provider: "deepseek", Model: "deepseek-chat", Label: "DeepSeek Chat", Capabilities: []string{"text"}},
		{Provider: "deepseek", Model: "deepseek-reasoner", Label: "DeepSeek Reasoner (R1)", Capabilities: []string{"text", "thinking"}},

		{Provider: "fake", Model: "fake-analysis", Label: "Offline Fake (Testes)", Capabilities: []string{"text", "thinking", "vision"}},
	}
}
