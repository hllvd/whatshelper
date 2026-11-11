package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"whatshelper/internal/brain"
	"whatshelper/internal/channels/simulator"
	"whatshelper/internal/config"
	"whatshelper/internal/core"
	"whatshelper/internal/core/fakes"
	"whatshelper/internal/llm/anthropic"
	"whatshelper/internal/pack"
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

	appCtx, appCancel := context.WithCancel(context.Background())
	defer appCancel()

	clock := core.SystemClock{}

	ddbClient, err := dynamo.NewClient(appCtx, cfg.DynamoDBEndpoint, cfg.S3Region, cfg.S3AccessKey, cfg.S3SecretKey)
	if err != nil {
		logger.Error("falha ao criar cliente DynamoDB", slog.Any("error", err))
		os.Exit(1)
	}
	storeRepo := dynamo.NewStore(ddbClient, cfg.DynamoDBTable, clock)

	s3Client, err := s3blob.NewClient(appCtx, cfg.S3Endpoint, cfg.S3Region, cfg.S3AccessKey, cfg.S3SecretKey)
	if err != nil {
		logger.Error("falha ao criar cliente S3", slog.Any("error", err))
		os.Exit(1)
	}
	blobRepo := s3blob.NewStore(s3Client, cfg.S3Bucket)

	llmClient, analysisModel, responseModel, nudgeModel, err := buildLLM(cfg, clock)
	if err != nil {
		logger.Error("falha ao inicializar LLM", slog.Any("error", err))
		os.Exit(1)
	}

	bus := core.NewInMemoryEventBus(128)
	simCh := simulator.New(clock, 128)
	if err := simCh.Start(appCtx); err != nil {
		logger.Error("falha ao iniciar simulador", slog.Any("error", err))
		os.Exit(1)
	}

	pipeline := brain.NewPipeline(appCtx, brain.PipelineConfig{
		Store:               storeRepo,
		Blobs:               blobRepo,
		LLM:                 llmClient,
		Bus:                 bus,
		Clock:               clock,
		Tenants:             brain.NewStoreTenantPolicyLoader(storeRepo, brain.NewLocalTenantLoader(cfg.TenantsDir)),
		Knowledge:           brain.NewLocalKnowledgeSource(cfg.TenantsDir),
		PackLoader:          func(name string) (*pack.Pack, error) { return pack.LoadFromDir(filepath.Join(cfg.PacksDir, name)) },
		AnalysisModel:       analysisModel,
		ResponseModel:       responseModel,
		NudgeModel:          nudgeModel,
		Debounce:            time.Duration(cfg.DebounceMs) * time.Millisecond,
		RecentMessagesLimit: cfg.RecentMessagesLimit,
		RunRetentionDays:    cfg.RunRetentionDays,
		Logger:              logger,
	})
	pipeline.RegisterChannel(simCh)
	pipeline.StartNudgeWorker(appCtx, 30*time.Second, func() []string {
		return discoverLocalTenants(cfg.TenantsDir)
	})

	// Consumidor das mensagens do simulador para ingestão no pipeline
	go func() {
		for {
			select {
			case <-appCtx.Done():
				return
			case msg, ok := <-simCh.Messages():
				if !ok {
					return
				}
				if err := pipeline.Ingest(appCtx, msg); err != nil {
					logger.Error("erro ao ingerir mensagem do simulador",
						slog.String("message_id", msg.MessageID),
						slog.Any("error", err),
					)
				}
			}
		}
	}()

	srv := server.NewWithDeps(cfg, logger, server.Dependencies{
		Store:     storeRepo,
		Blobs:     blobRepo,
		Bus:       bus,
		Simulator: simCh,
		Pipeline:  pipeline,
	})

	serverErrCh := make(chan error, 1)
	go func() {
		if err := srv.Start(); err != nil {
			serverErrCh <- err
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	select {
	case sig := <-quit:
		logger.Info("sinal recebido, encerrando servidor", slog.String("signal", sig.String()))
	case err := <-serverErrCh:
		logger.Error("falha fatal no servidor", slog.Any("error", err))
		os.Exit(1)
	}

	appCancel()
	_ = simCh.Stop(context.Background())
	pipeline.Stop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("erro ao encerrar servidor graciosamente", slog.Any("error", err))
		os.Exit(1)
	}

	logger.Info("Whatshelper Brain finalizado com sucesso")
}

func buildLLM(cfg config.Config, clock core.Clock) (core.LLM, string, string, string, error) {
	if cfg.LLMProvider == "anthropic" {
		client, err := anthropic.New(cfg.AnthropicAPIKey, clock)
		if err != nil {
			return nil, "", "", "", err
		}
		return client, cfg.AnalysisModel, cfg.ResponseModel, cfg.NudgeModel, nil
	}
	analysisModel := cfg.AnalysisModel
	if analysisModel == "" {
		analysisModel = "fake-analysis"
	}
	responseModel := cfg.ResponseModel
	if responseModel == "" {
		responseModel = "fake-response"
	}
	nudgeModel := cfg.NudgeModel
	if nudgeModel == "" {
		nudgeModel = "fake-nudge"
	}
	return fakes.NewOfflineDemoLLM(), analysisModel, responseModel, nudgeModel, nil
}

func discoverLocalTenants(tenantsDir string) []string {
	entries, err := os.ReadDir(tenantsDir)
	if err != nil {
		return nil
	}
	var ids []string
	for _, entry := range entries {
		if entry.IsDir() {
			if _, err := os.Stat(filepath.Join(tenantsDir, entry.Name(), "tenant.yaml")); err == nil {
				ids = append(ids, entry.Name())
			}
		}
	}
	return ids
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

	storeRepo := dynamo.NewStore(ddbClient, cfg.DynamoDBTable, core.SystemClock{})
	seeded, err := brain.SeedLocalTenants(ctx, cfg.TenantsDir, storeRepo)
	if err != nil {
		return err
	}
	logger.Info("políticas de tenants carregadas", slog.Any("tenants", seeded))
	return nil
}
