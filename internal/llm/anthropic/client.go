package anthropic

import (
	"context"
	"errors"
	"fmt"
	"strings"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"whatshelper/internal/core"
)

var (
	ErrMissingAPIKey = errors.New("anthropic: api key é obrigatória")
	ErrMissingModel  = errors.New("anthropic: modelo é obrigatório na requisição")
	ErrEmptyPrompt   = errors.New("anthropic: prompt não pode ser vazio")
	ErrEmptyResponse = errors.New("anthropic: resposta sem blocos de texto")
)

// Client implementa core.LLM utilizando o SDK oficial da Anthropic.
type Client struct {
	sdk   sdk.Client
	clock core.Clock
}

// New cria um cliente Anthropic validando a chave de API.
func New(apiKey string, clock core.Clock, opts ...option.RequestOption) (*Client, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, ErrMissingAPIKey
	}
	if clock == nil {
		clock = core.SystemClock{}
	}

	reqOpts := make([]option.RequestOption, 0, len(opts)+2)
	reqOpts = append(reqOpts, option.WithoutEnvironmentDefaults(), option.WithAPIKey(apiKey))
	reqOpts = append(reqOpts, opts...)

	return &Client{
		sdk:   sdk.NewClient(reqOpts...),
		clock: clock,
	}, nil
}

// Generate executa uma chamada de mensagens na API da Anthropic usando o modelo configurado no request.
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

	params := sdk.MessageNewParams{
		Model:     sdk.Model(req.Model),
		MaxTokens: int64(maxTokens),
		Messages: []sdk.MessageParam{
			sdk.NewUserMessage(sdk.NewTextBlock(promptText)),
		},
	}

	if strings.TrimSpace(req.System) != "" {
		params.System = []sdk.TextBlockParam{
			{Text: req.System},
		}
	}

	if req.Temperature > 0 {
		params.Temperature = sdk.Float(req.Temperature)
	}

	start := c.clock.Now()
	msg, err := c.sdk.Messages.New(ctx, params)
	durationMs := c.clock.Now().Sub(start).Milliseconds()
	if err != nil {
		return nil, fmt.Errorf("anthropic messages.new falhou: %w", err)
	}

	var out strings.Builder
	for _, block := range msg.Content {
		if block.Type == "text" {
			out.WriteString(block.AsText().Text)
		}
	}

	content := out.String()
	if strings.TrimSpace(content) == "" {
		return nil, ErrEmptyResponse
	}

	promptTokens := int(msg.Usage.InputTokens + msg.Usage.CacheCreationInputTokens + msg.Usage.CacheReadInputTokens)
	completionTokens := int(msg.Usage.OutputTokens)

	return &core.LLMResponse{
		Content:          content,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		DurationMs:       durationMs,
	}, nil
}
