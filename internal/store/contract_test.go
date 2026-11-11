package store_test

import (
	"testing"

	"whatshelper/internal/core"
	"whatshelper/internal/core/fakes"
	"whatshelper/internal/store/storetest"
)

func TestMemoryStoreContract(t *testing.T) {
	storetest.RunStoreContractTests(t, func(t *testing.T, clock core.Clock) core.Store {
		return fakes.NewMemoryStoreWithClock(clock)
	})
}

func TestMemoryBlobStoreContract(t *testing.T) {
	storetest.RunBlobStoreContractTests(t, func(t *testing.T) core.BlobStore {
		return fakes.NewMemoryBlobStore()
	})
}
