package pack_test

import (
	"encoding/json"
	"strings"
	"testing"

	"whatshelper/internal/pack"
)

const samplePackYAML = `
name: sample
language: pt-BR
actors:
  business: agent
  customer: customer
facts:
  - key: Budget
    type: number
    description: Orçamento máximo
  - key: ItemsCount
    type: integer
    description: Quantidade desejada
  - key: Urgent
    type: boolean
    description: Possui urgência
  - key: Plan
    type: enum
    values: [basic, pro, enterprise]
    description: Plano de interesse
  - key: Notes
    type: string
    description: Observações livres
signals:
  - key: has_new_question
    description: Nova pergunta clara na última mensagem
  - key: customer_engaged
    description: Cliente está respondendo ativamente
context:
  - key: last_action
    fields:
      - key: type
        type: enum
        values: [question, followup, other]
      - key: actor
        type: enum
        values: [agent, customer]
      - key: description
        type: string
prompts:
  analysis: prompts/analysis.tmpl
  reply: prompts/reply.tmpl
`

func TestParsePackYAML(t *testing.T) {
	p, err := pack.ParsePackYAML([]byte(samplePackYAML))
	if err != nil {
		t.Fatalf("unexpected error parsing valid pack: %v", err)
	}
	if p.Name != "sample" || p.Actors.Business != "agent" {
		t.Fatalf("unexpected pack metadata: %+v", p)
	}
	if len(p.Facts) != 5 || len(p.Signals) != 2 || len(p.Context) != 1 {
		t.Fatalf("unexpected counts: facts=%d signals=%d context=%d", len(p.Facts), len(p.Signals), len(p.Context))
	}

	// Casos inválidos
	invalidCases := []struct {
		name string
		yaml string
	}{
		{
			name: "missing name",
			yaml: "language: pt-BR\nactors: {business: a, customer: b}\nprompts: {analysis: a, reply: b}",
		},
		{
			name: "same actors",
			yaml: "name: x\nlanguage: pt-BR\nactors: {business: a, customer: a}\nprompts: {analysis: a, reply: b}",
		},
		{
			name: "duplicate fact",
			yaml: "name: x\nlanguage: pt-BR\nactors: {business: a, customer: b}\nfacts:\n  - {key: k1, type: string}\n  - {key: k1, type: number}\nprompts: {analysis: a, reply: b}",
		},
		{
			name: "enum without values",
			yaml: "name: x\nlanguage: pt-BR\nactors: {business: a, customer: b}\nfacts:\n  - {key: k1, type: enum}\nprompts: {analysis: a, reply: b}",
		},
		{
			name: "invalid fact type",
			yaml: "name: x\nlanguage: pt-BR\nactors: {business: a, customer: b}\nfacts:\n  - {key: k1, type: array}\nprompts: {analysis: a, reply: b}",
		},
	}

	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := pack.ParsePackYAML([]byte(tc.yaml)); err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
		})
	}
}

