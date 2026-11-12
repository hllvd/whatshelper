package server

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"whatshelper/internal/brain"
	"whatshelper/internal/channels/admin"
	"whatshelper/internal/channels/bridge"
	"whatshelper/internal/channels/simulator"
	"whatshelper/internal/config"
	"whatshelper/internal/core"
	"whatshelper/internal/security"
)

//go:embed static/*
var staticFS embed.FS

// Dependencies agrupa as portas de domínio necessárias para a API HTTP e SSE.
type Dependencies struct {
	Store          core.Store
	Blobs          core.BlobStore
	Bus            core.EventBus
	Simulator      *simulator.Channel
	AdminChannel   *admin.Channel
	RemoteChannels map[string]*bridge.RemoteChannel
	Pipeline       *brain.Pipeline
	Knowledge      interface{ Invalidate(tenantID string) }
}

// Server encapsula o servidor HTTP do Brain com logging estruturado e graceful shutdown.
type Server struct {
	httpServer *http.Server
	logger     *slog.Logger
	cfg        config.Config
	deps       Dependencies
	eventRing  *eventBuffer
	ringSub    core.Subscription
	stopCh     chan struct{}
	stopOnce   sync.Once
}

type HealthResponse struct {
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
	Service   string    `json:"service"`
}

// New cria uma instância básica de Server (usada quando apenas endpoints de saúde são necessários).
func New(cfg config.Config, logger *slog.Logger) *Server {
	return NewWithDeps(cfg, logger, Dependencies{})
}

