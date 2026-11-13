package openai

import (
	"github.com/openai/openai-go/option"

	"whatshelper/internal/core"
	"whatshelper/internal/llm/openaicompat"
)

const DefaultBaseURL = "https://api.openai.com/v1"

var (
	ErrMissingAPIKey = openaicompat.ErrMissingAPIKey
	ErrMissingModel  = openaicompat.ErrMissingModel
	ErrEmptyPrompt   = openaicompat.ErrEmptyPrompt
	ErrEmptyResponse = openaicompat.ErrEmptyResponse
)

// Client implementa core.LLM para a API oficial da OpenAI (GPT).
type Client = openaicompat.Client

// New cria um cliente OpenAI direto validando a API key.
func New(apiKey, baseURL string, clock core.Clock, opts ...option.RequestOption) (*Client, error) {
	return openaicompat.New(openaicompat.Config{
		ProviderName:   "openai",
		APIKey:         apiKey,
		BaseURL:        baseURL,
		DefaultBaseURL: DefaultBaseURL,
		Clock:          clock,
		Options:        opts,
	})
}
