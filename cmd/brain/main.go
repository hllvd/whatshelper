package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"whatshelper/internal/brain"
	"whatshelper/internal/channels/admin"
	"whatshelper/internal/channels/bridge"
	"whatshelper/internal/channels/simulator"
	"whatshelper/internal/config"
	"whatshelper/internal/core"
	"whatshelper/internal/llm"
	"whatshelper/internal/media"
	"whatshelper/internal/pack"
	"whatshelper/internal/security"
	"whatshelper/internal/server"
	"whatshelper/internal/store/dynamo"
	"whatshelper/internal/store/s3blob"
	"whatshelper/internal/store/sessioncache"
)

func main() {
	if err := config.LoadDotEnv(""); err != nil {
		slog.Error("falha ao carregar arquivo .env", slog.Any("error", err))
		os.Exit(1)
	}
	cfg := config.LoadFromEnv()
	logger := server.InitLoggerWithSecrets(cfg.LogLevel, cfg.LogFormat, cfg.Secrets()...)
	slog.SetDefault(logger)

	if len(os.Args) >= 3 && os.Args[1] == "store" && os.Args[2] == "init" {
		if err := runStoreInit(cfg, logger); err != nil {
			logger.Error("falha ao inicializar armazenamento", slog.Any("error", err))
			os.Exit(1)
		}
		return
	}

	if len(os.Args) >= 4 && os.Args[1] == "pack" && os.Args[2] == "validate" {
		if err := runPackValidate(os.Args[3:]); err != nil {
			logger.Error("validação de pack falhou", slog.Any("error", err))
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

	llmRouter, err := llm.NewRouterFromConfig(appCtx, cfg, clock)
	if err != nil {
		logger.Error("falha ao inicializar LLM", slog.Any("error", err))
		os.Exit(1)
	}
	defaults := llmRouter.StageDefaults()
	analysisModel := defaults["analysis"].Model
	responseModel := defaults["response"].Model
	nudgeModel := defaults["nudge"].Model

	bus := core.NewInMemoryEventBus(128)
	simCh := simulator.New(clock, 128)
	if err := simCh.Start(appCtx); err != nil {
		logger.Error("falha ao iniciar simulador", slog.Any("error", err))
		os.Exit(1)
	}
	adminCh := admin.New(clock, 128)
	if err := adminCh.Start(appCtx); err != nil {
		logger.Error("falha ao iniciar canal admin", slog.Any("error", err))
		os.Exit(1)
	}
	whatsappRemoteCh := bridge.NewRemoteChannel("whatsapp", core.ChannelCapabilities{
		Typing:             true,
		Media:              true,
		ReportsOwnMessages: true,
	}, logger)
	_ = whatsappRemoteCh.Start(appCtx)

	knowledgeSrc := brain.NewBlobKnowledgeSource(blobRepo, clock, 5*time.Second)
	piiCipher, err := security.NewPIICipherFromEnv(cfg.PIIEncryptionKey, cfg.Environment)
	if err != nil {
		logger.Error("falha ao inicializar cifra PII", slog.Any("error", err))
		os.Exit(1)
	}

	audioModel := cfg.AudioModel
	if audioModel == "" {
		audioModel = cfg.TranscriptionModel
	}
	var transcriber core.AudioTranscriber
	if t := media.NewDynamicAudioTranscriber(cfg.TranscriptionURL, cfg.TranscriptionAPIKey, cfg.TranscriptionModel, cfg.OpenAIAPIKey, cfg.OpenAIBaseURL, cfg.LLMProvider, audioModel, nil); t != nil {
		transcriber = t
	}

	sessionCachePath := os.Getenv("SESSION_CACHE_PATH")
	if sessionCachePath == "" {
		sessionCachePath = filepath.Join("whatsapp_data", "session_cache.db")
	}
	sessionCache, scErr := sessioncache.Open(sessionCachePath)
	if scErr != nil {
		logger.Warn("não foi possível abrir cache SQLite de sessões; operando sem cache rápido local", slog.Any("error", scErr))
	} else {
		defer sessionCache.Close()
	}

	pipeline := brain.NewPipeline(appCtx, brain.PipelineConfig{
		Store:               storeRepo,
		Blobs:               blobRepo,
		LLM:                 llmRouter,
		Bus:                 bus,
		Clock:               clock,
		Tenants:             brain.NewStoreTenantPolicyLoader(storeRepo, brain.NewLocalTenantLoader(cfg.TenantsDir)),
		Knowledge:           knowledgeSrc,
		PackLoader:          func(name string) (*pack.Pack, error) { return pack.LoadFromDir(filepath.Join(cfg.PacksDir, name)) },
		AnalysisModel:       analysisModel,
		ResponseModel:       responseModel,
		NudgeModel:          nudgeModel,
		LearningModel:       cfg.LearningModel,
		VisionModel:         cfg.VisionModel,
		AudioModel:          audioModel,
		Debounce:            time.Duration(cfg.DebounceMs) * time.Millisecond,
		RecentMessagesLimit: cfg.RecentMessagesLimit,
		RunRetentionDays:    cfg.RunRetentionDays,
		PIICipher:           piiCipher,
		Transcriber:         transcriber,
		SessionCache:        sessionCache,
		Logger:              logger,
	})
	pipeline.RegisterChannel(simCh)
	pipeline.RegisterChannel(adminCh)
	pipeline.RegisterChannel(whatsappRemoteCh)
	pipeline.StartNudgeWorker(appCtx, 30*time.Second, func() []string {
		return discoverLocalTenants(cfg.TenantsDir)
	})

	go consumeChannel(appCtx, simCh, pipeline, logger)
	go consumeChannel(appCtx, adminCh, pipeline, logger)

	srv := server.NewWithDeps(cfg, logger, server.Dependencies{
		Store:          storeRepo,
		Blobs:          blobRepo,
		Bus:            bus,
		Simulator:      simCh,
		AdminChannel:   adminCh,
		RemoteChannels: map[string]*bridge.RemoteChannel{"whatsapp": whatsappRemoteCh},
		Pipeline:       pipeline,
		Knowledge:      knowledgeSrc,
		LLMRouter:      llmRouter,
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
	_ = adminCh.Stop(context.Background())
	_ = whatsappRemoteCh.Stop(context.Background())
	pipeline.Stop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("erro ao encerrar servidor graciosamente", slog.Any("error", err))
		os.Exit(1)
	}

	logger.Info("Whatshelper Brain finalizado com sucesso")
}

func consumeChannel(ctx context.Context, ch core.Channel, pipeline *brain.Pipeline, logger *slog.Logger) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch.Messages():
			if !ok {
				return
			}
			if err := pipeline.Ingest(ctx, msg); err != nil {
				logger.Error("erro ao ingerir mensagem do canal",
					slog.String("channel", ch.ID()),
					slog.String("message_id", msg.MessageID),
					slog.Any("error", err),
				)
			}
		}
	}
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

	blobRepo := s3blob.NewStore(s3Client, cfg.S3Bucket)
	seededDocs, err := brain.SeedLocalKnowledge(ctx, cfg.TenantsDir, blobRepo)
	if err != nil {
		return err
	}
	logger.Info("documentos de conhecimento semeados", slog.Any("keys", seededDocs))
	return nil
}

