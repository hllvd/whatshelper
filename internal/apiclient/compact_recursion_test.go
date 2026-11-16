package apiclient_test

import (
	"strings"
	"testing"

	"whatshelper/internal/apiclient"
)

func TestCompactJSONBody_RealSalesAppContractResponse(t *testing.T) {
	rawContract := `{
		"success": true,
		"data": {
			"id": 96664,
			"contractNumber": "1101053001",
			"userId": "9d5d2b12-d32b-4532-8dd2-29d5bc3b6b2b",
			"userName": "Emanuelly Barbosa",
			"totalAmount": 100000,
			"groupId": 84,
			"groupName": "12170",
			"status": "Active",
			"contractStartDate": "2026-11-13T00:00:00",
			"isActive": true,
			"createdAt": "2026-11-13T15:27:56.3817907",
			"updatedAt": "2026-11-15T15:30:47.8785556",
			"segment": 1,
			"segmentName": "Lar",
			"contractType": "Lar",
			"quota": 788,
			"pvId": 1276,
			"customerName": "Éder silva Sanches",
			"matriculaId": 2,
			"matriculaNumber": "8338",
			"rawStatus": null,
			"hasPayment": true,
			"isAwaitingPayment": false,
			"isRemappedToAwaitingPayment": false,
			"teamName": "netzer"
		},
		"message": "Contrato recuperado com sucesso",
		"aggregation": null
	}`

	compacted := apiclient.CompactJSONBody(rawContract, "1101053001", 5)
	if !strings.Contains(compacted, "1101053001") {
		t.Fatalf("expected contractNumber 1101053001 in compacted output, got:\n%s", compacted)
	}
	if !strings.Contains(compacted, "Emanuelly Barbosa") {
		t.Fatalf("expected userName Emanuelly Barbosa in compacted output, got:\n%s", compacted)
	}
	if !strings.Contains(compacted, "netzer") {
		t.Fatalf("expected teamName netzer in compacted output, got:\n%s", compacted)
	}
}
