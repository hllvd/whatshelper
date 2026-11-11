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
	"whatshelper/internal/store/dynamo"
	"whatshelper/internal/store/s3blob"
)

func main() {
	cfg := config.LoadFromEnv()
	logger := server.InitLogger(cfg.LogLevel, cfg.LogFormat)
	slog.SetDefault(logger)

	if len(os.Args) >= 3 && os.Args[1] == "store" && os.Args[2] == "init" {
		if err := runStoreInit(cfg, logger); err != nil {
			logger.Error("falha ao inicializar armazenamento", slog.Any("error", err))
			os.Exit(1)
		}
		return
	}

	if err := cfg.Validate(); err != nil {
		logger.Error("configuração inválida", slog.Any("error", err))
		os.Exit(1)
	}

	logger.Info("inicializando Whatshelper Brain",
		slog.String("env", cfg.Environment),
		slog.String("llm_provider", cfg.LLMProvider),
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

func runStoreInit(cfg config.Config, logger *slog.Logger) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ddbClient, err := dynamo.NewClient(ctx, cfg.DynamoDBEndpoint, cfg.S3Region, cfg.S3AccessKey, cfg.S3SecretKey)
	if err != nil {
		return err
	}
	if err := dynamo.EnsureTable(ctx, ddbClient, cfg.DynamoDBTable); err != nil {
		return err
	}
	logger.Info("tabela DynamoDB verificada/criada", slog.String("table", cfg.DynamoDBTable))

	s3Client, err := s3blob.NewClient(ctx, cfg.S3Endpoint, cfg.S3Region, cfg.S3AccessKey, cfg.S3SecretKey)
	if err != nil {
		return err
	}
	if err := s3blob.EnsureBucket(ctx, s3Client, cfg.S3Bucket); err != nil {
		return err
	}
	logger.Info("bucket S3 verificado/criado", slog.String("bucket", cfg.S3Bucket))
	return nil
}
