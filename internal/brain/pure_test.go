package brain_test

import (
	"testing"
	"time"

	"whatshelper/internal/brain"
)

func TestMergeAndDiffFacts(t *testing.T) {
	existing := map[string]string{
		"Budget": "3000",
		"Notes":  "urgente",
		"Plan":   "basic",
	}
	incoming := map[string]string{
		"Budget": "4000",     // updated
		"Plan":   "basic",    // unchanged -> no diff
		"City":   "Curitiba", // added
	}
	cleared := []string{"Notes", "NonExistent"} // Notes removed

	merged, diffs := brain.MergeAndDiffFacts(existing, incoming, cleared)

	// Imutabilidade: existing não deve ter sido modificado
	if existing["Budget"] != "3000" || existing["Notes"] != "urgente" {
		t.Fatalf("existing map was mutated: %v", existing)
	}

	if len(merged) != 3 || merged["Budget"] != "4000" || merged["Plan"] != "basic" || merged["City"] != "Curitiba" {
		t.Fatalf("unexpected merged facts: %v", merged)
	}
	if _, exists := merged["Notes"]; exists {
		t.Fatalf("expected Notes to be removed")
	}

	if len(diffs) != 3 {
		t.Fatalf("expected 3 diffs (Budget updated, City added, Notes removed), got %d: %+v", len(diffs), diffs)
	}
	if diffs[0].Key != "Budget" || diffs[0].Action != "updated" || diffs[0].OldValue != "3000" || diffs[0].NewValue != "4000" {
		t.Fatalf("unexpected diff[0]: %+v", diffs[0])
	}
	if diffs[1].Key != "City" || diffs[1].Action != "added" || diffs[1].NewValue != "Curitiba" {
		t.Fatalf("unexpected diff[1]: %+v", diffs[1])
	}
	if diffs[2].Key != "Notes" || diffs[2].Action != "removed" || diffs[2].OldValue != "urgente" {
		t.Fatalf("unexpected diff[2]: %+v", diffs[2])
	}
}

func TestBuildConversationContext(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	sections := map[string]map[string]string{
		"last_action": {
			"type":        "question",
			"actor":       "customer",
			"description": "Qual o prazo?",
		},
	}

	ctx := brain.BuildConversationContext(sections, now)
	if ctx.LastActionType != "question" || ctx.LastActionBy != "customer" || ctx.Description != "Qual o prazo?" {
		t.Fatalf("unexpected projected last_action: %+v", ctx)
	}
	if ctx.Sections["last_action"]["type"] != "question" {
		t.Fatalf("expected sections preserved")
	}
}

func TestSanitizePlainText(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "plain text untouched",
			in:   "Olá! Tudo bem? Posso ajudar.",
			want: "Olá! Tudo bem? Posso ajudar.",
		},
		{
			name: "strips markdown headings, bold, bullets, links and code",
			in:   "## Opções disponíveis\n\n- **Plano A**: ideal para você\n- *Plano B*: veja [aqui](https://exemplo.com)\n1. `Código 123`",
			want: "Opções disponíveis\n\nPlano A: ideal para você\nPlano B: veja aqui (https://exemplo.com)\nCódigo 123",
		},
		{
			name: "strips code fences",
			in:   "```text\nOlá cliente!\n```",
			want: "Olá cliente!",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := brain.SanitizePlainText(tc.in)
			if got != tc.want {
				t.Fatalf("want:\n%q\ngot:\n%q", tc.want, got)
			}
		})
	}
}
