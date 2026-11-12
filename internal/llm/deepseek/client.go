package deepseek

import (
	"github.com/openai/openai-go/option"

	"whatshelper/internal/core"
	"whatshelper/internal/llm/openaicompat"
)

const DefaultBaseURL = "https://api.deepseek.com"

var (
	ErrMissingAPIKey = openaicompat.ErrMissingAPIKey
	ErrMissingModel  = openaicompat.ErrMissingModel
	ErrEmptyPrompt   = openaicompat.ErrEmptyPrompt
	ErrEmptyResponse = openaicompat.ErrEmptyResponse
)

// Client implementa core.LLM usando o adaptador OpenAI-compatible apontado para a API do DeepSeek.
type Client = openaicompat.Client

// New cria um cliente DeepSeek validando a chave de API e aplicando DefaultBaseURL se omitido.
func New(apiKey, baseURL string, clock core.Clock, opts ...option.RequestOption) (*Client, error) {
	return openaicompat.New(openaicompat.Config{
		ProviderName:   "deepseek",
		APIKey:         apiKey,
		BaseURL:        baseURL,
		DefaultBaseURL: DefaultBaseURL,
		Clock:          clock,
		Options:        opts,
	})
}
