package openrouter_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"whatshelper/internal/core"
	"whatshelper/internal/llm/openrouter"
)

func TestOpenRouterClient_GenerateHeadersAndErrors(t *testing.T) {
	ctx := context.Background()
	if _, err := openrouter.New("", "", "", "", nil); !errors.Is(err, openrouter.ErrMissingAPIKey) {
		t.Fatalf("expected ErrMissingAPIKey, got %v", err)
	}

	var capturedAuth, capturedReferer, capturedTitle, capturedPath string
	var capturedBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		capturedReferer = r.Header.Get("HTTP-Referer")
		capturedTitle = r.Header.Get("X-Title")
		capturedPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &capturedBody)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "or-1",
			"object":  "chat.completion",
			"created": 1700000000,
			"model":   "google/gemini-2.5-flash",
			"choices": []any{
				map[string]any{
					"index": 0,
					"message": map[string]any{
						"role":    "assistant",
						"content": `{"ok":true}`,
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]any{
				"prompt_tokens":     20,
				"completion_tokens": 8,
				"total_tokens":      28,
			},
		})
	}))
	defer srv.Close()

	client, err := openrouter.New("or-secret", srv.URL, "https://whatshelper.local", "Whatshelper", nil)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	if _, err := client.Generate(ctx, core.LLMRequest{Prompt: "oi"}); !errors.Is(err, openrouter.ErrMissingModel) {
		t.Fatalf("expected ErrMissingModel, got %v", err)
	}
	if _, err := client.Generate(ctx, core.LLMRequest{Model: "openai/gpt-4o-mini"}); !errors.Is(err, openrouter.ErrEmptyPrompt) {
		t.Fatalf("expected ErrEmptyPrompt, got %v", err)
	}

	resp, err := client.Generate(ctx, core.LLMRequest{
		Stage:      "response",
		Model:      "google/gemini-2.5-flash",
		System:     "Sistema",
		Prompt:     "Pergunta",
		JSONSchema: `{"type":"object"}`,
		MaxTokens:  256,
	})
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if resp.Content != `{"ok":true}` || resp.PromptTokens != 20 || resp.CompletionTokens != 8 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if capturedAuth != "Bearer or-secret" || capturedReferer != "https://whatshelper.local" || capturedTitle != "Whatshelper" {
		t.Fatalf("unexpected headers: auth=%q referer=%q title=%q", capturedAuth, capturedReferer, capturedTitle)
	}
	if capturedPath != "/chat/completions" {
		t.Fatalf("expected /chat/completions, got %q", capturedPath)
	}
	rf, _ := capturedBody["response_format"].(map[string]any)
	if rf["type"] != "json_schema" {
		t.Fatalf("expected json_schema response_format, got %+v", rf)
	}
}
