package fakes

import (
	"context"
	"errors"
	"strings"
	"sync"
)

var ErrBlobNotFound = errors.New("blob not found")

type blobEntry struct {
	data        []byte
	contentType string
}

// MemoryBlobStore implementa core.BlobStore em memória para testes rápidos e isolados.
type MemoryBlobStore struct {
	mu    sync.RWMutex
	blobs map[string]blobEntry
}

func NewMemoryBlobStore() *MemoryBlobStore {
	return &MemoryBlobStore{
		blobs: make(map[string]blobEntry),
	}
}

func (m *MemoryBlobStore) Put(ctx context.Context, key string, data []byte, contentType string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	copied := make([]byte, len(data))
	copy(copied, data)
	m.blobs[key] = blobEntry{
		data:        copied,
		contentType: contentType,
	}
	return nil
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
