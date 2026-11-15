package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"whatshelper/internal/core"
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
	LLMProvider         string // "fake" | "openai" | "anthropic" | "gemini" | "deepseek" | "openrouter"
	OpenAIAPIKey        string
	OpenAIBaseURL       string
	AnthropicAPIKey     string
	GeminiAPIKey        string
	DeepSeekAPIKey      string
	DeepSeekBaseURL     string
	OpenRouterAPIKey    string
	OpenRouterBaseURL   string
	OpenRouterSiteURL   string
	OpenRouterAppName   string
	AnalysisModel       string
	ResponseModel       string
	NudgeModel          string
	LearningModel       string
	VisionModel         string
	AudioModel          string
	AnalysisEffort      string
	ResponseEffort      string
	NudgeEffort         string
	LearningEffort      string
	VisionEffort        string
	AudioEffort         string
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
	ErrInvalidLLMProvider   = errors.New("LLM_PROVIDER inválido: deve ser 'fake', 'openai', 'anthropic', 'gemini', 'deepseek' ou 'openrouter'")
	ErrAmbiguousLLMProvider = errors.New("múltiplas chaves de LLM definidas sem LLM_PROVIDER explícito")
	ErrMissingOpenAIKey     = errors.New("OPENAI_API_KEY é obrigatório quando LLM_PROVIDER=openai")
	ErrMissingAnthropicKey  = errors.New("ANTHROPIC_API_KEY é obrigatório quando LLM_PROVIDER=anthropic")
	ErrMissingGeminiKey     = errors.New("GEMINI_API_KEY é obrigatório quando LLM_PROVIDER=gemini")
	ErrMissingDeepSeekKey   = errors.New("DEEPSEEK_API_KEY é obrigatório quando LLM_PROVIDER=deepseek")
	ErrMissingOpenRouterKey = errors.New("OPENROUTER_API_KEY é obrigatório quando LLM_PROVIDER=openrouter")
	ErrMissingStageModel    = errors.New("nome do modelo obrigatório ausente para o provedor selecionado")
	ErrInsecureHTTPBind     = errors.New("HTTP_HOST não-loopback exige API_TOKEN configurado")
)

type providerKeyEntry struct {
	name    string
	key     string
	missing error
}

// LoadFromEnv carrega a configuração a partir do ambiente, preenchendo padrões seguros.
// Nomes de modelo de LLM nunca são fixados no código; devem vir de variáveis de ambiente.
func LoadFromEnv() Config {
	openAIKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	anthropicKey := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
	geminiKey := strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))
	deepseekKey := strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY"))
	openRouterKey := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY"))
	provider := inferLLMProvider(getEnv("LLM_PROVIDER", ""), []providerKeyEntry{
		{name: "openai", key: openAIKey},
		{name: "anthropic", key: anthropicKey},
		{name: "gemini", key: geminiKey},
		{name: "deepseek", key: deepseekKey},
		{name: "openrouter", key: openRouterKey},
	})

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
		OpenAIAPIKey:        openAIKey,
		OpenAIBaseURL:       getEnv("OPENAI_BASE_URL", "https://api.openai.com/v1"),
		AnthropicAPIKey:     anthropicKey,
		GeminiAPIKey:        geminiKey,
		DeepSeekAPIKey:      deepseekKey,
		DeepSeekBaseURL:     getEnv("DEEPSEEK_BASE_URL", "https://api.deepseek.com"),
		OpenRouterAPIKey:    openRouterKey,
		OpenRouterBaseURL:   getEnv("OPENROUTER_BASE_URL", "https://openrouter.ai/api/v1"),
		OpenRouterSiteURL:   strings.TrimSpace(os.Getenv("OPENROUTER_SITE_URL")),
		OpenRouterAppName:   getEnv("OPENROUTER_APP_NAME", "Whatshelper"),
		AnalysisModel:       strings.TrimSpace(os.Getenv("MODEL_ANALYSIS")),
		ResponseModel:       strings.TrimSpace(os.Getenv("MODEL_RESPONSE")),
		NudgeModel:          strings.TrimSpace(os.Getenv("MODEL_NUDGE")),
		LearningModel:       strings.TrimSpace(os.Getenv("MODEL_LEARNING")),
		VisionModel:         strings.TrimSpace(os.Getenv("MODEL_VISION")),
		AudioModel:          strings.TrimSpace(os.Getenv("MODEL_AUDIO")),
		AnalysisEffort:      strings.TrimSpace(os.Getenv("EFFORT_ANALYSIS")),
		ResponseEffort:      strings.TrimSpace(os.Getenv("EFFORT_RESPONSE")),
		NudgeEffort:         strings.TrimSpace(os.Getenv("EFFORT_NUDGE")),
		LearningEffort:      strings.TrimSpace(os.Getenv("EFFORT_LEARNING")),
		VisionEffort:        strings.TrimSpace(os.Getenv("EFFORT_VISION")),
		AudioEffort:         strings.TrimSpace(os.Getenv("EFFORT_AUDIO")),
		TranscriptionURL:    strings.TrimSpace(os.Getenv("TRANSCRIPTION_URL")),
		TranscriptionAPIKey: strings.TrimSpace(os.Getenv("TRANSCRIPTION_API_KEY")),
		TranscriptionModel:  strings.TrimSpace(os.Getenv("TRANSCRIPTION_MODEL")),
		DebounceMs:          getEnvInt("DEBOUNCE_MS", 8000),
		RecentMessagesLimit: getEnvInt("RECENT_MESSAGES_LIMIT", 10),
		RunRetentionDays:    getEnvInt("RUN_RETENTION_DAYS", 90),
		PacksDir:            getEnv("PACKS_DIR", "packs"),
		TenantsDir:          getEnv("TENANTS_DIR", "tenants"),
	}
}

