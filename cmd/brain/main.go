package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"whatshelper/internal/config"
	"whatshelper/internal/server"
)

func main() {
	cfg := config.LoadFromEnv()
	logger := server.InitLogger(cfg.LogLevel, cfg.LogFormat)
	slog.SetDefault(logger)

	logger.Info("inicializando Whatshelper Brain",
		slog.String("env", cfg.Environment),
		slog.String("table", cfg.DynamoDBTable),
		slog.String("bucket", cfg.S3Bucket),
	)

	srv := server.New(cfg, logger)

	// Inicia servidor HTTP em background
	serverErrCh := make(chan error, 1)
	go func() {
		if err := srv.Start(); err != nil {
			serverErrCh <- err
		}
	}()

	// Captura sinais de terminação para graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	select {
	case sig := <-quit:
		logger.Info("sinal recebido, encerrando servidor", slog.String("signal", sig.String()))
	case err := <-serverErrCh:
		logger.Error("falha fatal no servidor", slog.Any("error", err))
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("erro ao encerrar servidor graciosamente", slog.Any("error", err))
		os.Exit(1)
	}

	logger.Info("Whatshelper Brain finalizado com sucesso")
}
