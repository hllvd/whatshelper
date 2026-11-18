package server_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"whatshelper/internal/channels/simulator"
	"whatshelper/internal/config"
	"whatshelper/internal/core"
	"whatshelper/internal/core/fakes"
	"whatshelper/internal/server"
)

func TestHandlePostSimMessage_AudioJSONAndMultipart(t *testing.T) {
	clock := fakes.NewFakeClock(time.Date(2026, 11, 18, 14, 0, 0, 0, time.UTC))
	blobs := fakes.NewMemoryBlobStore()
	simCh := simulator.New(clock, 16)
	_ = simCh.Start(context.Background())
	defer simCh.Stop(context.Background())

	srv := server.NewWithDeps(config.Config{HTTPPort: 0}, nil, server.Dependencies{
		Blobs:     blobs,
		Simulator: simCh,
	})

	// 1. Envio de áudio via JSON com Base64
	fakeAudio := []byte("fake-ogg-audio-stream")
	audioB64 := base64.StdEncoding.EncodeToString(fakeAudio)
	jsonBody, _ := json.Marshal(map[string]any{
		"tenant_id":    "supporter",
		"source_key":   "sim-audio-user",
		"type":         "audio",
		"audio_base64": audioB64,
		"mime_type":    "audio/webm",
		"file_name":    "audio.webm",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/sim/messages", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected HTTP 202 Accepted for audio JSON, got %d: %s", w.Code, w.Body.String())
	}

	select {
	case msg := <-simCh.Messages():
		if msg.Type != core.MessageTypeAudio {
			t.Fatalf("expected MessageTypeAudio, got %s", msg.Type)
		}
		if msg.TenantID != "supporter" || msg.SourceKey != "sim-audio-user" {
			t.Fatalf("unexpected message routing: tenant=%s, sourceKey=%s", msg.TenantID, msg.SourceKey)
		}
		storedAudio, err := blobs.Get(context.Background(), msg.MediaRef)
		if err != nil {
			t.Fatalf("failed to retrieve stored audio from BlobStore: %v", err)
		}
		if string(storedAudio) != string(fakeAudio) {
			t.Fatalf("stored audio mismatch: got %q, expected %q", string(storedAudio), string(fakeAudio))
		}
	default:
		t.Fatal("expected inbound message in simulator channel")
	}

	// 2. Envio de áudio via Multipart Form
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("tenant_id", "supporter")
	_ = mw.WriteField("source_key", "sim-audio-user")
	_ = mw.WriteField("text", "[Áudio de teste]")
	part, err := mw.CreateFormFile("file", "gravacao.ogg")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("fake-multipart-ogg-bytes"))
	_ = mw.Close()

	mpReq := httptest.NewRequest(http.MethodPost, "/api/sim/messages", &buf)
	mpReq.Header.Set("Content-Type", mw.FormDataContentType())
	mpW := httptest.NewRecorder()
	srv.Handler().ServeHTTP(mpW, mpReq)

	if mpW.Code != http.StatusAccepted {
		t.Fatalf("expected HTTP 202 Accepted for multipart audio, got %d: %s", mpW.Code, mpW.Body.String())
	}

	select {
	case msg := <-simCh.Messages():
		if msg.Type != core.MessageTypeAudio {
			t.Fatalf("expected MessageTypeAudio, got %s", msg.Type)
		}
		storedAudio, err := blobs.Get(context.Background(), msg.MediaRef)
		if err != nil {
			t.Fatalf("failed to retrieve stored multipart audio from BlobStore: %v", err)
		}
		if string(storedAudio) != "fake-multipart-ogg-bytes" {
			t.Fatalf("stored multipart audio mismatch: %q", string(storedAudio))
		}
	default:
		t.Fatal("expected inbound message in simulator channel")
	}
}
