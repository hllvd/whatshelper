package config_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"whatshelper/internal/config"
	"whatshelper/internal/security"
)

func clearLLMEnv() {
	for _, k := range []string{
		"LLM_PROVIDER", "ANTHROPIC_API_KEY", "GEMINI_API_KEY", "DEEPSEEK_API_KEY", "DEEPSEEK_BASE_URL",
		"OPENROUTER_API_KEY", "OPENROUTER_BASE_URL", "OPENROUTER_SITE_URL", "OPENROUTER_APP_NAME",
		"MODEL_ANALYSIS", "MODEL_RESPONSE", "MODEL_NUDGE", "MODEL_LEARNING", "ENV_FILE",
		"HTTP_HOST", "HTTP_PORT", "API_TOKEN", "CONNECTOR_TOKEN", "ALLOW_INSECURE_BIND",
		"APP_ENV", "LOG_LEVEL", "LOG_FORMAT", "PII_ENCRYPTION_KEY", "S3_USE_SSL",
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

func TestConfig_PIISecurityAndConnectorToken(t *testing.T) {
	cfg := config.Config{
		Environment: "production",
		LLMProvider: "fake",
	}
	if err := cfg.Validate(); !errors.Is(err, security.ErrMissingPIIKey) {
		t.Fatalf("expected ErrMissingPIIKey in production without PII key, got %v", err)
	}

	cfg.PIIEncryptionKey = "too-short"
	if err := cfg.Validate(); !errors.Is(err, security.ErrInvalidPIIKey) {
		t.Fatalf("expected ErrInvalidPIIKey for bad key, got %v", err)
	}

	cfg.PIIEncryptionKey = strings.Repeat("k", 32)
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected valid production config with 32-byte key, got %v", err)
	}

	cfg.APIToken = "api-tok"
	if got := cfg.EffectiveConnectorToken(); got != "api-tok" {
		t.Fatalf("expected fallback to APIToken, got %q", got)
	}
	cfg.ConnectorToken = "conn-tok"
	if got := cfg.EffectiveConnectorToken(); got != "conn-tok" {
		t.Fatalf("expected explicit ConnectorToken, got %q", got)
	}
}

func TestConfig_GeminiDeepSeekAndDotEnv(t *testing.T) {
	clearLLMEnv()
	defer clearLLMEnv()

	// Validação de Gemini e DeepSeek
	gemCfg := config.Config{LLMProvider: "gemini"}
	if err := gemCfg.Validate(); !errors.Is(err, config.ErrMissingGeminiKey) {
		t.Fatalf("expected ErrMissingGeminiKey, got %v", err)
	}
	gemCfg.GeminiAPIKey = "gem-secret"
	gemCfg.AnalysisModel = "m-a"
	gemCfg.ResponseModel = "m-r"
	gemCfg.NudgeModel = "m-n"
	if err := gemCfg.Validate(); err != nil {
		t.Fatalf("expected valid gemini config, got %v", err)
	}

	dsCfg := config.Config{LLMProvider: "deepseek"}
	if err := dsCfg.Validate(); !errors.Is(err, config.ErrMissingDeepSeekKey) {
		t.Fatalf("expected ErrMissingDeepSeekKey, got %v", err)
	}
	dsCfg.DeepSeekAPIKey = "ds-secret"
	dsCfg.AnalysisModel = "m-a"
	dsCfg.ResponseModel = "m-r"
	dsCfg.NudgeModel = "m-n"
	if err := dsCfg.Validate(); err != nil {
		t.Fatalf("expected valid deepseek config, got %v", err)
	}

	orCfg := config.Config{LLMProvider: "openrouter"}
	if err := orCfg.Validate(); !errors.Is(err, config.ErrMissingOpenRouterKey) {
		t.Fatalf("expected ErrMissingOpenRouterKey, got %v", err)
	}
	orCfg.OpenRouterAPIKey = "or-secret"
	orCfg.AnalysisModel = "m-a"
	orCfg.ResponseModel = "m-r"
	orCfg.NudgeModel = "m-n"
	if err := orCfg.Validate(); err != nil {
		t.Fatalf("expected valid openrouter config, got %v", err)
	}

	// Múltiplas chaves sem LLM_PROVIDER deve acusar ambiguidade explícita
	_ = os.Setenv("GEMINI_API_KEY", "g1")
	_ = os.Setenv("DEEPSEEK_API_KEY", "d1")
	amb := config.LoadFromEnv()
	if err := amb.Validate(); !errors.Is(err, config.ErrAmbiguousLLMProvider) {
		t.Fatalf("expected ErrAmbiguousLLMProvider, got %v", err)
	}

	// LoadDotEnv não sobrescreve variáveis já exportadas no ambiente
	clearLLMEnv()
	tmpEnv := t.TempDir() + "/.env"
	content := "# comentário\nexport LLM_PROVIDER=gemini\nGEMINI_API_KEY=\"from-dotenv # keep\"\nDEEPSEEK_BASE_URL=https://custom.example # inline\nMODEL_ANALYSIS='m-analysis'\nMODEL_RESPONSE=m-response\nMODEL_NUDGE=m-nudge\n"
	if err := os.WriteFile(tmpEnv, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Setenv("MODEL_ANALYSIS", "override-from-shell")
	if err := config.LoadDotEnv(tmpEnv); err != nil {
		t.Fatalf("LoadDotEnv failed: %v", err)
	}
	loaded := config.LoadFromEnv()
	if loaded.LLMProvider != "gemini" || loaded.GeminiAPIKey != "from-dotenv # keep" {
		t.Fatalf("unexpected dotenv load: provider=%q key=%q", loaded.LLMProvider, loaded.GeminiAPIKey)
	}
	if loaded.DeepSeekBaseURL != "https://custom.example" {
		t.Fatalf("expected stripped inline comment, got %q", loaded.DeepSeekBaseURL)
	}
	if loaded.AnalysisModel != "override-from-shell" {
		t.Fatalf("expected shell env to take precedence over .env, got %q", loaded.AnalysisModel)
	}
}
