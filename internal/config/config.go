package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"whatshelper/internal/security"
)

// Config centraliza todas as configurações do sistema carregadas de variáveis de ambiente.
type Config struct {
	HTTPHost            string
	HTTPPort            int
	APIToken            string
	ConnectorToken      string
	AllowInsecureBind   bool
	Environment         string // "development", "staging", "production"
	LogLevel            string // "debug", "info", "warn", "error"
	LogFormat           string // "text", "json"
	PIIEncryptionKey    string
	DynamoDBEndpoint    string
	DynamoDBTable       string
	S3Endpoint          string
	S3Bucket            string
	S3Region            string
	S3AccessKey         string
	S3SecretKey         string
	S3UseSSL            bool
	LLMProvider         string // "fake" | "anthropic" | "gemini" | "deepseek"
	AnthropicAPIKey     string
	GeminiAPIKey        string
	DeepSeekAPIKey      string
	DeepSeekBaseURL     string
	AnalysisModel       string
	ResponseModel       string
	NudgeModel          string
	LearningModel       string
	TranscriptionURL    string
	TranscriptionAPIKey string
	TranscriptionModel  string
	DebounceMs          int
	RecentMessagesLimit int
	RunRetentionDays    int
	PacksDir            string
	TenantsDir          string
}

var (
	ErrInvalidLLMProvider   = errors.New("LLM_PROVIDER inválido: deve ser 'fake', 'anthropic', 'gemini' ou 'deepseek'")
	ErrAmbiguousLLMProvider = errors.New("múltiplas chaves de LLM definidas sem LLM_PROVIDER explícito")
	ErrMissingAnthropicKey  = errors.New("ANTHROPIC_API_KEY é obrigatório quando LLM_PROVIDER=anthropic")
	ErrMissingGeminiKey     = errors.New("GEMINI_API_KEY é obrigatório quando LLM_PROVIDER=gemini")
	ErrMissingDeepSeekKey   = errors.New("DEEPSEEK_API_KEY é obrigatório quando LLM_PROVIDER=deepseek")
	ErrMissingStageModel    = errors.New("nome do modelo obrigatório ausente para o provedor selecionado")
	ErrInsecureHTTPBind     = errors.New("HTTP_HOST não-loopback exige API_TOKEN configurado")
)

// LoadFromEnv carrega a configuração a partir do ambiente, preenchendo padrões seguros.
// Nomes de modelo de LLM nunca são fixados no código; devem vir de variáveis de ambiente.
func LoadFromEnv() Config {
	anthropicKey := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
	geminiKey := strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))
	deepseekKey := strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY"))
	provider := inferLLMProvider(getEnv("LLM_PROVIDER", ""), anthropicKey, geminiKey, deepseekKey)

	return Config{
		HTTPHost:            getEnv("HTTP_HOST", "127.0.0.1"),
		HTTPPort:            getEnvInt("HTTP_PORT", 8080),
		APIToken:            strings.TrimSpace(os.Getenv("API_TOKEN")),
		ConnectorToken:      strings.TrimSpace(os.Getenv("CONNECTOR_TOKEN")),
		AllowInsecureBind:   getEnvBool("ALLOW_INSECURE_BIND", false),
		Environment:         getEnv("APP_ENV", "development"),
		LogLevel:            getEnv("LOG_LEVEL", "info"),
		LogFormat:           getEnv("LOG_FORMAT", "text"),
		PIIEncryptionKey:    strings.TrimSpace(os.Getenv("PII_ENCRYPTION_KEY")),
		DynamoDBEndpoint:    getEnv("DYNAMODB_ENDPOINT", "http://localhost:8000"),
		DynamoDBTable:       getEnv("DYNAMODB_TABLE", "whatshelper-brain"),
		S3Endpoint:          getEnv("S3_ENDPOINT", "http://localhost:9000"),
		S3Bucket:            getEnv("S3_BUCKET", "whatshelper-blobs"),
		S3Region:            getEnv("AWS_REGION", "us-east-1"),
		S3AccessKey:         getEnv("AWS_ACCESS_KEY_ID", "minioadmin"),
		S3SecretKey:         getEnv("AWS_SECRET_ACCESS_KEY", "minioadmin"),
		S3UseSSL:            getEnvBool("S3_USE_SSL", false),
		LLMProvider:         strings.ToLower(provider),
		AnthropicAPIKey:     anthropicKey,
		GeminiAPIKey:        geminiKey,
		DeepSeekAPIKey:      deepseekKey,
		DeepSeekBaseURL:     getEnv("DEEPSEEK_BASE_URL", "https://api.deepseek.com"),
		AnalysisModel:       strings.TrimSpace(os.Getenv("MODEL_ANALYSIS")),
		ResponseModel:       strings.TrimSpace(os.Getenv("MODEL_RESPONSE")),
		NudgeModel:          strings.TrimSpace(os.Getenv("MODEL_NUDGE")),
		LearningModel:       strings.TrimSpace(os.Getenv("MODEL_LEARNING")),
		TranscriptionURL:    strings.TrimSpace(os.Getenv("TRANSCRIPTION_URL")),
		TranscriptionAPIKey: strings.TrimSpace(os.Getenv("TRANSCRIPTION_API_KEY")),
		TranscriptionModel:  strings.TrimSpace(os.Getenv("TRANSCRIPTION_MODEL")),
		DebounceMs:          getEnvInt("DEBOUNCE_MS", 1500),
		RecentMessagesLimit: getEnvInt("RECENT_MESSAGES_LIMIT", 20),
		RunRetentionDays:    getEnvInt("RUN_RETENTION_DAYS", 90),
		PacksDir:            getEnv("PACKS_DIR", "packs"),
		TenantsDir:          getEnv("TENANTS_DIR", "tenants"),
	}
}

