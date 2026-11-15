package pack_test

import (
	"os"
	"path/filepath"
	"testing"

	"whatshelper/internal/pack"
)

func TestLoadJourneysFromDir_AndResolveJourney(t *testing.T) {
	tmp := t.TempDir()
	pDir := filepath.Join(tmp, "generic")
	if err := os.MkdirAll(filepath.Join(pDir, "prompts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(pDir, "journeys"), 0o755); err != nil {
		t.Fatal(err)
	}

	_ = os.WriteFile(filepath.Join(pDir, "pack.yaml"), []byte(`name: generic
language: pt-BR
actors:
  customer: Cliente
  business: Atendente
intents:
  - name: ask_info
    action: reply
    description: Dúvida geral
  - name: schedule_call
    action: reply
    description: Agendar conversa
prompts:
  analysis: prompts/analysis.tmpl
  reply: prompts/reply.tmpl
`), 0o644)
	_ = os.WriteFile(filepath.Join(pDir, "prompts", "analysis.tmpl"), []byte("sys\n<!-- USER_PROMPT -->\nuser"), 0o644)
	_ = os.WriteFile(filepath.Join(pDir, "prompts", "reply.tmpl"), []byte("sys\n<!-- USER_PROMPT -->\nuser"), 0o644)

	_ = os.WriteFile(filepath.Join(pDir, "journeys", "support.yaml"), []byte(`version: "1"
initial_state: triage
states:
  triage:
    description: Triagem
  done:
    terminal: true
`), 0o644)

	_ = os.WriteFile(filepath.Join(pDir, "journeys", "leads.yaml"), []byte(`version: "1"
initial_state: qualify
states:
  qualify:
    description: Qualificação
  booked:
    terminal: true
`), 0o644)

	pk, err := pack.LoadFromDir(pDir)
	if err != nil {
		t.Fatalf("LoadFromDir failed: %v", err)
	}
	if len(pk.Journeys) != 2 {
		t.Fatalf("expected 2 journeys, got %d", len(pk.Journeys))
	}
	if j := pk.ResolveJourney("leads"); j == nil || j.InitialState != "qualify" {
		t.Fatalf("expected leads journey with initial_state=qualify, got %+v", j)
	}
	if j := pk.ResolveJourney(""); j == nil || j.InitialState != "triage" {
		t.Fatalf("expected default fallback to support (triage), got %+v", j)
	}
	if j := pk.ResolveJourney("unknown"); j == nil || j.InitialState != "triage" {
		t.Fatalf("expected unknown workflow to fallback to support (triage), got %+v", j)
	}
}

func TestLoadAllRepositoryPacks(t *testing.T) {
	for _, name := range []string{"realestate", "support", "supporter"} {
		pDir := filepath.Join("..", "..", "packs", name)
		pk, err := pack.LoadFromDir(pDir)
		if err != nil {
			t.Fatalf("LoadFromDir(%s) failed: %v", name, err)
		}
		if pk.Name != name {
			t.Fatalf("expected pack name %q, got %q", name, pk.Name)
		}
	}
}
