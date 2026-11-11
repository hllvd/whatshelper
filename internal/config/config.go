package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config centraliza todas as configurações do sistema carregadas de variáveis de ambiente.
type Config struct {
	HTTPHost            string
	HTTPPort            int
	Environment         string // "development", "staging", "production"
	LogLevel            string // "debug", "info", "warn", "error"
	LogFormat           string // "text", "json"
	DynamoDBEndpoint    string
	DynamoDBTable       string
	S3Endpoint          string
	S3Bucket            string
	S3Region            string
	S3AccessKey         string
	S3SecretKey         string
	S3UseSSL            bool
	LLMProvider         string // "fake" | "anthropic"
	AnthropicAPIKey     string
	AnalysisModel       string
	ResponseModel       string
	NudgeModel          string
	DebounceMs          int
	RecentMessagesLimit int
	PacksDir            string
	TenantsDir          string
}

var (
	ErrInvalidLLMProvider  = errors.New("LLM_PROVIDER inválido: deve ser 'fake' ou 'anthropic'")
	ErrMissingAnthropicKey = errors.New("ANTHROPIC_API_KEY é obrigatório quando LLM_PROVIDER=anthropic")
	ErrMissingStageModel   = errors.New("nome do modelo obrigatório ausente quando LLM_PROVIDER=anthropic")
)

// LoadFromEnv carrega a configuração a partir do ambiente, preenchendo padrões seguros.
// Nomes de modelo de LLM nunca são fixados no código; devem vir de variáveis de ambiente.
func LoadFromEnv() Config {
	provider := getEnv("LLM_PROVIDER", "")
	apiKey := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
	if provider == "" {
		if apiKey != "" {
			provider = "anthropic"
		} else {
			provider = "fake"
		}
	}

	return Config{
		HTTPHost:            getEnv("HTTP_HOST", "0.0.0.0"),
		HTTPPort:            getEnvInt("HTTP_PORT", 8080),
		Environment:         getEnv("APP_ENV", "development"),
		LogLevel:            getEnv("LOG_LEVEL", "info"),
		LogFormat:           getEnv("LOG_FORMAT", "text"),
		DynamoDBEndpoint:    getEnv("DYNAMODB_ENDPOINT", "http://localhost:8000"),
		DynamoDBTable:       getEnv("DYNAMODB_TABLE", "whatshelper-brain"),
		S3Endpoint:          getEnv("S3_ENDPOINT", "http://localhost:9000"),
		S3Bucket:            getEnv("S3_BUCKET", "whatshelper-blobs"),
		S3Region:            getEnv("AWS_REGION", "us-east-1"),
		S3AccessKey:         getEnv("AWS_ACCESS_KEY_ID", "minioadmin"),
		S3SecretKey:         getEnv("AWS_SECRET_ACCESS_KEY", "minioadmin"),
		S3UseSSL:            getEnvBool("S3_USE_SSL", false),
		LLMProvider:         strings.ToLower(provider),
		AnthropicAPIKey:     apiKey,
		AnalysisModel:       strings.TrimSpace(os.Getenv("MODEL_ANALYSIS")),
		ResponseModel:       strings.TrimSpace(os.Getenv("MODEL_RESPONSE")),
		NudgeModel:          strings.TrimSpace(os.Getenv("MODEL_NUDGE")),
		DebounceMs:          getEnvInt("DEBOUNCE_MS", 1500),
		RecentMessagesLimit: getEnvInt("RECENT_MESSAGES_LIMIT", 20),
		PacksDir:            getEnv("PACKS_DIR", "packs"),
		TenantsDir:          getEnv("TENANTS_DIR", "tenants"),
	}
}

// Validate valida as configurações obrigatórias na fronteira de inicialização.
func (c Config) Validate() error {
	switch c.LLMProvider {
	case "fake":
		return nil
	case "anthropic":
		if c.AnthropicAPIKey == "" {
			return ErrMissingAnthropicKey
		}
		if c.AnalysisModel == "" {
			return fmt.Errorf("%w: MODEL_ANALYSIS", ErrMissingStageModel)
		}
		if c.ResponseModel == "" {
			return fmt.Errorf("%w: MODEL_RESPONSE", ErrMissingStageModel)
		}
		if c.NudgeModel == "" {
			return fmt.Errorf("%w: MODEL_NUDGE", ErrMissingStageModel)
		}
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrInvalidLLMProvider, c.LLMProvider)
	}
}

func getEnv(key, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok && strings.TrimSpace(val) != "" {
		return strings.TrimSpace(val)
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) int {
	if val, ok := os.LookupEnv(key); ok {
		if parsed, err := strconv.Atoi(strings.TrimSpace(val)); err == nil {
			return parsed
		}
	}
	return defaultVal
}

func getEnvBool(key string, defaultVal bool) bool {
	if val, ok := os.LookupEnv(key); ok {
		switch strings.ToLower(strings.TrimSpace(val)) {
		case "true", "1", "yes":
			return true
		case "false", "0", "no":
			return false
		}
	}
	return defaultVal
}
