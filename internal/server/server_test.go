package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"whatshelper/internal/config"
	"whatshelper/internal/server"
)

func TestServer_HealthEndpoints(t *testing.T) {
	cfg := config.Config{
		HTTPHost:    "127.0.0.1",
		HTTPPort:    0,
		Environment: "test",
		LogLevel:    "error",
		LogFormat:   "text",
	}
	srv := server.New(cfg, nil)

	// Test /healthz
	reqHealth := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	recHealth := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recHealth, reqHealth)

	if recHealth.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recHealth.Code)
	}

	var healthResp server.HealthResponse
	body, _ := io.ReadAll(recHealth.Body)
	if err := json.Unmarshal(body, &healthResp); err != nil {
		t.Fatalf("failed to parse json: %v", err)
	}
	if healthResp.Status != "ok" || healthResp.Service != "whatshelper-brain" {
		t.Fatalf("unexpected health response: %+v", healthResp)
	}

	// Test /readyz
	reqReady := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	recReady := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recReady, reqReady)

	if recReady.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recReady.Code)
	}
	var readyResp server.HealthResponse
	bodyReady, _ := io.ReadAll(recReady.Body)
	if err := json.Unmarshal(bodyReady, &readyResp); err != nil {
		t.Fatalf("failed to parse json: %v", err)
	}
	if readyResp.Status != "ready" {
		t.Fatalf("unexpected ready response: %+v", readyResp)
	}
}

func TestServer_GracefulShutdown(t *testing.T) {
	cfg := config.Config{
		HTTPHost:    "127.0.0.1",
		HTTPPort:    0,
		Environment: "test",
		LogLevel:    "error",
		LogFormat:   "text",
	}
	srv := server.New(cfg, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown failed: %v", err)
	}
}
