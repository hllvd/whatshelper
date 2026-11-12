package deepseek

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"

	"whatshelper/internal/core"
)

const DefaultBaseURL = "https://api.deepseek.com"

var (
	ErrMissingAPIKey = errors.New("deepseek: api key é obrigatória")
	ErrMissingModel  = errors.New("deepseek: modelo é obrigatório na requisição")
	ErrEmptyPrompt   = errors.New("deepseek: prompt não pode ser vazio")
	ErrEmptyResponse = errors.New("deepseek: resposta sem escolhas de texto")
)

// Client implementa core.LLM usando o SDK oficial openai-go apontado para a API OpenAI-compatible do DeepSeek.
type Client struct {
	sdk   openai.Client
	clock core.Clock
}

// New cria um cliente DeepSeek validando a chave de API e aplicando baseURL padrão se omitido.
func New(apiKey, baseURL string, clock core.Clock, opts ...option.RequestOption) (*Client, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, ErrMissingAPIKey
	}
	if clock == nil {
		clock = core.SystemClock{}
	}
	resolvedBase := strings.TrimSpace(baseURL)
	if resolvedBase == "" {
		resolvedBase = DefaultBaseURL
	}

	reqOpts := make([]option.RequestOption, 0, len(opts)+2)
	reqOpts = append(reqOpts, option.WithAPIKey(strings.TrimSpace(apiKey)), option.WithBaseURL(resolvedBase))
	reqOpts = append(reqOpts, opts...)

	return &Client{
		sdk:   openai.NewClient(reqOpts...),
		clock: clock,
	}, nil
}

// Generate executa uma chamada Chat Completions no DeepSeek usando o modelo configurado na requisição.
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

	messages := make([]openai.ChatCompletionMessageParamUnion, 0, 2)
	if strings.TrimSpace(req.System) != "" {
		messages = append(messages, openai.SystemMessage(req.System))
	}
	messages = append(messages, openai.UserMessage(promptText))

	params := openai.ChatCompletionNewParams{
		Model:     shared.ChatModel(strings.TrimSpace(req.Model)),
		Messages:  messages,
		MaxTokens: openai.Int(int64(maxTokens)),
	}
	if req.Temperature > 0 {
		params.Temperature = openai.Float(req.Temperature)
	}
	if strings.TrimSpace(req.JSONSchema) != "" {
		jsonObj := shared.NewResponseFormatJSONObjectParam()
		params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONObject: &jsonObj,
		}
	}

	start := c.clock.Now()
	comp, err := c.sdk.Chat.Completions.New(ctx, params)
	durationMs := c.clock.Now().Sub(start).Milliseconds()
	if err != nil {
		return nil, fmt.Errorf("deepseek chat.completions.new falhou: %w", err)
	}
	if comp == nil || len(comp.Choices) == 0 {
		return nil, ErrEmptyResponse
	}

	content := comp.Choices[0].Message.Content
	if strings.TrimSpace(content) == "" {
		return nil, ErrEmptyResponse
	}

	return &core.LLMResponse{
		Content:          content,
		PromptTokens:     int(comp.Usage.PromptTokens),
		CompletionTokens: int(comp.Usage.CompletionTokens),
		DurationMs:       durationMs,
	}, nil
}
