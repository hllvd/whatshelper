package media_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"whatshelper/internal/core"
	"whatshelper/internal/media"
)

func TestDynamicAudioTranscriber_OverrideModelAndProvider(t *testing.T) {
	var capturedModel string
	var capturedAuth string
	var capturedPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedAuth = r.Header.Get("Authorization")
		if err := r.ParseMultipartForm(1 << 20); err == nil {
			capturedModel = r.FormValue("model")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"olá mundo"}`))
	}))
	defer srv.Close()

	tr := media.NewDynamicAudioTranscriber("", "", "", "sk-openai", srv.URL, "openai", "whisper-1", srv.Client())
	if tr == nil {
		t.Fatal("expected non-nil DynamicAudioTranscriber")
	}

	txt, err := tr.TranscribeWithConfig(context.Background(), []byte("fake-ogg"), "audio/ogg", "openai", "gpt-4o-transcribe")
	if err != nil {
		t.Fatalf("TranscribeWithConfig failed: %v", err)
	}
	if txt != "olá mundo" {
		t.Fatalf("unexpected transcript: %q", txt)
	}
	if capturedPath != "/audio/transcriptions" || capturedAuth != "Bearer sk-openai" || capturedModel != "gpt-4o-transcribe" {
		t.Fatalf("unexpected request: path=%q auth=%q model=%q", capturedPath, capturedAuth, capturedModel)
	}
}

type stubLLM struct {
	calledReq core.LLMRequest
	resp      string
}

func (s *stubLLM) Generate(ctx context.Context, req core.LLMRequest) (*core.LLMResponse, error) {
	s.calledReq = req
	return &core.LLMResponse{
		Content: s.resp,
	}, nil
}

func TestDynamicAudioTranscriber_WithLLM(t *testing.T) {
	stub := &stubLLM{resp: "audio transcrito pelo gemini"}
	tr := media.NewDynamicAudioTranscriberWithLLM("", "", "", "", "", "gemini", "gemini-2.5-flash", stub, nil)
	if tr == nil {
		t.Fatal("expected non-nil DynamicAudioTranscriber")
	}

	txt, err := tr.Transcribe(context.Background(), []byte("audio-bytes"), "audio/webm")
	if err != nil {
		t.Fatalf("Transcribe failed: %v", err)
	}
	if txt != "audio transcrito pelo gemini" {
		t.Fatalf("unexpected transcript: %q", txt)
	}
	if stub.calledReq.Stage != "audio" || stub.calledReq.Model != "gemini-2.5-flash" || len(stub.calledReq.MediaParts) != 1 {
		t.Fatalf("unexpected LLM request: %+v", stub.calledReq)
	}
}
