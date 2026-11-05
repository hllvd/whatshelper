package config

import (
	"os"
	"strconv"
	"strings"
)

// Config centraliza todas as configurações do sistema carregadas de variáveis de ambiente.
type Config struct {
	HTTPHost         string
	HTTPPort         int
	Environment      string // "development", "staging", "production"
	LogLevel         string // "debug", "info", "warn", "error"
	LogFormat        string // "text", "json"
	DynamoDBEndpoint string
	DynamoDBTable    string
	S3Endpoint       string
	S3Bucket         string
	S3Region         string
	S3AccessKey      string
	S3SecretKey      string
	S3UseSSL         bool
	AnthropicAPIKey  string
	AnalysisModel    string
	ResponseModel    string
	NudgeModel       string
}

// LoadFromEnv carrega a configuração a partir do ambiente, preenchendo padrões seguros.
func LoadFromEnv() Config {
	return Config{
		HTTPHost:         getEnv("HTTP_HOST", "0.0.0.0"),
		HTTPPort:         getEnvInt("HTTP_PORT", 8080),
		Environment:      getEnv("APP_ENV", "development"),
		LogLevel:         getEnv("LOG_LEVEL", "info"),
		LogFormat:        getEnv("LOG_FORMAT", "text"),
		DynamoDBEndpoint: getEnv("DYNAMODB_ENDPOINT", "http://localhost:8000"),
		DynamoDBTable:    getEnv("DYNAMODB_TABLE", "whatshelper-brain"),
		S3Endpoint:       getEnv("S3_ENDPOINT", "http://localhost:9000"),
		S3Bucket:         getEnv("S3_BUCKET", "whatshelper-blobs"),
		S3Region:         getEnv("AWS_REGION", "us-east-1"),
		S3AccessKey:      getEnv("AWS_ACCESS_KEY_ID", "minioadmin"),
		S3SecretKey:      getEnv("AWS_SECRET_ACCESS_KEY", "minioadmin"),
		S3UseSSL:         getEnvBool("S3_USE_SSL", false),
		AnthropicAPIKey:  os.Getenv("ANTHROPIC_API_KEY"),
		AnalysisModel:    getEnv("MODEL_ANALYSIS", "claude-3-7-sonnet-20250219"),
		ResponseModel:    getEnv("MODEL_RESPONSE", "claude-3-7-sonnet-20250219"),
		NudgeModel:       getEnv("MODEL_NUDGE", "claude-3-5-haiku-20241022"),
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
