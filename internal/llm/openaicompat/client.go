package openaicompat

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"

	"whatshelper/internal/core"
)

var (
	ErrMissingAPIKey = errors.New("api key é obrigatória")
	ErrMissingModel  = errors.New("modelo é obrigatório na requisição")
	ErrEmptyPrompt   = errors.New("prompt não pode ser vazio")
	ErrEmptyResponse = errors.New("resposta sem escolhas de texto")
)

// Config parametriza um cliente core.LLM baseado na API OpenAI Chat Completions.
type Config struct {
	ProviderName   string
	APIKey         string
	BaseURL        string
	DefaultBaseURL string
	Headers        map[string]string
	Clock          core.Clock
	Options        []option.RequestOption
}

// Client implementa core.LLM para provedores compatíveis com OpenAI Chat Completions (DeepSeek, OpenRouter).
type Client struct {
	provider string
	sdk      openai.Client
	clock    core.Clock
}

// New valida a configuração e constrói um Client sobre github.com/openai/openai-go.
func New(cfg Config) (*Client, error) {
	provider := strings.TrimSpace(cfg.ProviderName)
	if provider == "" {
		provider = "openai-compat"
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("%s: %w", provider, ErrMissingAPIKey)
	}
	clock := cfg.Clock
	if clock == nil {
		clock = core.SystemClock{}
	}
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = strings.TrimSpace(cfg.DefaultBaseURL)
	}

	reqOpts := make([]option.RequestOption, 0, len(cfg.Options)+len(cfg.Headers)+2)
	reqOpts = append(reqOpts, option.WithAPIKey(strings.TrimSpace(cfg.APIKey)))
	if baseURL != "" {
		reqOpts = append(reqOpts, option.WithBaseURL(baseURL))
	}
	for k, v := range cfg.Headers {
		if strings.TrimSpace(k) != "" && strings.TrimSpace(v) != "" {
			reqOpts = append(reqOpts, option.WithHeader(strings.TrimSpace(k), strings.TrimSpace(v)))
		}
	}
	reqOpts = append(reqOpts, cfg.Options...)

	return &Client{
		provider: provider,
		sdk:      openai.NewClient(reqOpts...),
		clock:    clock,
	}, nil
}

// Generate executa Chat.Completions.New mapeando System, Prompt, JSON object mode e contagem de tokens.
func (c *Client) Generate(ctx context.Context, req core.LLMRequest) (*core.LLMResponse, error) {
	if strings.TrimSpace(req.Model) == "" {
		return nil, fmt.Errorf("%s: %w", c.provider, ErrMissingModel)
	}
	promptText := strings.TrimSpace(req.Prompt)
	if promptText == "" {
		return nil, fmt.Errorf("%s: %w", c.provider, ErrEmptyPrompt)
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 2048
	}

	messages := make([]openai.ChatCompletionMessageParamUnion, 0, 2)
	if strings.TrimSpace(req.System) != "" {
		messages = append(messages, openai.SystemMessage(req.System))
	}
	if len(req.MediaParts) > 0 {
		parts := make([]openai.ChatCompletionContentPartUnionParam, 0, 1+len(req.MediaParts))
		parts = append(parts, openai.TextContentPart(promptText))
		for _, mp := range req.MediaParts {
			if len(mp.Data) == 0 {
				continue
			}
			mime := strings.TrimSpace(mp.MimeType)
			if mime == "" {
				mime = "image/jpeg"
			}
			dataURI := fmt.Sprintf("data:%s;base64,%s", mime, base64.StdEncoding.EncodeToString(mp.Data))
			parts = append(parts, openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{
				URL: dataURI,
			}))
		}
		messages = append(messages, openai.UserMessage(parts))
	} else {
		messages = append(messages, openai.UserMessage(promptText))
	}

	params := openai.ChatCompletionNewParams{
		Model:     shared.ChatModel(strings.TrimSpace(req.Model)),
		Messages:  messages,
		MaxTokens: openai.Int(int64(maxTokens)),
	}
	if req.Temperature > 0 {
		params.Temperature = openai.Float(req.Temperature)
	}
	if strings.TrimSpace(req.JSONSchema) != "" {
		var schemaMap map[string]any
		if (c.provider == "openai" || c.provider == "openrouter") && json.Unmarshal([]byte(req.JSONSchema), &schemaMap) == nil && len(schemaMap) > 0 {
			params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
				OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{
					JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{
						Name:   "analysis_output",
						Schema: schemaMap,
						Strict: openai.Bool(false),
					},
				},
			}
		} else {
			jsonObj := shared.NewResponseFormatJSONObjectParam()
			params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
				OfJSONObject: &jsonObj,
			}
		}
	}

	start := c.clock.Now()
	comp, err := c.sdk.Chat.Completions.New(ctx, params)
	durationMs := c.clock.Now().Sub(start).Milliseconds()
	if err != nil {
		return nil, fmt.Errorf("%s chat.completions.new falhou: %w", c.provider, err)
	}
	if comp == nil || len(comp.Choices) == 0 {
		return nil, fmt.Errorf("%s: %w", c.provider, ErrEmptyResponse)
	}

	content := comp.Choices[0].Message.Content
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("%s: %w", c.provider, ErrEmptyResponse)
	}

	return &core.LLMResponse{
		Content:          content,
		Provider:         c.provider,
		Model:            strings.TrimSpace(req.Model),
		PromptTokens:     int(comp.Usage.PromptTokens),
		CompletionTokens: int(comp.Usage.CompletionTokens),
		DurationMs:       durationMs,
	}, nil
}
