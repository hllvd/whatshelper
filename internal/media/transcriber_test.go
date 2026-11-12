package media_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"whatshelper/internal/media"
)

func TestHTTPTranscriber(t *testing.T) {
	if media.NewHTTPTranscriber("", "", "", nil) != nil {
		t.Fatalf("expected nil transcriber when endpoint is empty")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-tr" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if err := r.ParseMultipartForm(4 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"text": "Quero confirmar o horário."})
	}))
	defer srv.Close()

	tr := media.NewHTTPTranscriber(srv.URL, "sk-tr", "whisper-1", nil)
	got, err := tr.Transcribe(context.Background(), []byte("fake-ogg"), "audio/ogg")
	if err != nil {
		t.Fatalf("Transcribe failed: %v", err)
	}
	if got != "Quero confirmar o horário." {
		t.Fatalf("unexpected transcription: %q", got)
	}
}