func inferLLMProvider(explicit, anthropicKey, geminiKey, deepseekKey string) string {
	if p := strings.TrimSpace(explicit); p != "" {
		return strings.ToLower(p)
	}
	var active []string
	if anthropicKey != "" {
		active = append(active, "anthropic")
	}
	if geminiKey != "" {
		active = append(active, "gemini")
	}
	if deepseekKey != "" {
		active = append(active, "deepseek")
	}
	switch len(active) {
	case 0:
		return "fake"
	case 1:
		return active[0]
	default:
		return "ambiguous"
	}
}

// EffectiveConnectorToken retorna CONNECTOR_TOKEN ou faz fallback para API_TOKEN quando não definido separadamente.
func (c Config) EffectiveConnectorToken() string {
	if strings.TrimSpace(c.ConnectorToken) != "" {
		return strings.TrimSpace(c.ConnectorToken)
	}
	return strings.TrimSpace(c.APIToken)
}

// Secrets retorna os valores sensíveis configurados para aplicação na redação de logs.
func (c Config) Secrets() []string {
	candidates := []string{
		c.APIToken,
		c.ConnectorToken,
		c.AnthropicAPIKey,
		c.GeminiAPIKey,
		c.DeepSeekAPIKey,
		c.PIIEncryptionKey,
		c.S3SecretKey,
		c.TranscriptionAPIKey,
	}
	out := make([]string, 0, len(candidates))
	for _, s := range candidates {
		if trimmed := strings.TrimSpace(s); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// Validate valida as configurações obrigatórias na fronteira de inicialização.
func (c Config) Validate() error {
	if !isLoopbackHost(c.HTTPHost) && strings.TrimSpace(c.APIToken) == "" && !c.AllowInsecureBind {
		return fmt.Errorf("%w (host=%q)", ErrInsecureHTTPBind, c.HTTPHost)
	}

	env := strings.ToLower(strings.TrimSpace(c.Environment))
	if env == "staging" || env == "production" {
		if strings.TrimSpace(c.PIIEncryptionKey) == "" {
			return security.ErrMissingPIIKey
		}
	}
	if strings.TrimSpace(c.PIIEncryptionKey) != "" {
		if _, err := security.ParsePIIKey(c.PIIEncryptionKey); err != nil {
			return err
		}
	}

	switch c.LLMProvider {
	case "fake":
		return nil
	case "ambiguous":
		return ErrAmbiguousLLMProvider
	case "anthropic":
		if strings.TrimSpace(c.AnthropicAPIKey) == "" {
			return ErrMissingAnthropicKey
		}
		return c.validateStageModels()
	case "gemini":
		if strings.TrimSpace(c.GeminiAPIKey) == "" {
			return ErrMissingGeminiKey
		}
		return c.validateStageModels()
	case "deepseek":
		if strings.TrimSpace(c.DeepSeekAPIKey) == "" {
			return ErrMissingDeepSeekKey
		}
		return c.validateStageModels()
	default:
		return fmt.Errorf("%w: %q", ErrInvalidLLMProvider, c.LLMProvider)
	}
}

func (c Config) validateStageModels() error {
	if strings.TrimSpace(c.AnalysisModel) == "" {
		return fmt.Errorf("%w: MODEL_ANALYSIS", ErrMissingStageModel)
	}
	if strings.TrimSpace(c.ResponseModel) == "" {
		return fmt.Errorf("%w: MODEL_RESPONSE", ErrMissingStageModel)
	}
	if strings.TrimSpace(c.NudgeModel) == "" {
		return fmt.Errorf("%w: MODEL_NUDGE", ErrMissingStageModel)
	}
	return nil
}

func isLoopbackHost(host string) bool {
	h := strings.TrimSpace(strings.ToLower(host))
	if h == "" || h == "localhost" || h == "127.0.0.1" || h == "::1" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
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
