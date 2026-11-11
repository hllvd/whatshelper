package config_test

import (
	"errors"
	"os"
	"testing"

	"whatshelper/internal/config"
)

func clearLLMEnv() {
	for _, k := range []string{
		"LLM_PROVIDER", "ANTHROPIC_API_KEY", "MODEL_ANALYSIS", "MODEL_RESPONSE", "MODEL_NUDGE",
		"HTTP_HOST", "HTTP_PORT", "API_TOKEN", "ALLOW_INSECURE_BIND",
		"APP_ENV", "LOG_LEVEL", "LOG_FORMAT", "S3_USE_SSL",
	} {
		_ = os.Unsetenv(k)
	}
}

func TestLoadFromEnv_Defaults(t *testing.T) {
	clearLLMEnv()
	defer clearLLMEnv()

	cfg := config.LoadFromEnv()
	if cfg.HTTPHost != "127.0.0.1" {
		t.Fatalf("expected default HTTPHost 127.0.0.1, got %s", cfg.HTTPHost)
	}
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
	if cfg.LLMProvider != "fake" {
		t.Fatalf("expected default LLMProvider fake when no API key, got %s", cfg.LLMProvider)
	}
	if cfg.AnalysisModel != "" || cfg.ResponseModel != "" || cfg.NudgeModel != "" {
		t.Fatalf("expected empty model names in code defaults, got %q/%q/%q", cfg.AnalysisModel, cfg.ResponseModel, cfg.NudgeModel)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected fake provider config to validate, got %v", err)
	}
}

func TestLoadFromEnv_Custom(t *testing.T) {
	clearLLMEnv()
	defer clearLLMEnv()

	_ = os.Setenv("HTTP_PORT", "9090")
	_ = os.Setenv("APP_ENV", "production")
	_ = os.Setenv("LOG_FORMAT", "json")
	_ = os.Setenv("S3_USE_SSL", "true")

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

func TestConfig_ValidateAnthropic(t *testing.T) {
	tests := []struct {
		name    string
		cfg     config.Config
		wantErr error
	}{
		{
			name:    "invalid provider",
			cfg:     config.Config{LLMProvider: "openai"},
			wantErr: config.ErrInvalidLLMProvider,
		},
		{
			name:    "anthropic missing key",
			cfg:     config.Config{LLMProvider: "anthropic"},
			wantErr: config.ErrMissingAnthropicKey,
		},
		{
			name: "anthropic missing analysis model",
			cfg: config.Config{
				LLMProvider:     "anthropic",
				AnthropicAPIKey: "sk-test",
			},
			wantErr: config.ErrMissingStageModel,
		},
		{
			name: "anthropic missing response model",
			cfg: config.Config{
				LLMProvider:     "anthropic",
				AnthropicAPIKey: "sk-test",
				AnalysisModel:   "model-a",
			},
			wantErr: config.ErrMissingStageModel,
		},
		{
			name: "anthropic missing nudge model",
			cfg: config.Config{
				LLMProvider:     "anthropic",
				AnthropicAPIKey: "sk-test",
				AnalysisModel:   "model-a",
				ResponseModel:   "model-b",
			},
			wantErr: config.ErrMissingStageModel,
		},
		{
			name: "anthropic valid",
			cfg: config.Config{
				LLMProvider:     "anthropic",
				AnthropicAPIKey: "sk-test",
				AnalysisModel:   "model-a",
				ResponseModel:   "model-b",
				NudgeModel:      "model-c",
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr == nil && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected error %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestConfig_HTTPBindSecurity(t *testing.T) {
	cases := []struct {
		name    string
		host    string
		token   string
		allow   bool
		wantErr error
	}{
		{name: "loopback without token ok", host: "127.0.0.1"},
		{name: "localhost without token ok", host: "localhost"},
		{name: "non-loopback without token rejected", host: "0.0.0.0", wantErr: config.ErrInsecureHTTPBind},
		{name: "non-loopback with token ok", host: "0.0.0.0", token: "secret-123"},
		{name: "non-loopback with explicit allow_insecure_bind ok", host: "0.0.0.0", allow: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Config{
				HTTPHost:          tc.host,
				APIToken:          tc.token,
				AllowInsecureBind: tc.allow,
				LLMProvider:       "fake",
			}
			err := cfg.Validate()
			if tc.wantErr == nil && err != nil {
				t.Fatalf("expected nil, got %v", err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected %v, got %v", tc.wantErr, err)
			}
		})
	}
}