func TestBuildAnalysisSchema(t *testing.T) {
	p, err := pack.ParsePackYAML([]byte(samplePackYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rawSchema, err := pack.BuildAnalysisSchema(p)
	if err != nil {
		t.Fatalf("BuildAnalysisSchema failed: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(rawSchema, &parsed); err != nil {
		t.Fatalf("generated schema is not valid JSON: %v", err)
	}

	props := parsed["properties"].(map[string]any)
	factsObj := props["facts"].(map[string]any)
	factProps := factsObj["properties"].(map[string]any)

	if _, ok := factProps["Budget"]; !ok {
		t.Fatalf("expected Budget in schema facts properties")
	}
	if _, ok := factProps["Plan"]; !ok {
		t.Fatalf("expected Plan in schema facts properties")
	}
	if !strings.Contains(string(rawSchema), `"has_new_question"`) {
		t.Fatalf("expected signal has_new_question in schema")
	}
}

func TestValidateAnalysis(t *testing.T) {
	p, err := pack.ParsePackYAML([]byte(samplePackYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tests := []struct {
		name          string
		input         string
		wantErrFields []string
		check         func(t *testing.T, res *pack.AnalysisResult)
	}{
		{
			name: "valid complete output with normalization and missing signal default false",
			input: `{
				"summary": "Cliente quer plano pro com orçamento de 1500.",
				"last_sender": "customer",
				"facts": {
					"Budget": 1500,
					"ItemsCount": "3",
					"Urgent": true,
					"Plan": "PRO",
					"Notes": null
				},
				"signals": {
					"has_new_question": true
				},
				"context": {
					"last_action": {
						"type": "question",
						"actor": "customer",
						"description": "Qual o prazo?"
					}
				}
			}`,
			check: func(t *testing.T, res *pack.AnalysisResult) {
				if res.Facts["Budget"] != "1500" {
					t.Fatalf("expected Budget 1500, got %q", res.Facts["Budget"])
				}
				if res.Facts["ItemsCount"] != "3" {
					t.Fatalf("expected ItemsCount 3, got %q", res.Facts["ItemsCount"])
				}
				if res.Facts["Urgent"] != "true" {
					t.Fatalf("expected Urgent true, got %q", res.Facts["Urgent"])
				}
				if res.Facts["Plan"] != "pro" {
					t.Fatalf("expected normalized enum pro, got %q", res.Facts["Plan"])
				}
				if len(res.ClearedFacts) != 1 || res.ClearedFacts[0] != "Notes" {
					t.Fatalf("expected ClearedFacts [Notes], got %v", res.ClearedFacts)
				}
				if !res.Signals["has_new_question"] {
					t.Fatalf("expected has_new_question true")
				}
				if res.Signals["customer_engaged"] {
					t.Fatalf("expected missing signal customer_engaged to default false")
				}
				if res.Context["last_action"]["type"] != "question" {
					t.Fatalf("unexpected context last_action: %v", res.Context)
				}
			},
		},
		{
			name:          "rejects text in number field (500 mil)",
			input:         `{"summary": "ok", "last_sender": "customer", "facts": {"Budget": "500 mil"}, "signals": {}, "context": {}}`,
			wantErrFields: []string{"facts.Budget"},
		},
		{
			name:          "rejects float in integer field",
			input:         `{"summary": "ok", "last_sender": "customer", "facts": {"ItemsCount": 3.5}, "signals": {}, "context": {}}`,
			wantErrFields: []string{"facts.ItemsCount"},
		},
		{
			name:          "rejects unknown fact key",
			input:         `{"summary": "ok", "last_sender": "customer", "facts": {"UnknownKey": "abc"}, "signals": {}, "context": {}}`,
			wantErrFields: []string{"facts.UnknownKey"},
		},
		{
			name:          "rejects invalid enum and wrong last_sender",
			input:         `{"summary": "ok", "last_sender": "unknown", "facts": {"Plan": "gold"}, "signals": {}, "context": {}}`,
			wantErrFields: []string{"facts.Plan", "last_sender"},
		},
		{
			name:          "rejects non-boolean signal and unknown context section",
			input:         `{"summary": "ok", "last_sender": "agent", "facts": {}, "signals": {"has_new_question": "yes"}, "context": {"other": {}}}`,
			wantErrFields: []string{"context.other", "signals.has_new_question"},
		},
		{
			name:          "rejects missing summary",
			input:         `{"last_sender": "agent", "facts": {}, "signals": {}, "context": {}}`,
			wantErrFields: []string{"summary"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, errs := pack.ValidateAnalysis(p, []byte(tc.input))
			if len(tc.wantErrFields) > 0 {
				if len(errs) == 0 {
					t.Fatalf("expected validation errors %v, got none", tc.wantErrFields)
				}
				gotFields := make([]string, len(errs))
				for i, e := range errs {
					gotFields[i] = e.Field
				}
				for _, expected := range tc.wantErrFields {
					found := false
					for _, g := range gotFields {
						if g == expected {
							found = true
							break
						}
					}
					if !found {
						t.Fatalf("expected error on field %q, got fields %v (errs: %v)", expected, gotFields, errs)
					}
				}
				return
			}
			if len(errs) > 0 {
				t.Fatalf("unexpected validation errors: %v", errs)
			}
			if tc.check != nil {
				tc.check(t, res)
			}
		})
	}
}
