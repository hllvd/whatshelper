package brain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"whatshelper/internal/core"
	"whatshelper/internal/pack"
	"whatshelper/internal/store"
)

// TenantConfig representa a configuração carregada para um tenant.
type TenantConfig struct {
	ID               string `yaml:"id" json:"id"`
	Name             string `yaml:"name" json:"name"`
	Pack             string `yaml:"pack" json:"pack"`
	ProactiveEnabled bool   `yaml:"proactive_enabled" json:"proactive_enabled"`
}

// TenantConfigLoader resolve a configuração de um tenant.
type TenantConfigLoader interface {
	LoadTenant(ctx context.Context, tenantID string) (TenantConfig, error)
}

// LocalTenantLoader carrega <tenantsDir>/<tenant>/tenant.yaml do sistema de arquivos.
type LocalTenantLoader struct {
	tenantsDir string
}

func NewLocalTenantLoader(tenantsDir string) *LocalTenantLoader {
	return &LocalTenantLoader{tenantsDir: tenantsDir}
}

func (l *LocalTenantLoader) LoadTenant(ctx context.Context, tenantID string) (TenantConfig, error) {
	cleanID := filepath.Base(strings.TrimSpace(tenantID))
	if cleanID == "" || cleanID == "." || cleanID == ".." {
		return TenantConfig{}, fmt.Errorf("tenant_id inválido: %q", tenantID)
	}
	path := filepath.Join(l.tenantsDir, cleanID, "tenant.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		return TenantConfig{}, fmt.Errorf("falha ao ler tenant.yaml (%s): %w", path, err)
	}
	var cfg TenantConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return TenantConfig{}, fmt.Errorf("falha ao parsear tenant.yaml: %w", err)
	}
	if strings.TrimSpace(cfg.ID) == "" {
		cfg.ID = cleanID
	}
	if strings.TrimSpace(cfg.Pack) == "" {
		return TenantConfig{}, fmt.Errorf("tenant %q sem pack definido", cleanID)
	}
	return cfg, nil
}

// KnowledgeBundle reúne os documentos .md estáticos e o arquivo QA.md separado.
type KnowledgeBundle struct {
	Docs        []pack.KnowledgeDoc `json:"docs"`
	QAContent   string              `json:"qa_content"`
	Fingerprint string              `json:"fingerprint"`
}

// KnowledgeSource fornece a base de conhecimento do tenant com suporte a recarga.
type KnowledgeSource interface {
	Load(ctx context.Context, tenantID string) (KnowledgeBundle, error)
}

type cachedBundle struct {
	bundle    KnowledgeBundle
	checkedAt time.Time
}

// LocalKnowledgeSource lê <tenantsDir>/<tenant>/knowledge/*.md com cache invalidado por fingerprint de arquivos.
type LocalKnowledgeSource struct {
	tenantsDir string
	mu         sync.RWMutex
	cache      map[string]KnowledgeBundle
}

func NewLocalKnowledgeSource(tenantsDir string) *LocalKnowledgeSource {
	return &LocalKnowledgeSource{
		tenantsDir: tenantsDir,
		cache:      make(map[string]KnowledgeBundle),
	}
}

func (s *LocalKnowledgeSource) Load(ctx context.Context, tenantID string) (KnowledgeBundle, error) {
	cleanID := filepath.Base(strings.TrimSpace(tenantID))
	dir := filepath.Join(s.tenantsDir, cleanID, "knowledge")

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return KnowledgeBundle{}, nil
		}
		return KnowledgeBundle{}, fmt.Errorf("erro ao listar conhecimento local (%s): %w", dir, err)
	}

	type fileMeta struct {
		name  string
		path  string
		size  int64
		mtime int64
	}

	var files []fileMeta
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".md") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return KnowledgeBundle{}, err
		}
		files = append(files, fileMeta{
			name:  entry.Name(),
			path:  filepath.Join(dir, entry.Name()),
			size:  info.Size(),
			mtime: info.ModTime().UnixNano(),
		})
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].name < files[j].name
	})

	h := sha256.New()
	for _, f := range files {
		_, _ = fmt.Fprintf(h, "%s:%d:%d;", f.name, f.size, f.mtime)
	}
	fp := hex.EncodeToString(h.Sum(nil))

	s.mu.RLock()
	cached, ok := s.cache[cleanID]
	s.mu.RUnlock()
	if ok && cached.Fingerprint == fp {
		return cached, nil
	}

	var docs []pack.KnowledgeDoc
	var qaContent string
	for _, f := range files {
		raw, err := os.ReadFile(f.path)
		if err != nil {
			return KnowledgeBundle{}, fmt.Errorf("erro ao ler %s: %w", f.path, err)
		}
		content := strings.TrimSpace(string(raw))
		if strings.EqualFold(f.name, "QA.md") {
			qaContent = content
			continue
		}
		docs = append(docs, pack.KnowledgeDoc{
			Name:    f.name,
			Content: content,
		})
	}

	bundle := KnowledgeBundle{
		Docs:        docs,
		QAContent:   qaContent,
		Fingerprint: fp,
	}

	s.mu.Lock()
	s.cache[cleanID] = bundle
	s.mu.Unlock()

	return bundle, nil
}

// BlobKnowledgeSource lê conhecimento versionado de um core.BlobStore com cache por TTL curto.
type BlobKnowledgeSource struct {
	blobs core.BlobStore
	clock core.Clock
	ttl   time.Duration
	mu    sync.RWMutex
	cache map[string]cachedBundle
}

func NewBlobKnowledgeSource(blobs core.BlobStore, clock core.Clock, ttl time.Duration) *BlobKnowledgeSource {
	if clock == nil {
		clock = core.SystemClock{}
	}
	if ttl <= 0 {
		ttl = 5 * time.Second
	}
	return &BlobKnowledgeSource{
		blobs: blobs,
		clock: clock,
		ttl:   ttl,
		cache: make(map[string]cachedBundle),
	}
}

func (s *BlobKnowledgeSource) Load(ctx context.Context, tenantID string) (KnowledgeBundle, error) {
	now := s.clock.Now().UTC()
	s.mu.RLock()
	entry, ok := s.cache[tenantID]
	s.mu.RUnlock()
	if ok && now.Sub(entry.checkedAt) < s.ttl {
		return entry.bundle, nil
	}

	prefix := store.S3KnowledgeKey(tenantID, "")
	keys, err := s.blobs.List(ctx, prefix)
	if err != nil {
		return KnowledgeBundle{}, err
	}
	sort.Strings(keys)

	h := sha256.New()
	var docs []pack.KnowledgeDoc
	var qaContent string

	for _, key := range keys {
		if !strings.HasSuffix(strings.ToLower(key), ".md") {
			continue
		}
		raw, err := s.blobs.Get(ctx, key)
		if err != nil {
			return KnowledgeBundle{}, err
		}
		name := filepath.Base(key)
		content := strings.TrimSpace(string(raw))
		_, _ = fmt.Fprintf(h, "%s:%x;", name, sha256.Sum256(raw))

		if strings.EqualFold(name, "QA.md") {
			qaContent = content
			continue
		}
		docs = append(docs, pack.KnowledgeDoc{
			Name:    name,
			Content: content,
		})
	}

	bundle := KnowledgeBundle{
		Docs:        docs,
		QAContent:   qaContent,
		Fingerprint: hex.EncodeToString(h.Sum(nil)),
	}

	s.mu.Lock()
	s.cache[tenantID] = cachedBundle{bundle: bundle, checkedAt: now}
	s.mu.Unlock()

	return bundle, nil
}
