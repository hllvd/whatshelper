package config_test

import (
	"os"
	"testing"

	"whatshelper/internal/config"
)

func TestLoadFromEnv_Defaults(t *testing.T) {
	// Limpa variáveis relevantes
	_ = os.Unsetenv("HTTP_PORT")
	_ = os.Unsetenv("APP_ENV")
	_ = os.Unsetenv("LOG_LEVEL")

	cfg := config.LoadFromEnv()
	if cfg.HTTPPort != 8080 {
		t.Fatalf("expected default port 8080, got %d", cfg.HTTPPort)
	}
	if cfg.Environment != "development" {
		t.Fatalf("expected default env development, got %s", cfg.Environment)
	}
	if cfg.LogLevel != "info" {
		t.Fatalf("expected default log level info, got %s", cfg.LogLevel)
	}
	if cfg.DynamoDBTable != "whatshelper-brain" {
		t.Fatalf("expected default table whatshelper-brain, got %s", cfg.DynamoDBTable)
	}
}

func TestLoadFromEnv_Custom(t *testing.T) {
	_ = os.Setenv("HTTP_PORT", "9090")
	_ = os.Setenv("APP_ENV", "production")
	_ = os.Setenv("LOG_FORMAT", "json")
	_ = os.Setenv("S3_USE_SSL", "true")
	defer func() {
		_ = os.Unsetenv("HTTP_PORT")
		_ = os.Unsetenv("APP_ENV")
		_ = os.Unsetenv("LOG_FORMAT")
		_ = os.Unsetenv("S3_USE_SSL")
	}()

	cfg := config.LoadFromEnv()
	if cfg.HTTPPort != 9090 {
		t.Fatalf("expected port 9090, got %d", cfg.HTTPPort)
	}
	if cfg.Environment != "production" {
		t.Fatalf("expected production, got %s", cfg.Environment)
	}
	if cfg.LogFormat != "json" {
		t.Fatalf("expected json, got %s", cfg.LogFormat)
	}
	if !cfg.S3UseSSL {
		t.Fatalf("expected S3UseSSL to be true")
	}
}
