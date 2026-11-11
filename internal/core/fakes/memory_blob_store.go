package fakes

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"whatshelper/internal/core"
)

var ErrBlobNotFound = errors.New("blob not found")

type blobEntry struct {
	versionID   string
	data        []byte
	contentType string
	modifiedAt  time.Time
}

// MemoryBlobStore implementa core.BlobStore e core.VersionedBlobStore em memória para testes rápidos e isolados.
type MemoryBlobStore struct {
	mu       sync.RWMutex
	blobs    map[string]blobEntry
	versions map[string][]blobEntry
}

func NewMemoryBlobStore() *MemoryBlobStore {
	return &MemoryBlobStore{
		blobs:    make(map[string]blobEntry),
		versions: make(map[string][]blobEntry),
	}
}

func (m *MemoryBlobStore) Put(ctx context.Context, key string, data []byte, contentType string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	copied := make([]byte, len(data))
	copy(copied, data)
	verNum := len(m.versions[key]) + 1
	entry := blobEntry{
		versionID:   fmt.Sprintf("v%d", verNum),
		data:        copied,
		contentType: contentType,
		modifiedAt:  time.Now().UTC(),
	}
	m.blobs[key] = entry
	m.versions[key] = append(m.versions[key], entry)
	return nil
}

func (m *MemoryBlobStore) ListVersions(ctx context.Context, key string) ([]core.BlobVersion, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	hist := m.versions[key]
	out := make([]core.BlobVersion, 0, len(hist))
	for i := len(hist) - 1; i >= 0; i-- {
		e := hist[i]
		out = append(out, core.BlobVersion{
			VersionID:    e.versionID,
			IsLatest:     i == len(hist)-1,
			LastModified: e.modifiedAt,
			Size:         int64(len(e.data)),
		})
	}
	return out, nil
}

func (m *MemoryBlobStore) GetVersion(ctx context.Context, key, versionID string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, e := range m.versions[key] {
		if e.versionID == versionID {
			copied := make([]byte, len(e.data))
			copy(copied, e.data)
			return copied, nil
		}
	}
	return nil, ErrBlobNotFound
}

func (m *MemoryBlobStore) Get(ctx context.Context, key string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	entry, ok := m.blobs[key]
	if !ok {
		return nil, ErrBlobNotFound
	}
	copied := make([]byte, len(entry.data))
	copy(copied, entry.data)
	return copied, nil
}

func (m *MemoryBlobStore) Delete(ctx context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.blobs, key)
	return nil
}

func (m *MemoryBlobStore) List(ctx context.Context, prefix string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []string
	for k := range m.blobs {
		if strings.HasPrefix(k, prefix) {
			result = append(result, k)
		}
	}
	return result, nil
}
