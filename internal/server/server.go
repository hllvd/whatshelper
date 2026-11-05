package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"whatshelper/internal/config"
)

// Server encapsula o servidor HTTP do Brain com logging estruturado e graceful shutdown.
type Server struct {
	httpServer *http.Server
	logger     *slog.Logger
	cfg        config.Config
}

type HealthResponse struct {
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
	Service   string    `json:"service"`
}

// New cria uma nova instância de Server configurada.
func New(cfg config.Config, logger *slog.Logger) *Server {
	if logger == nil {
		logger = InitLogger(cfg.LogLevel, cfg.LogFormat)
	}

	mux := http.NewServeMux()
	s := &Server{
		logger: logger,
		cfg:    cfg,
	}

	s.registerRoutes(mux)

	s.httpServer = &http.Server{
		Addr:         fmt.Sprintf("%s:%d", cfg.HTTPHost, cfg.HTTPPort),
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return s
}

func (s *Server) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /readyz", s.handleReadyz)
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
	return s.httpServer.Shutdown(ctx)
}

// InitLogger constrói um slog.Logger estruturado de acordo com o nível e formato desejados.
func InitLogger(levelStr, format string) *slog.Logger {
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

	return slog.New(handler)
}
