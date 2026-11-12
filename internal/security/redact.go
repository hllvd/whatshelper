package security

import (
	"context"
	"log/slog"
	"regexp"
	"sort"
	"strings"
)

var (
	bearerPattern   = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]+`)
	jidPattern      = regexp.MustCompile(`\b\d{8,15}(?:@s\.whatsapp\.net|@lid|@c\.us)\b`)
	emailPattern    = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	e164PlusPattern = regexp.MustCompile(`\+\d{8,15}\b`)
)

// sensitiveKeys contém nomes de atributos cujo valor nunca deve vazar em claro nos logs.
var sensitiveKeys = map[string]struct{}{
	"token":                  {},
	"api_token":              {},
	"connector_token":        {},
	"anthropic_api_key":      {},
	"pii_encryption_key":     {},
	"password":               {},
	"secret":                 {},
	"api_key":                {},
	"authorization":          {},
	"raw_contact":            {},
	"contact_info":           {},
	"encrypted_contact_info": {},
	"phone":                  {},
	"email":                  {},
	"payload":                {},
	"body":                   {},
	"text":                   {},
	"raw_output":             {},
	"user_prompt":            {},
	"system_prompt":          {},
}

// safeIdentifierKeys preserva identificadores derivados (ex.: source_key) desde que não contenham segredos explícitos.
var safeIdentifierKeys = map[string]struct{}{
	"tenant":     {},
	"tenant_id":  {},
	"channel":    {},
	"source_key": {},
	"message_id": {},
	"run_id":     {},
	"task_id":    {},
	"proposal_id": {},
}

// RedactingHandler envolve um slog.Handler removendo segredos, tokens e PII bruta antes da escrita.
type RedactingHandler struct {
	inner   slog.Handler
	secrets []string
}

// NewRedactingHandler cria um slog.Handler com redação determinística para segredos e PII.
func NewRedactingHandler(inner slog.Handler, secrets ...string) *RedactingHandler {
	cleaned := normalizeSecrets(secrets)
	return &RedactingHandler{
		inner:   inner,
		secrets: cleaned,
	}
}

func (h *RedactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *RedactingHandler) Handle(ctx context.Context, r slog.Record) error {
	clone := slog.NewRecord(r.Time, r.Level, RedactString(r.Message, h.secrets), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		clone.AddAttrs(redactAttr(a, h.secrets))
		return true
	})
	return h.inner.Handle(ctx, clone)
}

func (h *RedactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	redacted := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		redacted[i] = redactAttr(a, h.secrets)
	}
	return &RedactingHandler{
		inner:   h.inner.WithAttrs(redacted),
		secrets: h.secrets,
	}
}

func (h *RedactingHandler) WithGroup(name string) slog.Handler {
	return &RedactingHandler{
		inner:   h.inner.WithGroup(name),
		secrets: h.secrets,
	}
}

func redactAttr(a slog.Attr, secrets []string) slog.Attr {
	key := strings.ToLower(strings.TrimSpace(a.Key))
	if a.Value.Kind() == slog.KindGroup {
		group := a.Value.Group()
		out := make([]slog.Attr, len(group))
		for i, item := range group {
			out[i] = redactAttr(item, secrets)
		}
		return slogGroupAttrCompat(a.Key, out...)
	}

	if _, isSensitive := sensitiveKeys[key]; isSensitive {
		return slog.String(a.Key, "[REDACTED]")
	}

	if a.Value.Kind() == slog.KindString {
		val := a.Value.String()
		val = redactSecretsOnly(val, secrets)
		if _, isSafeID := safeIdentifierKeys[key]; !isSafeID {
			val = RedactPII(val)
		}
		return slog.String(a.Key, val)
	}

	if a.Value.Kind() == slog.KindAny {
		if err, ok := a.Value.Any().(error); ok && err != nil {
			return slog.String(a.Key, RedactString(err.Error(), secrets))
		}
	}

	return a
}

// GroupValueAttr constrói um slog.Attr do tipo grupo sem expor detalhes internos.
func slogGroupAttrCompat(key string, attrs ...slog.Attr) slog.Attr {
	anyAttrs := make([]any, len(attrs))
	for i, attr := range attrs {
		anyAttrs[i] = attr
	}
	return slog.Group(key, anyAttrs...)
}

// RedactString aplica redação de segredos explícitos e padrões PII comuns em texto livre.
func RedactString(s string, secrets []string) string {
	return RedactPII(redactSecretsOnly(s, secrets))
}

// RedactPII mascara credenciais Bearer, JIDs, e-mails e telefones no padrão +E.164.
func RedactPII(s string) string {
	if s == "" {
		return ""
	}
	out := bearerPattern.ReplaceAllString(s, "Bearer [REDACTED]")
	out = jidPattern.ReplaceAllString(out, "[JID_REDACTED]")
	out = emailPattern.ReplaceAllString(out, "[EMAIL_REDACTED]")
	out = e164PlusPattern.ReplaceAllString(out, "[PHONE_REDACTED]")
	return out
}

func redactSecretsOnly(s string, secrets []string) string {
	if s == "" || len(secrets) == 0 {
		return s
	}
	out := s
	for _, secret := range secrets {
		out = strings.ReplaceAll(out, secret, "[REDACTED]")
	}
	return out
}

func normalizeSecrets(secrets []string) []string {
	seen := make(map[string]struct{}, len(secrets))
	out := make([]string, 0, len(secrets))
	for _, raw := range secrets {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		return len(out[i]) > len(out[j])
	})
	return out
}
