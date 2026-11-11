//go:build llm_live

package anthropic_test

import (
	"context"
	"os"
	"testing"

	"whatshelper/internal/core"
	"whatshelper/internal/llm/anthropic"
)

func TestAnthropicLive_Generate(t *testing.T) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	model := os.Getenv("MODEL_RESPONSE")
	if apiKey == "" || model == "" {
		t.Skip("ANTHROPIC_API_KEY e MODEL_RESPONSE são necessários para o teste live")
	}

	client, err := anthropic.New(apiKey, nil)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	resp, err := client.Generate(context.Background(), core.LLMRequest{
		Stage:     "response",
		Model:     model,
		Prompt:    "Responda apenas com a palavra: OK",
		MaxTokens: 32,
	})
	if err != nil {
		t.Fatalf("live Generate failed: %v", err)
	}
	if resp.Content == "" {
		t.Fatalf("expected non-empty content")
	}
	t.Logf("Live response: %q (tokens: %d/%d)", resp.Content, resp.PromptTokens, resp.CompletionTokens)
}
