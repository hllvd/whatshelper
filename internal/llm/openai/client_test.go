package openai_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"whatshelper/internal/core"
	"whatshelper/internal/llm/openai"
)

func TestOpenAIClient_DirectAndMultimodalJSONSchema(t *testing.T) {
	if _, err := openai.New("", "", nil); !errors.Is(err, openai.ErrMissingAPIKey) {
		t.Fatalf("expected ErrMissingAPIKey, got %v", err)
	}

	var capturedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("unexpected Authorization: %q", r.Header.Get("Authorization"))
		}
		_ = json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl-1",
			"object":"chat.completion",
			"choices":[{"index":0,"message":{"role":"assistant","content":"{\"ok\":true}"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":12,"completion_tokens":4}
		}`))
	}))
	defer srv.Close()

	client, err := openai.New("sk-test", srv.URL, nil)
	if err != nil {
		t.Fatalf("openai.New: %v", err)
	}

	resp, err := client.Generate(context.Background(), core.LLMRequest{
		Stage:      "vision",
		Model:      "gpt-4o-mini",
		System:     "Describe image",
		Prompt:     "What is in this image?",
		JSONSchema: `{"type":"object","properties":{"ok":{"type":"boolean"}}}`,
		MediaParts: []core.MediaPart{{MimeType: "image/png", Data: []byte("fake-png")}},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp.Provider != "openai" || resp.Model != "gpt-4o-mini" || resp.Content != `{"ok":true}` {
		t.Fatalf("unexpected response: %+v", resp)
	}
	rf, _ := capturedBody["response_format"].(map[string]any)
	if rf == nil || rf["type"] != "json_schema" {
		t.Fatalf("expected response_format.type=json_schema, got %+v", capturedBody["response_format"])
	}
}
