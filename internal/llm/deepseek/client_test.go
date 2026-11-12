package deepseek_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"whatshelper/internal/core"
	"whatshelper/internal/llm/deepseek"
)

func TestDeepSeekClient_GenerateAndErrors(t *testing.T) {
	ctx := context.Background()
	if _, err := deepseek.New("", "", nil); !errors.Is(err, deepseek.ErrMissingAPIKey) {
		t.Fatalf("expected ErrMissingAPIKey, got %v", err)
	}

	var capturedAuth string
	var capturedPath string
	var capturedBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		capturedPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &capturedBody)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-1",
			"object":  "chat.completion",
			"created": 1700000000,
			"model":   "deepseek-chat",
			"choices": []any{
				map[string]any{
					"index": 0,
					"message": map[string]any{
						"role":    "assistant",
						"content": `{"answer":"ok"}`,
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]any{
				"prompt_tokens":     15,
				"completion_tokens": 5,
				"total_tokens":      20,
			},
		})
	}))
	defer srv.Close()

	client, err := deepseek.New("ds-test-key", srv.URL, nil)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	if _, err := client.Generate(ctx, core.LLMRequest{Prompt: "oi"}); !errors.Is(err, deepseek.ErrMissingModel) {
		t.Fatalf("expected ErrMissingModel, got %v", err)
	}
	if _, err := client.Generate(ctx, core.LLMRequest{Model: "deepseek-chat"}); !errors.Is(err, deepseek.ErrEmptyPrompt) {
		t.Fatalf("expected ErrEmptyPrompt, got %v", err)
	}

	resp, err := client.Generate(ctx, core.LLMRequest{
		Stage:       "analysis",
		Model:       "deepseek-chat",
		System:      "Sistema",
		Prompt:      "Pergunta",
		JSONSchema:  `{"type":"object"}`,
		Temperature: 0.3,
		MaxTokens:   400,
	})
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if resp.Content != `{"answer":"ok"}` || resp.PromptTokens != 15 || resp.CompletionTokens != 5 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if capturedAuth != "Bearer ds-test-key" {
		t.Fatalf("expected Bearer auth header, got %q", capturedAuth)
	}
	if capturedPath != "/chat/completions" {
		t.Fatalf("expected /chat/completions path, got %q", capturedPath)
	}
	rf, _ := capturedBody["response_format"].(map[string]any)
	if rf["type"] != "json_object" {
		t.Fatalf("expected response_format.type=json_object, got %+v", rf)
	}
}
