package contextmod_test

import (
	"testing"

	"whatshelper/internal/contextmod/contracttest"
)

func TestInProcessService_ContractSuite(t *testing.T) {
	contracttest.RunSuite(t, contracttest.NewInProcessHarness)
}
