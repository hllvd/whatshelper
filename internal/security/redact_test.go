package security_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"whatshelper/internal/security"
)

func TestRedactingHandler_MasksSecretsAndPIIButKeepsSourceKey(t *testing.T) {
	var buf bytes.Buffer
	base := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(security.NewRedactingHandler(base, "super-secret-api-token", "sk-ant-1234567890"))

	logger.Info("mensagem recebida de +5511999998888 com Bearer tok-xyz",
		slog.String("tenant", "demo"),
		slog.String("channel", "whatsapp"),
		slog.String("source_key", "5511999998888"),
		slog.String("message_id", "wamid-1"),
		slog.String("raw_contact", "+55 11 99999-8888"),
		slog.String("email_hint", "cliente@exemplo.com.br"),
		slog.String("jid", "5511999998888@s.whatsapp.net"),
		slog.String("auth_header", "Bearer tok-abc-999"),
		slog.String("token", "super-secret-api-token"),
		slog.Group("extra",
			slog.String("api_key", "sk-ant-1234567890"),
			slog.String("note", "usou chave sk-ant-1234567890"),
		),
	)

	out := buf.String()
	for _, leaked := range []string{
		"+5511999998888",
		"99999-8888",
		"cliente@exemplo.com.br",
		"5511999998888@s.whatsapp.net",
		"tok-xyz",
		"tok-abc-999",
		"super-secret-api-token",
		"sk-ant-1234567890",
	} {
		if strings.Contains(out, leaked) {
			t.Fatalf("expected %q to be redacted in log output: %s", leaked, out)
		}
	}

	for _, preserved := range []string{
		"tenant=demo",
		"channel=whatsapp",
		"source_key=5511999998888",
		"message_id=wamid-1",
		"[REDACTED]",
		"[PHONE_REDACTED]",
		"[EMAIL_REDACTED]",
		"[JID_REDACTED]",
	} {
		if !strings.Contains(out, preserved) {
			t.Fatalf("expected %q to be present in log output: %s", preserved, out)
		}
	}
}