func runPackValidate(dirs []string) error {
	for _, dir := range dirs {
		pk, err := pack.LoadFromDir(dir)
		if err != nil {
			return fmt.Errorf("%s: %w", dir, err)
		}
		if _, err := pack.BuildAnalysisSchema(pk); err != nil {
			return fmt.Errorf("%s schema: %w", dir, err)
		}
		sampleInput := pack.PromptInput{
			Pack:    pk,
			Summary: "Validação sintática",
			NewMessage: pack.PromptMessage{
				Sender:    pk.Actors.Customer,
				Text:      "Mensagem de validação",
				Timestamp: "2026-11-11T12:00:00Z",
			},
			PlainTextOnly: true,
		}
		if _, err := pack.RenderAnalysis(pk, sampleInput); err != nil {
			return fmt.Errorf("%s render analysis: %w", dir, err)
		}
		if _, err := pack.RenderReply(pk, sampleInput); err != nil {
			return fmt.Errorf("%s render reply: %w", dir, err)
		}
		if pk.NudgeTmpl != nil {
			if _, err := pack.RenderNudge(pk, sampleInput); err != nil {
				return fmt.Errorf("%s render nudge: %w", dir, err)
			}
		}
		fmt.Printf("OK: pack %q (%s) válido (%d facts, %d signals, %d intents, %d task_rules)\n",
			pk.Name, dir, len(pk.Facts), len(pk.Signals), len(pk.Intents), len(pk.TaskRules))
	}
	return nil
}