// NewWithDeps cria uma instância completa de Server conectada ao Store, BlobStore, EventBus e Simulador.
func NewWithDeps(cfg config.Config, logger *slog.Logger, deps Dependencies) *Server {
	if logger == nil {
		logger = InitLoggerWithSecrets(cfg.LogLevel, cfg.LogFormat, cfg.Secrets()...)
	}

	s := &Server{
		logger:    logger,
		cfg:       cfg,
		deps:      deps,
		eventRing: newEventBuffer(200),
		stopCh:    make(chan struct{}),
	}

	if deps.Bus != nil {
		sub := deps.Bus.Subscribe(core.EventFilter{})
		s.ringSub = sub
		go func() {
			for {
				select {
				case <-s.stopCh:
					return
				case ev, ok := <-sub.Events():
					if !ok {
						return
					}
					s.eventRing.add(ev)
				}
			}
		}()
	}

	mux := http.NewServeMux()
	s.registerRoutes(mux)

	s.httpServer = &http.Server{
		Addr:         fmt.Sprintf("%s:%d", cfg.HTTPHost, cfg.HTTPPort),
		Handler:      s.authMiddleware(mux),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return s
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	apiToken := strings.TrimSpace(s.cfg.APIToken)
	connToken := strings.TrimSpace(s.cfg.EffectiveConnectorToken())
	if apiToken == "" && connToken == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		expectedToken := apiToken
		if strings.HasPrefix(r.URL.Path, "/api/connectors/v1/") {
			expectedToken = connToken
		}
		if expectedToken == "" {
			next.ServeHTTP(w, r)
			return
		}
		provided := ""
		if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
			provided = strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
		} else if qToken := strings.TrimSpace(r.URL.Query().Get("token")); qToken != "" {
			provided = qToken
		}
		if len(provided) == 0 || subtle.ConstantTimeCompare([]byte(provided), []byte(expectedToken)) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="whatshelper"`)
			writeJSONError(w, http.StatusUnauthorized, "token de autenticação inválido ou ausente")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /readyz", s.handleReadyz)

	// API HTTP (SRV-01, SAF-01, SAF-02)
	mux.HandleFunc("POST /api/sim/messages", s.handlePostSimMessage)
	mux.HandleFunc("GET /api/admin/messages", s.handleGetAdminMessages)
	mux.HandleFunc("POST /api/admin/messages", s.handlePostAdminMessage)
	mux.HandleFunc("GET /api/sources", s.handleListSources)
	mux.HandleFunc("GET /api/sources/{channel}/{key}/state", s.handleGetSourceState)
	mux.HandleFunc("POST /api/sources/{channel}/{key}/status", s.handleUpdateSourceStatus)
	mux.HandleFunc("GET /api/sources/{channel}/{key}/messages", s.handleGetSourceMessages)
	mux.HandleFunc("GET /api/sources/{channel}/{key}/runs", s.handleGetSourceRuns)
	mux.HandleFunc("GET /api/sources/{channel}/{key}/tasks", s.handleGetSourceTasks)
	mux.HandleFunc("GET /api/tasks/pending", s.handleListPendingTasks)
	mux.HandleFunc("POST /api/tasks/{id}/complete", s.handleCompleteTask)
	mux.HandleFunc("POST /api/tasks/{id}/approve", s.handleApproveTask)
	mux.HandleFunc("POST /api/replay", s.handlePostReplay)
	mux.HandleFunc("GET /api/contacts/{contact_id}/sources", s.handleListContactSources)
	mux.HandleFunc("GET /api/knowledge", s.handleListKnowledge)
	mux.HandleFunc("GET /api/knowledge/{file}", s.handleGetKnowledgeFile)
	mux.HandleFunc("PUT /api/knowledge/{file}", s.handlePutKnowledgeFile)
	mux.HandleFunc("GET /api/knowledge/{file}/versions", s.handleListKnowledgeVersions)
	mux.HandleFunc("GET /api/learning/pending", s.handleListPendingLearning)
	mux.HandleFunc("POST /api/learning/{id}/approve", s.handleApproveLearning)
	mux.HandleFunc("POST /api/learning/{id}/reject", s.handleRejectLearning)
	mux.HandleFunc("POST /api/learning/correct", s.handleCorrectLearning)
	mux.HandleFunc("GET /api/runs/{id}/trace", s.handleGetRunTrace)
	mux.HandleFunc("GET /api/tenants/{id}/policy", s.handleGetTenantPolicy)
	mux.HandleFunc("PUT /api/tenants/{id}/policy", s.handlePutTenantPolicy)

	// Protocolo Connector <-> Brain v1 (CH-02)
	mux.HandleFunc("POST /api/connectors/v1/inbound", s.handleConnectorInbound)
	mux.HandleFunc("POST /api/connectors/v1/media", s.handleConnectorMedia)
	mux.HandleFunc("GET /api/connectors/v1/ws", s.handleConnectorWS)

	// Stream de eventos SSE (SRV-02)
	mux.HandleFunc("GET /api/events", s.handleEvents)

	// UIs estáticas (SRV-03, SRV-04, SRV-05)
	subFS, _ := fs.Sub(staticFS, "static")
	fileServer := http.FileServer(http.FS(subFS))
	mux.Handle("GET /static/", http.StripPrefix("/static/", fileServer))
	mux.HandleFunc("GET /{$}", s.serveStaticFile(subFS, "index.html", "text/html; charset=utf-8"))
	mux.HandleFunc("GET /thinking", s.serveStaticFile(subFS, "thinking.html", "text/html; charset=utf-8"))
	mux.HandleFunc("GET /knowledge", s.serveStaticFile(subFS, "knowledge.html", "text/html; charset=utf-8"))
}

func (s *Server) serveStaticFile(fsys fs.FS, name, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	}
}

// Handler retorna o http.Handler configurado (útil para testes unitários).
func (s *Server) Handler() http.Handler {
	return s.httpServer.Handler
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	resp := HealthResponse{
		Status:    "ok",
		Timestamp: time.Now().UTC(),
		Service:   "whatshelper-brain",
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	resp := HealthResponse{
		Status:    "ready",
		Timestamp: time.Now().UTC(),
		Service:   "whatshelper-brain",
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// Start inicializa o listener HTTP de forma síncrona.
func (s *Server) Start() error {
	s.logger.Info("servidor iniciando",
		slog.String("host", s.cfg.HTTPHost),
		slog.Int("port", s.cfg.HTTPPort),
		slog.String("env", s.cfg.Environment),
	)
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("falha ao rodar servidor HTTP: %w", err)
	}
	return nil
}

// Shutdown executa o encerramento gracioso do servidor.
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("encerrando servidor graciosamente")
	s.stopOnce.Do(func() {
		close(s.stopCh)
		if s.ringSub != nil {
			s.ringSub.Cancel()
		}
	})
	return s.httpServer.Shutdown(ctx)
}

// InitLogger constrói um slog.Logger estruturado com redação de PII por padrão.
func InitLogger(levelStr, format string) *slog.Logger {
	return InitLoggerWithSecrets(levelStr, format)
}

// InitLoggerWithSecrets constrói um slog.Logger estruturado mascarando PII e segredos configurados.
func InitLoggerWithSecrets(levelStr, format string, secrets ...string) *slog.Logger {
	var level slog.Level
	switch levelStr {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{
		Level: level,
	}

	var handler slog.Handler
	if format == "json" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	return slog.New(security.NewRedactingHandler(handler, secrets...))
}
