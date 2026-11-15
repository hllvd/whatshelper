package brain_test

import (
	"testing"

	"whatshelper/internal/brain"
	"whatshelper/internal/pack"
)

func TestMergeAndDiffCollections_TableAndIdempotency(t *testing.T) {
	defs := []pack.CollectionDef{
		{
			Key:        "objectives",
			IDField:    "id",
			NaturalKey: []string{"type", "target"},
			Fields: []pack.FactDef{
				{Key: "type", Type: pack.FactTypeEnum, Values: []string{"buy", "sell", "rent"}},
				{Key: "target", Type: pack.FactTypeString},
				{Key: "deadline", Type: pack.FactTypeString},
			},
		},
	}

	// 1. Inserção inicial de 2 objetivos (comprar e vender)
	patch1 := map[string][]map[string]any{
		"objectives": {
			{"type": "buy", "target": "unit_a"},
			{"type": "sell", "target": "current_unit"},
		},
	}
	merged1, diff1 := brain.MergeAndDiffCollections(nil, patch1, defs)
	if len(merged1["objectives"]) != 2 {
		t.Fatalf("expected 2 items in objectives, got %d", len(merged1["objectives"]))
	}
	if len(diff1.Added) != 2 {
		t.Fatalf("expected 2 added diffs, got %d", len(diff1.Added))
	}

	// 2. Idempotência: reaplicar o mesmo patch não altera nada nem gera diff
	mergedIdemp, diffIdemp := brain.MergeAndDiffCollections(merged1, patch1, defs)
	if len(mergedIdemp["objectives"]) != 2 || len(diffIdemp.Added) != 0 || len(diffIdemp.Updated) != 0 {
		t.Fatalf("expected idempotent merge with 0 diffs, got %+v", diffIdemp)
	}

	// 3. Upsert por chave natural (atualiza "buy/unit_a" com deadline) + adiciona 3º objetivo ("rent" em março)
	patch2 := map[string][]map[string]any{
		"objectives": {
			{"type": "buy", "target": "unit_a", "deadline": "imediato"},
			{"type": "rent", "target": "unit_b", "deadline": "março"},
		},
	}
	merged2, diff2 := brain.MergeAndDiffCollections(merged1, patch2, defs)
	if len(merged2["objectives"]) != 3 {
		t.Fatalf("expected 3 accumulated objectives, got %d", len(merged2["objectives"]))
	}
	if len(diff2.Updated) != 1 || len(diff2.Added) != 1 {
		t.Fatalf("expected 1 updated and 1 added, got %+v", diff2)
	}

	// 4. Remoção explícita (_delete: true)
	patch3 := map[string][]map[string]any{
		"objectives": {
			{"type": "sell", "target": "current_unit", "_delete": true},
		},
	}
	merged3, diff3 := brain.MergeAndDiffCollections(merged2, patch3, defs)
	if len(merged3["objectives"]) != 2 || len(diff3.Removed) != 1 {
		t.Fatalf("expected 2 remaining objectives and 1 removed, got len=%d diff=%+v", len(merged3["objectives"]), diff3)
	}
}
