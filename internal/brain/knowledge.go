package brain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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

const PolicyConfigKey = "policy"

// TenantConfig representa a configuração e política carregada para um tenant.
type TenantConfig = core.TenantPolicy

// TenantConfigLoader resolve a configuração e política de um tenant.
type TenantConfigLoader interface {
	LoadTenant(ctx context.Context, tenantID string) (TenantConfig, error)
}

// ParseTenantPolicyYAML valida e normaliza o YAML de tenant.yaml em uma TenantPolicy completa.
func ParseTenantPolicyYAML(defaultID string, raw []byte) (core.TenantPolicy, error) {
	var policy core.TenantPolicy
	if err := yaml.Unmarshal(raw, &policy); err != nil {
		return core.TenantPolicy{}, fmt.Errorf("falha ao parsear tenant.yaml: %w", err)
	}
	var overrides struct {
		BotEnabled *bool `yaml:"bot_enabled"`
	}
	if err := yaml.Unmarshal(raw, &overrides); err != nil {
		return core.TenantPolicy{}, fmt.Errorf("falha ao parsear flags de tenant.yaml: %w", err)
	}
	if overrides.BotEnabled == nil {
		policy.BotEnabled = true
	}
	if strings.TrimSpace(policy.ID) == "" {
		policy.ID = defaultID
	}
	policy = policy.WithDefaults()
	if err := policy.Validate(); err != nil {
		return core.TenantPolicy{}, fmt.Errorf("política inválida para tenant %q: %w", policy.ID, err)
	}
	return policy, nil
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
	return ParseTenantPolicyYAML(cleanID, raw)
}

// StoreTenantPolicyLoader lê CONFIG#policy do core.Store (DynamoDB/Memória), com fallback opcional.
type StoreTenantPolicyLoader struct {
	store    core.Store
	fallback TenantConfigLoader
}

func NewStoreTenantPolicyLoader(st core.Store, fallback TenantConfigLoader) *StoreTenantPolicyLoader {
	return &StoreTenantPolicyLoader{store: st, fallback: fallback}
}

func (l *StoreTenantPolicyLoader) LoadTenant(ctx context.Context, tenantID string) (TenantConfig, error) {
	raw, err := l.store.GetTenantConfig(ctx, tenantID, PolicyConfigKey)
	if err == nil {
		var policy core.TenantPolicy
		if err := json.Unmarshal(raw, &policy); err != nil {
			return TenantConfig{}, fmt.Errorf("falha ao decodificar CONFIG#policy do tenant %q: %w", tenantID, err)
		}
		policy = policy.WithDefaults()
		if err := policy.Validate(); err != nil {
			return TenantConfig{}, err
		}
		return policy, nil
	}
	if errors.Is(err, core.ErrConfigNotFound) && l.fallback != nil {
		return l.fallback.LoadTenant(ctx, tenantID)
	}
	return TenantConfig{}, err
}

// SeedTenantPolicy grava a política de um tenant em CONFIG#policy no core.Store.
func SeedTenantPolicy(ctx context.Context, st core.Store, policy core.TenantPolicy) error {
	normalized := policy.WithDefaults()
	if err := normalized.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(normalized)
	if err != nil {
		return err
	}
	return st.SaveTenantConfig(ctx, normalized.ID, PolicyConfigKey, data)
}

// SeedLocalTenants carrega todos os <tenantsDir>/*/tenant.yaml e grava no core.Store.
func SeedLocalTenants(ctx context.Context, tenantsDir string, st core.Store) ([]string, error) {
	entries, err := os.ReadDir(tenantsDir)
	if err != nil {
		return nil, fmt.Errorf("erro ao listar tenantsDir (%s): %w", tenantsDir, err)
	}
	loader := NewLocalTenantLoader(tenantsDir)
	var seeded []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		policyPath := filepath.Join(tenantsDir, entry.Name(), "tenant.yaml")
		if _, statErr := os.Stat(policyPath); statErr != nil {
			continue
		}
		policy, err := loader.LoadTenant(ctx, entry.Name())
		if err != nil {
			return nil, err
		}
		if err := SeedTenantPolicy(ctx, st, policy); err != nil {
			return nil, err
		}
		seeded = append(seeded, policy.ID)
	}
	sort.Strings(seeded)
	return seeded, nil
}

// KnowledgeBundle reúne os documentos .md estáticos e o arquivo QA.md separado.
type KnowledgeBundle struct {
	Docs        []pack.KnowledgeDoc `json:"docs"`
	QAContent   string              `json:"qa_content"`
	Personality string              `json:"personality,omitempty"`
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

	tenantPersPath := filepath.Join(s.tenantsDir, cleanID, "personality.md")

	h := sha256.New()
	if persInfo, err := os.Stat(tenantPersPath); err == nil {
		_, _ = fmt.Fprintf(h, "personality:%d:%d;", persInfo.Size(), persInfo.ModTime().UnixNano())
	}
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
	var personalityContent string

	if rawPers, err := os.ReadFile(tenantPersPath); err == nil {
		personalityContent = strings.TrimSpace(string(rawPers))
	}

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
		if strings.EqualFold(f.name, "personality.md") {
			if personalityContent == "" {
				personalityContent = content
			}
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
		Personality: personalityContent,
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

// Invalidate remove o tenant do cache para refletir edições imediatamente.
func (s *BlobKnowledgeSource) Invalidate(tenantID string) {
	s.mu.Lock()
	delete(s.cache, tenantID)
	s.mu.Unlock()
}

// SeedLocalKnowledge copia <tenantsDir>/<tenant>/knowledge/*.md para o BlobStore quando ainda não existem.
func SeedLocalKnowledge(ctx context.Context, tenantsDir string, blobs core.BlobStore) ([]string, error) {
	entries, err := os.ReadDir(tenantsDir)
	if err != nil {
		return nil, fmt.Errorf("erro ao listar tenantsDir (%s): %w", tenantsDir, err)
	}
	var seededKeys []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		tenantID := entry.Name()
		kDir := filepath.Join(tenantsDir, tenantID, "knowledge")
		kFiles, err := os.ReadDir(kDir)
		if err != nil {
			continue
		}
		existingList, err := blobs.List(ctx, store.S3KnowledgeKey(tenantID, ""))
		if err != nil {
			return nil, err
		}
		existingSet := make(map[string]bool, len(existingList))
		for _, k := range existingList {
			existingSet[k] = true
		}

		for _, f := range kFiles {
			if f.IsDir() || !strings.HasSuffix(strings.ToLower(f.Name()), ".md") {
				continue
			}
			key := store.S3KnowledgeKey(tenantID, f.Name())
			if existingSet[key] {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(kDir, f.Name()))
			if err != nil {
				return nil, err
			}
			if err := blobs.Put(ctx, key, raw, "text/markdown; charset=utf-8"); err != nil {
				return nil, err
			}
			seededKeys = append(seededKeys, key)
		}
	}
	sort.Strings(seededKeys)
	return seededKeys, nil
}
