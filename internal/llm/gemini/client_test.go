package gemini_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"whatshelper/internal/core"
	"whatshelper/internal/llm/gemini"
)

func TestGeminiClient_GenerateAndErrors(t *testing.T) {
	ctx := context.Background()
	if _, err := gemini.New(ctx, "", nil); !errors.Is(err, gemini.ErrMissingAPIKey) {
		t.Fatalf("expected ErrMissingAPIKey, got %v", err)
	}

	var capturedPath string
	var capturedAPIKey string
	var capturedBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedAPIKey = r.Header.Get("X-Goog-Api-Key")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &capturedBody)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"candidates": []any{
				map[string]any{
					"content": map[string]any{
						"role":  "model",
						"parts": []any{map[string]any{"text": `{"ok":true}`}},
					},
				},
			},
			"usageMetadata": map[string]any{
				"promptTokenCount":     12,
				"candidatesTokenCount": 7,
				"totalTokenCount":      19,
			},
		})
	}))
	defer srv.Close()

	client, err := gemini.New(ctx, "gem-test-key", nil, gemini.WithBaseURL(srv.URL), gemini.WithHTTPClient(srv.Client()))
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	if _, err := client.Generate(ctx, core.LLMRequest{Prompt: "oi"}); !errors.Is(err, gemini.ErrMissingModel) {
		t.Fatalf("expected ErrMissingModel, got %v", err)
	}
	if _, err := client.Generate(ctx, core.LLMRequest{Model: "gemini-model"}); !errors.Is(err, gemini.ErrEmptyPrompt) {
		t.Fatalf("expected ErrEmptyPrompt, got %v", err)
	}

	resp, err := client.Generate(ctx, core.LLMRequest{
		Stage:       "analysis",
		Model:       "gemini-2.5-flash",
		System:      "Responda apenas JSON.",
		Prompt:      "Cliente disse oi",
		JSONSchema:  `{"type":"object"}`,
		Temperature: 0.2,
		MaxTokens:   512,
	})
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if resp.Content != `{"ok":true}` || resp.PromptTokens != 12 || resp.CompletionTokens != 7 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if capturedAPIKey != "gem-test-key" {
		t.Fatalf("expected X-Goog-Api-Key header, got %q", capturedAPIKey)
	}
	if !strings.Contains(capturedPath, "gemini-2.5-flash:generateContent") {
		t.Fatalf("unexpected endpoint path: %s", capturedPath)
	}
	genCfg, _ := capturedBody["generationConfig"].(map[string]any)
	if genCfg["responseMimeType"] != "application/json" {
		t.Fatalf("expected responseMimeType application/json, got %+v", genCfg)
	}
}
