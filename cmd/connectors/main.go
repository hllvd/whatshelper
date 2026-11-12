package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"whatshelper/internal/channels/bridge"
	"whatshelper/internal/channels/humanizer"
	"whatshelper/internal/channels/whatsapp"
	"whatshelper/internal/core"
	"whatshelper/internal/server"
)

func main() {
	token := firstNonEmpty(os.Getenv("CONNECTOR_TOKEN"), os.Getenv("API_TOKEN"))
	logLevel := getEnv("LOG_LEVEL", "info")
	logFormat := getEnv("LOG_FORMAT", "text")
	logger := server.InitLoggerWithSecrets(logLevel, logFormat, token)
	slog.SetDefault(logger)

	brainURL := getEnv("BRAIN_URL", "http://127.0.0.1:8080")
	dbPath := getEnv("WHATSAPP_DB_PATH", "/data/whatsapp.db")
	tenantID := getEnv("WHATSAPP_TENANT_ID", "demo")
	account := getEnv("WHATSAPP_ACCOUNT", "main")
	adminPhone := strings.TrimSpace(os.Getenv("ADMIN_PHONE_E164"))
	pairPhone := strings.TrimSpace(os.Getenv("WHATSAPP_PAIR_PHONE"))
	allowGroups := strings.EqualFold(strings.TrimSpace(os.Getenv("WHATSAPP_ALLOW_GROUPS")), "true")
	httpAddr := getEnv("CONNECTOR_HTTP_ADDR", "127.0.0.1:8081")
	enabled := !strings.EqualFold(strings.TrimSpace(os.Getenv("WHATSAPP_ENABLED")), "false")

	logger.Info("inicializando Whatshelper Connectors Runner",
		slog.String("brain_url", brainURL),
		slog.String("tenant", tenantID),
		slog.String("channel", "whatsapp"),
		slog.Bool("enabled", enabled),
	)

	bridgeClient, err := bridge.NewClient(bridge.ClientConfig{
		BrainBaseURL: brainURL,
		Token:        token,
		Channel:      "whatsapp",
		Account:      account,
		Logger:       logger,
	})
	if err != nil {
		logger.Error("falha ao configurar cliente bridge", slog.Any("error", err))
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var waAdapter *whatsapp.Adapter
	var outbound bridge.OutboundHandler = noopOutboundHandler{}

	if enabled {
		waAdapter, err = whatsapp.New(whatsapp.Config{
			DBPath:         dbPath,
			TenantID:       tenantID,
			Account:        account,
			AdminPhoneE164: adminPhone,
			AllowGroups:    allowGroups,
			PairPhone:      pairPhone,
			BridgeClient:   bridgeClient,
			Logger:         logger,
		})
		if err != nil {
			logger.Error("falha ao inicializar adaptador WhatsApp", slog.Any("error", err))
			os.Exit(1)
		}
		if err := waAdapter.Start(ctx); err != nil {
			logger.Error("falha ao conectar WhatsApp", slog.Any("error", err))
			os.Exit(1)
		}
		outbound = humanizer.New(waAdapter, humanizer.Config{
			MinTypingDelay:       600 * time.Millisecond,
			MaxTypingDelay:       3500 * time.Millisecond,
			MsPerChar:            35 * time.Millisecond,
			MinIntervalPerSource: 400 * time.Millisecond,
			MinGlobalInterval:    150 * time.Millisecond,
		})
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		status := map[string]any{
			"status":  "ok",
			"service": "whatshelper-connectors",
			"enabled": enabled,
		}
		if waAdapter != nil {
			status["whatsapp"] = waAdapter.QRState()
		}
		_ = json.NewEncoder(w).Encode(status)
	})
	mux.HandleFunc("GET /qr", func(w http.ResponseWriter, r *http.Request) {
		if waAdapter == nil {
			http.Error(w, "WhatsApp connector desabilitado", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(waAdapter.QRState())
	})

	httpSrv := &http.Server{
		Addr:        httpAddr,
		Handler:     mux,
		ReadTimeout: 10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("falha no servidor HTTP local do connector", slog.Any("error", err))
		}
	}()

	wsErrCh := make(chan error, 1)
	go func() {
		wsErrCh <- bridgeClient.RunWS(ctx, outbound)
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	select {
	case sig := <-quit:
		logger.Info("encerrando connector graciosamente", slog.String("signal", sig.String()))
	case err := <-wsErrCh:
		if err != nil && ctx.Err() == nil {
			logger.Error("loop WebSocket do connector encerrado", slog.Any("error", err))
		}
	}

	cancel()
	if waAdapter != nil {
		_ = waAdapter.Stop(context.Background())
	}
	shutdownCtx, sCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer sCancel()
	_ = httpSrv.Shutdown(shutdownCtx)
}

func getEnv(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if t := strings.TrimSpace(v); t != "" {
			return t
		}
	}
	return ""
}

type noopOutboundHandler struct{}

func (noopOutboundHandler) Send(ctx context.Context, msg core.NormalizedMessage) error {
	return nil
}

func (noopOutboundHandler) SendTyping(ctx context.Context, sourceKey string, typing bool) error {
	return nil
}

func (noopOutboundHandler) Health() core.ChannelHealth {
	return core.ChannelHealth{Healthy: false, Detail: "WhatsApp connector desabilitado"}
}
