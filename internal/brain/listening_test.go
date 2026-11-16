package brain

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"whatshelper/internal/core"
	"whatshelper/internal/core/fakes"
	"whatshelper/internal/store"
)

func TestListening_IsListeningMode(t *testing.T) {
	st := fakes.NewMemoryStore()
	pipe := &Pipeline{
		cfg: PipelineConfig{
			Store: st,
			Clock: core.SystemClock{},
		},
	}
	ctx := context.Background()

	// 1. Não está em listening inicialmente
	if pipe.IsListeningMode(ctx, "demo", "simulator", "s1", "c1") {
		t.Fatal("expected IsListeningMode to be false initially")
	}

	// 2. Ativa listening no contato
	c := core.Contact{
		ID:        "c1",
		TenantID:  "demo",
		Listening: true,
	}
	_ = st.SaveContact(ctx, c)
	if !pipe.IsListeningMode(ctx, "demo", "simulator", "s1", "c1") {
		t.Fatal("expected IsListeningMode to be true when contact.Listening=true")
	}

	// Desativa no contato
	c.Listening = false
	_ = st.SaveContact(ctx, c)

	// 3. Ativa via tenant config whitelist
	cfg := ListeningConfig{
		Global:    false,
		Whitelist: []string{"teste@teste.com", "11999998888"},
	}
	cfgBytes, _ := json.Marshal(cfg)
	_ = st.SaveTenantConfig(ctx, "demo", "listening", cfgBytes)

	if !pipe.IsListeningMode(ctx, "demo", "email", "teste@teste.com", "c2") {
		t.Fatal("expected IsListeningMode to be true for whitelisted email")
	}
	if !pipe.IsListeningMode(ctx, "demo", "whatsapp", "5511999998888", "c3") {
		t.Fatal("expected IsListeningMode to be true for whitelisted phone")
	}
	if pipe.IsListeningMode(ctx, "demo", "email", "outro@teste.com", "c4") {
		t.Fatal("expected IsListeningMode to be false for non-whitelisted email")
	}

	// 4. Ativa via global config
	cfg.Global = true
	cfgBytes, _ = json.Marshal(cfg)
	_ = st.SaveTenantConfig(ctx, "demo", "listening", cfgBytes)

	if !pipe.IsListeningMode(ctx, "demo", "email", "outro@teste.com", "c4") {
		t.Fatal("expected IsListeningMode to be true when Global=true")
	}
}

func TestListening_ApproveProposalsIntoNestedFiles(t *testing.T) {
	st := fakes.NewMemoryStore()
	blobs := fakes.NewMemoryBlobStore()
	clock := fakes.NewFakeClock(time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC))

	pipe := &Pipeline{
		cfg: PipelineConfig{
			Store: st,
			Blobs: blobs,
			Clock: clock,
		},
	}
	ctx := context.Background()

	// 1. Testa aprovação de proposta de QA para learning/QA-listed.md
	propQA := LearningProposal{
		ID:         "prop-qa-1",
		TenantID:   "demo",
		Question:   "Como consultar extrato?",
		Answer:     "Acesse o menu Extrato no portal do cliente.",
		TargetFile: "learning/QA-listed.md",
		Kind:       "qa",
		Status:     "pending",
		CreatedAt:  clock.Now(),
	}
	propQABlob := store.S3LearningKey("demo", "prop-qa-1")
	qaBytes, _ := json.Marshal(propQA)
	_ = blobs.Put(ctx, propQABlob, qaBytes, "application/json")
	dueAt := clock.Now()
	_ = st.SaveTask(ctx, core.Task{
		ID:           "prop-qa-1",
		TenantID:     "demo",
		Channel:      "simulator",
		SourceKey:    "s1",
		Title:        "Test QA",
		Kind:         "learning",
		Status:       core.TaskStatusPending,
		DueAt:        &dueAt,
		DraftBlobRef: propQABlob,
	})

	approvedQA, err := pipe.ApproveLearningProposal(ctx, ApproveLearningRequest{
		TenantID:   "demo",
		ProposalID: "prop-qa-1",
	})
	if err != nil {
		t.Fatalf("ApproveLearningProposal QA failed: %v", err)
	}
	if approvedQA.Status != "approved" {
		t.Fatalf("expected status approved, got %s", approvedQA.Status)
	}

	// Verifica se learning/QA-listed.md foi gravado no BlobStore
	rawQA, err := blobs.Get(ctx, store.S3KnowledgeKey("demo", "learning/QA-listed.md"))
	if err != nil {
		t.Fatalf("learning/QA-listed.md not found in BlobStore: %v", err)
	}
	if len(rawQA) == 0 {
		t.Fatal("learning/QA-listed.md is empty")
	}

	// 2. Testa aprovação de proposta de personalidade para personas/character.md
	propChar := LearningProposal{
		ID:         "prop-char-1",
		TenantID:   "demo",
		Question:   "Tom empático e saudação acolhedora",
		Answer:     "Olá, tudo bem? Que ótimo falar com você!",
		TargetFile: "personas/character.md",
		Kind:       "character",
		Status:     "pending",
		CreatedAt:  clock.Now(),
	}
	propCharBlob := store.S3LearningKey("demo", "prop-char-1")
	charBytes, _ := json.Marshal(propChar)
	_ = blobs.Put(ctx, propCharBlob, charBytes, "application/json")
	_ = st.SaveTask(ctx, core.Task{
		ID:           "prop-char-1",
		TenantID:     "demo",
		Channel:      "simulator",
		SourceKey:    "s1",
		Title:        "Test Char",
		Kind:         "learning",
		Status:       core.TaskStatusPending,
		DueAt:        &dueAt,
		DraftBlobRef: propCharBlob,
	})

	approvedChar, err := pipe.ApproveLearningProposal(ctx, ApproveLearningRequest{
		TenantID:   "demo",
		ProposalID: "prop-char-1",
	})
	if err != nil {
		t.Fatalf("ApproveLearningProposal character failed: %v", err)
	}
	if approvedChar.Status != "approved" {
		t.Fatalf("expected status approved, got %s", approvedChar.Status)
	}

	// Verifica se personas/character.md foi gravado no BlobStore
	rawChar, err := blobs.Get(ctx, store.S3KnowledgeKey("demo", "personas/character.md"))
	if err != nil {
		t.Fatalf("personas/character.md not found in BlobStore: %v", err)
	}
	if len(rawChar) == 0 {
		t.Fatal("personas/character.md is empty")
	}
}
