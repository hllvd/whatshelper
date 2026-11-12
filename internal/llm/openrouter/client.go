package openrouter

import (
	"github.com/openai/openai-go/option"

	"whatshelper/internal/core"
	"whatshelper/internal/llm/openaicompat"
)

const DefaultBaseURL = "https://openrouter.ai/api/v1"

var (
	ErrMissingAPIKey = openaicompat.ErrMissingAPIKey
	ErrMissingModel  = openaicompat.ErrMissingModel
	ErrEmptyPrompt   = openaicompat.ErrEmptyPrompt
	ErrEmptyResponse = openaicompat.ErrEmptyResponse
)

// Client implementa core.LLM usando o endpoint OpenAI-compatible do OpenRouter.
type Client = openaicompat.Client

// New cria um cliente OpenRouter validando a chave de API e anexando headers opcionais de identificação.
func New(apiKey, baseURL, siteURL, appName string, clock core.Clock, opts ...option.RequestOption) (*Client, error) {
	headers := map[string]string{
		"HTTP-Referer": siteURL,
		"X-Title":      appName,
	}
	return openaicompat.New(openaicompat.Config{
		ProviderName:   "openrouter",
		APIKey:         apiKey,
		BaseURL:        baseURL,
		DefaultBaseURL: DefaultBaseURL,
		Headers:        headers,
		Clock:          clock,
		Options:        opts,
	})
}
