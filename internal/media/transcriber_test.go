package media_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

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
