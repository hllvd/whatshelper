package anthropic_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go/option"

	"whatshelper/internal/core"
	"whatshelper/internal/core/fakes"
	"whatshelper/internal/llm/anthropic"
)

func TestClient_NewValidation(t *testing.T) {
	if _, err := anthropic.New("", nil); !errors.Is(err, anthropic.ErrMissingAPIKey) {
		t.Fatalf("expected ErrMissingAPIKey, got %v", err)
	}
}

func TestClient_GenerateSuccess(t *testing.T) {
	var capturedBody map[string]any
	var capturedKey string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedKey = r.Header.Get("X-Api-Key")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &capturedBody)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"id": "msg_123",
			"type": "message",
			"role": "assistant",
			"model": "claude-test-model",
			"content": [{"type": "text", "text": "Olá! Como posso ajudar?"}],
			"stop_reason": "end_turn",
			"usage": {
				"input_tokens": 42,
				"output_tokens": 11,
				"cache_creation_input_tokens": 0,
				"cache_read_input_tokens": 5
			}
		}`))
	}))
	defer srv.Close()

	clock := fakes.NewFakeClock(time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC))
	client, err := anthropic.New("sk-test-123", clock, option.WithBaseURL(srv.URL), option.WithMaxRetries(0))
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	resp, err := client.Generate(context.Background(), core.LLMRequest{
		Stage:     "response",
		Model:     "claude-test-model",
		System:    "Você é um assistente.",
		Prompt:    "Oi",
		MaxTokens: 512,
	})
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if capturedKey != "sk-test-123" {
		t.Fatalf("expected X-Api-Key sk-test-123, got %q", capturedKey)
	}
	if capturedBody["model"] != "claude-test-model" {
		t.Fatalf("expected model claude-test-model, got %v", capturedBody["model"])
	}
	if resp.Content != "Olá! Como posso ajudar?" {
		t.Fatalf("unexpected content: %q", resp.Content)
	}
	if resp.PromptTokens != 47 || resp.CompletionTokens != 11 {
		t.Fatalf("unexpected usage: prompt=%d completion=%d", resp.PromptTokens, resp.CompletionTokens)
	}
}

func TestClient_GenerateErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"bad model"}}`))
	}))
	defer srv.Close()

	client, err := anthropic.New("sk-test", nil, option.WithBaseURL(srv.URL), option.WithMaxRetries(0))
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	if _, err := client.Generate(context.Background(), core.LLMRequest{Prompt: "oi"}); !errors.Is(err, anthropic.ErrMissingModel) {
		t.Fatalf("expected ErrMissingModel, got %v", err)
	}
	if _, err := client.Generate(context.Background(), core.LLMRequest{Model: "m", Prompt: "  "}); !errors.Is(err, anthropic.ErrEmptyPrompt) {
		t.Fatalf("expected ErrEmptyPrompt, got %v", err)
	}
	if _, err := client.Generate(context.Background(), core.LLMRequest{Model: "m", Prompt: "oi"}); err == nil {
		t.Fatalf("expected API error on HTTP 400")
	}
}
