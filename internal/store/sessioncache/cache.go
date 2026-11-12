package sessioncache

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrSessionNotFound = errors.New("sessão não encontrada no cache local")
)

// ActiveSession representa o estado ativo de uma conversa mantido em cache rápido.
type ActiveSession struct {
	TenantID     string            `json:"tenant_id"`
	Channel      string            `json:"channel"`
	SourceKey    string            `json:"source_key"`
	CurrentState string            `json:"current_state"`
	LastActivity time.Time         `json:"last_activity"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// Cache gerencia o acesso rápido em SQLite às sessões ativas de conversação.
type Cache struct {
	db *sql.DB
	mu sync.RWMutex
}

// Open inicializa uma conexão com o SQLite no caminho informado (ou ":memory:").
func Open(dbPath string) (*Cache, error) {
	if dbPath != ":memory:" && !stringsHasPrefix(dbPath, "file:") {
		dir := filepath.Dir(dbPath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("falha ao criar diretório para session cache: %w", err)
		}
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("falha ao abrir banco sqlite de sessões: %w", err)
	}

	// SQLite settings para alta concorrência local
	db.SetMaxOpenConns(1)

	schema := `
	CREATE TABLE IF NOT EXISTS active_sessions (
		tenant_id TEXT NOT NULL,
		channel TEXT NOT NULL,
		source_key TEXT NOT NULL,
		current_state TEXT NOT NULL,
		last_activity DATETIME NOT NULL,
		metadata_json TEXT,
		PRIMARY KEY (tenant_id, channel, source_key)
	);
	CREATE INDEX IF NOT EXISTS idx_active_sessions_activity ON active_sessions(last_activity);
	`
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("falha ao criar schema de sessões: %w", err)
	}

	return &Cache{db: db}, nil
}

func stringsHasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// GetSession busca a sessão ativa no cache rápido.
func (c *Cache) GetSession(ctx context.Context, tenantID, channel, sourceKey string) (*ActiveSession, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	query := `
	SELECT tenant_id, channel, source_key, current_state, last_activity, metadata_json
	FROM active_sessions
	WHERE tenant_id = ? AND channel = ? AND source_key = ?
	`
	row := c.db.QueryRowContext(ctx, query, tenantID, channel, sourceKey)

	var sess ActiveSession
	var rawMeta sql.NullString
	var lastAct string

	if err := row.Scan(&sess.TenantID, &sess.Channel, &sess.SourceKey, &sess.CurrentState, &lastAct, &rawMeta); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrSessionNotFound
		}
		return nil, fmt.Errorf("erro ao ler sessão ativa: %w", err)
	}

	parsedTime, err := time.Parse(time.RFC3339Nano, lastAct)
	if err != nil {
		parsedTime, _ = time.Parse("2006-01-02 15:04:05.999999999-07:00", lastAct)
	}
	sess.LastActivity = parsedTime

	if rawMeta.Valid && rawMeta.String != "" {
		_ = json.Unmarshal([]byte(rawMeta.String), &sess.Metadata)
	}

	return &sess, nil
}

// SaveSession insere ou atualiza o estado da sessão ativa.
func (c *Cache) SaveSession(ctx context.Context, sess *ActiveSession) error {
	if sess == nil {
		return errors.New("sessão nula")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	var metaJSON []byte
	if len(sess.Metadata) > 0 {
		var err error
		metaJSON, err = json.Marshal(sess.Metadata)
		if err != nil {
			return fmt.Errorf("falha ao serializar metadados da sessão: %w", err)
		}
	}

	if sess.LastActivity.IsZero() {
		sess.LastActivity = time.Now().UTC()
	}

	query := `
	INSERT INTO active_sessions (tenant_id, channel, source_key, current_state, last_activity, metadata_json)
	VALUES (?, ?, ?, ?, ?, ?)
	ON CONFLICT(tenant_id, channel, source_key) DO UPDATE SET
		current_state = excluded.current_state,
		last_activity = excluded.last_activity,
		metadata_json = excluded.metadata_json
	`
	_, err := c.db.ExecContext(ctx, query,
		sess.TenantID,
		sess.Channel,
		sess.SourceKey,
		sess.CurrentState,
		sess.LastActivity.Format(time.RFC3339Nano),
		string(metaJSON),
	)
	if err != nil {
		return fmt.Errorf("falha ao salvar sessão ativa: %w", err)
	}
	return nil
}

// DeleteSession remove uma sessão do cache rápido.
func (c *Cache) DeleteSession(ctx context.Context, tenantID, channel, sourceKey string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	_, err := c.db.ExecContext(ctx, "DELETE FROM active_sessions WHERE tenant_id = ? AND channel = ? AND source_key = ?",
		tenantID, channel, sourceKey)
	return err
}

// Close encerra a conexão com o banco SQLite.
func (c *Cache) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.db != nil {
		return c.db.Close()
	}
	return nil
}
