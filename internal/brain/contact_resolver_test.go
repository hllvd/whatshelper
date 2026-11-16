package brain

import (
	"context"
	"testing"
	"time"

	"whatshelper/internal/core"
	"whatshelper/internal/core/fakes"
)

func TestContactResolver_SimulatorTestMode(t *testing.T) {
	store := fakes.NewMemoryStore()
	pipe := &Pipeline{
		cfg: PipelineConfig{
			Store: store,
			Clock: core.SystemClock{},
		},
	}
	ctx := context.Background()

	msg := core.NormalizedMessage{
		TenantID:    "demo",
		Channel:     "simulator",
		SourceKey:   "sim-test-99",
		DisplayName: "Tester",
		Text:        "Olá teste",
		Timestamp:   time.Now(),
	}

	contact, err := pipe.resolveMessageContact(ctx, msg)
	if err != nil {
		t.Fatalf("resolveMessageContact failed: %v", err)
	}
	if contact.ID != "sim-test-99" {
		t.Fatalf("expected contact ID sim-test-99, got %s", contact.ID)
	}

	// Verifica se a fonte foi vinculada ao contato
	sources, err := store.ListContactSources(ctx, "demo", "sim-test-99")
	if err != nil || len(sources) != 1 {
		t.Fatalf("expected 1 linked source, got %d (err=%v)", len(sources), err)
	}
}

func TestContactResolver_WhatsAppAndEmailNormalization(t *testing.T) {
	store := fakes.NewMemoryStore()
	pipe := &Pipeline{
		cfg: PipelineConfig{
			Store: store,
			Clock: core.SystemClock{},
		},
	}
	ctx := context.Background()

	// Mensagem do WhatsApp com número brasileiro com DDI 55
	whatsMsg := core.NormalizedMessage{
		TenantID:   "demo",
		Channel:    "whatsapp",
		SourceKey:  "5511988887777",
		RawContact: "+55 (11) 98888-7777",
		Text:       "Meu email é contato@empresa.com",
		Timestamp:  time.Now(),
	}

	contact, err := pipe.resolveMessageContact(ctx, whatsMsg)
	if err != nil {
		t.Fatalf("resolveMessageContact WhatsApp failed: %v", err)
	}
	if contact.Phone != "11988887777" {
		t.Fatalf("expected normalized phone 11988887777, got %s", contact.Phone)
	}
	if contact.Email != "contato@empresa.com" {
		t.Fatalf("expected extracted email contato@empresa.com, got %s", contact.Email)
	}

	// Mensagem posterior vinda do canal de email com o mesmo email
	emailMsg := core.NormalizedMessage{
		TenantID:   "demo",
		Channel:    "email",
		SourceKey:  "contato@empresa.com",
		RawContact: "Contato <CONTATO@EMPRESA.COM>",
		Text:       "Oi pelo email",
		Timestamp:  time.Now(),
	}

	linkedContact, err := pipe.resolveMessageContact(ctx, emailMsg)
	if err != nil {
		t.Fatalf("resolveMessageContact email failed: %v", err)
	}
	if linkedContact.ID != contact.ID {
		t.Fatalf("expected to unify to existing contact ID %s, got %s", contact.ID, linkedContact.ID)
	}

	// Verifica que ambas as fontes agora estão vinculadas
	sources, err := store.ListContactSources(ctx, "demo", contact.ID)
	if err != nil || len(sources) != 2 {
		t.Fatalf("expected 2 linked sources for contact, got %d (err=%v)", len(sources), err)
	}
}
