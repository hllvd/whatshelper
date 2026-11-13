package gemini

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"google.golang.org/genai"

	"whatshelper/internal/core"
)

var (
	ErrMissingAPIKey = errors.New("gemini: api key é obrigatória")
	ErrMissingModel  = errors.New("gemini: modelo é obrigatório na requisição")
	ErrEmptyPrompt   = errors.New("gemini: prompt não pode ser vazio")
	ErrEmptyResponse = errors.New("gemini: resposta sem conteúdo de texto")
)

// ClientOption permite customizar o cliente HTTP ou endpoint (útil para testes).
type ClientOption func(*genai.ClientConfig)

// WithBaseURL sobrescreve a URL base da API Gemini.
func WithBaseURL(baseURL string) ClientOption {
	return func(cc *genai.ClientConfig) {
		if strings.TrimSpace(baseURL) != "" {
			cc.HTTPOptions.BaseURL = strings.TrimSpace(baseURL)
		}
	}
}

// WithHTTPClient injeta um http.Client customizado no SDK oficial do Gemini.
func WithHTTPClient(hc *http.Client) ClientOption {
	return func(cc *genai.ClientConfig) {
		if hc != nil {
			cc.HTTPClient = hc
		}
	}
}

// Client implementa core.LLM utilizando o SDK oficial google.golang.org/genai.
type Client struct {
	sdk   *genai.Client
	clock core.Clock
}

// New inicializa um cliente Gemini validando a chave de API.
func New(ctx context.Context, apiKey string, clock core.Clock, opts ...ClientOption) (*Client, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, ErrMissingAPIKey
	}
	if clock == nil {
		clock = core.SystemClock{}
	}

	cc := &genai.ClientConfig{
		APIKey:  strings.TrimSpace(apiKey),
		Backend: genai.BackendGeminiAPI,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(cc)
		}
	}

	sdkClient, err := genai.NewClient(ctx, cc)
	if err != nil {
		return nil, fmt.Errorf("gemini: falha ao inicializar SDK: %w", err)
	}
	return &Client{
		sdk:   sdkClient,
		clock: clock,
	}, nil
}

// Generate executa uma chamada GenerateContent no SDK oficial google.golang.org/genai.
func (c *Client) Generate(ctx context.Context, req core.LLMRequest) (*core.LLMResponse, error) {
	if strings.TrimSpace(req.Model) == "" {
		return nil, ErrMissingModel
	}
	promptText := strings.TrimSpace(req.Prompt)
	if promptText == "" {
		return nil, ErrEmptyPrompt
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 2048
	}

	genCfg := &genai.GenerateContentConfig{
		MaxOutputTokens: int32(maxTokens),
	}
	if req.Temperature > 0 {
		temp := float32(req.Temperature)
		genCfg.Temperature = &temp
	}
	if strings.TrimSpace(req.System) != "" {
		genCfg.SystemInstruction = &genai.Content{
			Parts: []*genai.Part{{Text: req.System}},
		}
	}
	if strings.TrimSpace(req.JSONSchema) != "" {
		genCfg.ResponseMIMEType = "application/json"
	}
	if effort, ok := core.NormalizeReasoningEffort(req.ReasoningEffort); ok && effort != "" {
		if budget, hasBudget := geminiThinkingBudget(effort); hasBudget {
			genCfg.ThinkingConfig = &genai.ThinkingConfig{
				ThinkingBudget: &budget,
			}
		}
	}

	start := c.clock.Now()
	resp, err := c.sdk.Models.GenerateContent(ctx, strings.TrimSpace(req.Model), genai.Text(promptText), genCfg)
	durationMs := c.clock.Now().Sub(start).Milliseconds()
	if err != nil {
		return nil, fmt.Errorf("gemini models.generateContent falhou: %w", err)
	}
	if resp == nil {
		return nil, ErrEmptyResponse
	}

	content := resp.Text()
	if strings.TrimSpace(content) == "" {
		return nil, ErrEmptyResponse
	}

	var promptTokens, completionTokens int
	if resp.UsageMetadata != nil {
		promptTokens = int(resp.UsageMetadata.PromptTokenCount)
		completionTokens = int(resp.UsageMetadata.CandidatesTokenCount)
	}

	return &core.LLMResponse{
		Content:          content,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		DurationMs:       durationMs,
	}, nil
}

func geminiThinkingBudget(effort string) (int32, bool) {
	switch effort {
	case "none":
		return 0, true
	case "minimal", "low":
		return 1024, true
	case "medium":
		return 4096, true
	case "high":
		return 16384, true
	default:
		return 0, false
	}
}
