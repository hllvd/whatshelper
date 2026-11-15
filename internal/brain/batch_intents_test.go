package brain_test

import (
	"testing"

	"whatshelper/internal/brain"
	"whatshelper/internal/pack"
)

func TestAggregateBatchIntents_Table(t *testing.T) {
	defs := []pack.IntentDef{
		{Name: "greeting", Action: pack.IntentActionReply},
		{Name: "buy_item", Action: pack.IntentActionReply},
		{Name: "sell_item", Action: pack.IntentActionCreateTask},
		{Name: "rent_item", Action: pack.IntentActionReply},
		{Name: "request_human", Action: pack.IntentActionHandoff},
	}

	items := []brain.BatchIntentItem{
		{MessageIndex: 0, Intent: "buy_item", Summary: "Quer comprar"},
		{MessageIndex: 1, Intent: "sell_item", Summary: "Quer vender o atual"},
		{MessageIndex: 2, Intent: "greeting", Summary: "Bom dia"},
		{MessageIndex: 3, Intent: "rent_item", Summary: "Quer alugar outro em março"},
	}

	agg := brain.AggregateBatchIntents("buy_item", items, defs)
	if agg.PrimaryIntent != "buy_item" {
		t.Fatalf("expected primary buy_item, got %s", agg.PrimaryIntent)
	}
	if len(agg.UniqueIntents) != 4 {
		t.Fatalf("expected 4 unique intents, got %v", agg.UniqueIntents)
	}
	if agg.DominantAction != pack.IntentActionCreateTask {
		t.Fatalf("expected dominant action create_task, got %s", agg.DominantAction)
	}
	if agg.MaxQuestionsAllowed != 1 {
		t.Fatalf("expected MaxQuestionsAllowed=1, got %d", agg.MaxQuestionsAllowed)
	}
}
