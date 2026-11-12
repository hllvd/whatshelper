package pack_test

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"whatshelper/internal/pack"
)

func TestDIM01_DIM02_DimensionsSchemaAndValidation(t *testing.T) {
	for _, name := range []string{"realestate", "support"} {
		pk, err := pack.LoadFromDir(filepath.Join("..", "..", "packs", name))
		if err != nil {
			t.Fatalf("LoadFromDir(%s): %v", name, err)
		}
		if len(pk.Dimensions) < 2 {
			t.Fatalf("pack %s must declare at least sentiment & urgency dimensions", name)
		}
		schemaBytes, err := pack.BuildAnalysisSchema(pk)
		if err != nil {
			t.Fatalf("BuildAnalysisSchema(%s): %v", name, err)
		}
		var schema map[string]any
		if err := json.Unmarshal(schemaBytes, &schema); err != nil {
			t.Fatalf("unmarshal schema: %v", err)
		}
		props := schema["properties"].(map[string]any)
		if _, ok := props["dimensions"]; !ok {
			t.Fatalf("pack %s schema missing dimensions field", name)
		}
	}

	pk := &pack.Pack{
		Name:     "dim-test",
		Language: "pt-BR",
		Actors:   pack.ActorsDef{Business: "agent", Customer: "customer"},
		Prompts:  pack.PromptsDef{Analysis: "a.tmpl", Reply: "r.tmpl"},
		Dimensions: []pack.DimensionDef{
			{Key: "sentiment", Values: []string{"positive", "neutral", "negative"}, Default: "unknown"},
			{Key: "urgency", Values: []string{"low", "high"}, Default: "unknown"},
		},
	}

	// Valid dimensions including "unknown"
	res, errs := pack.ValidateAnalysis(pk, []byte(`{
		"summary": "Cliente perguntou preço.",
		"last_sender": "customer",
		"facts": {},
		"signals": {},
		"context": {},
		"dimensions": {"sentiment": "positive", "urgency": "unknown"}
	}`))
	if len(errs) > 0 {
		t.Fatalf("unexpected validation errors: %v", errs)
	}
	if res.Dimensions["sentiment"] != "positive" || res.Dimensions["urgency"] != "unknown" {
		t.Fatalf("unexpected dimensions: %+v", res.Dimensions)
	}

	// Invalid dimension value must fail validation
	_, errs = pack.ValidateAnalysis(pk, []byte(`{
		"summary": "Cliente perguntou preço.",
		"last_sender": "customer",
		"facts": {},
		"signals": {},
		"context": {},
		"dimensions": {"sentiment": "angry_invalid", "urgency": "high"}
	}`))
	if len(errs) == 0 {
		t.Fatalf("expected validation error for invalid dimension value")
	}

	// Omitted dimensions falls back to default "unknown" without breaking legacy mocks
	resFallback, errs := pack.ValidateAnalysis(pk, []byte(`{
		"summary": "Resumo legado.",
		"last_sender": "customer",
		"facts": {},
		"signals": {},
		"context": {}
	}`))
	if len(errs) > 0 {
		t.Fatalf("unexpected validation errors on omitted dimensions: %v", errs)
	}
	if resFallback.Dimensions["sentiment"] != "unknown" || resFallback.Dimensions["urgency"] != "unknown" {
		t.Fatalf("expected unknown defaults, got %+v", resFallback.Dimensions)
	}
}