func inferLLMProvider(explicit string, entries []providerKeyEntry) string {
	if p := strings.TrimSpace(explicit); p != "" {
		return strings.ToLower(p)
	}
	var active []string
	for _, entry := range entries {
		if entry.key != "" {
			active = append(active, entry.name)
		}
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
		c.OpenAIAPIKey,
		c.AnthropicAPIKey,
		c.GeminiAPIKey,
		c.DeepSeekAPIKey,
		c.OpenRouterAPIKey,
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

	if c.LLMProvider == "fake" {
		return nil
	}
	if c.LLMProvider == "ambiguous" {
		return ErrAmbiguousLLMProvider
	}

	requiredKeys := map[string]providerKeyEntry{
		"openai":     {key: c.OpenAIAPIKey, missing: ErrMissingOpenAIKey},
		"anthropic":  {key: c.AnthropicAPIKey, missing: ErrMissingAnthropicKey},
		"gemini":     {key: c.GeminiAPIKey, missing: ErrMissingGeminiKey},
		"deepseek":   {key: c.DeepSeekAPIKey, missing: ErrMissingDeepSeekKey},
		"openrouter": {key: c.OpenRouterAPIKey, missing: ErrMissingOpenRouterKey},
	}
	entry, ok := requiredKeys[c.LLMProvider]
	if !ok {
		return fmt.Errorf("%w: %q", ErrInvalidLLMProvider, c.LLMProvider)
	}
	if strings.TrimSpace(entry.key) == "" {
		return entry.missing
	}
	if err := c.validateStageEfforts(); err != nil {
		return err
	}
	return c.validateStageModels()
}

func (c Config) validateStageEfforts() error {
	for envName, raw := range map[string]string{
		"EFFORT_ANALYSIS": c.AnalysisEffort,
		"EFFORT_RESPONSE": c.ResponseEffort,
		"EFFORT_NUDGE":    c.NudgeEffort,
		"EFFORT_LEARNING": c.LearningEffort,
		"EFFORT_VISION":   c.VisionEffort,
		"EFFORT_AUDIO":    c.AudioEffort,
	} {
		if _, ok := core.NormalizeReasoningEffort(raw); !ok {
			return fmt.Errorf("%s inválido %q: use 'none' (ou 'off'), 'minimal', 'low', 'medium' ou 'high'", envName, raw)
		}
	}
	return nil
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
