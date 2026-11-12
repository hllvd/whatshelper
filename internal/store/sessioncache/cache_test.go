package sessioncache_test

import (
	"context"
	"testing"
	"time"

	"whatshelper/internal/store/sessioncache"
)

func TestSessionCache_Lifecycle(t *testing.T) {
	ctx := context.Background()
	cache, err := sessioncache.Open(":memory:")
	if err != nil {
		t.Fatalf("falha ao abrir cache em memória: %v", err)
	}
	defer cache.Close()

	tenantID := "demo-tenant"
	channel := "whatsapp"
	sourceKey := "+5511999999999"

	// 1. Não encontrada inicialmente
	_, err = cache.GetSession(ctx, tenantID, channel, sourceKey)
	if err != sessioncache.ErrSessionNotFound {
		t.Fatalf("esperava ErrSessionNotFound, obteve %v", err)
	}

	// 2. Salva sessão ativa
	sess := &sessioncache.ActiveSession{
		TenantID:     tenantID,
		Channel:      channel,
		SourceKey:    sourceKey,
		CurrentState: "state_qualifying",
		LastActivity: time.Now().UTC(),
		Metadata: map[string]string{
			"turn": "1",
		},
	}
	if err := cache.SaveSession(ctx, sess); err != nil {
		t.Fatalf("falha ao salvar sessão: %v", err)
	}

	// 3. Lê com sucesso
	loaded, err := cache.GetSession(ctx, tenantID, channel, sourceKey)
	if err != nil {
		t.Fatalf("falha ao carregar sessão: %v", err)
	}
	if loaded.CurrentState != "state_qualifying" {
		t.Fatalf("esperava state_qualifying, obteve %s", loaded.CurrentState)
	}
	if loaded.Metadata["turn"] != "1" {
		t.Fatalf("esperava turn 1, obteve %v", loaded.Metadata)
	}

	// 4. Atualiza estado
	sess.CurrentState = "state_scheduled"
	sess.Metadata["turn"] = "2"
	if err := cache.SaveSession(ctx, sess); err != nil {
		t.Fatalf("falha ao atualizar sessão: %v", err)
	}

	updated, err := cache.GetSession(ctx, tenantID, channel, sourceKey)
	if err != nil {
		t.Fatalf("falha ao carregar sessão atualizada: %v", err)
	}
	if updated.CurrentState != "state_scheduled" {
		t.Fatalf("esperava state_scheduled, obteve %s", updated.CurrentState)
	}

	// 5. Deleta sessão
	if err := cache.DeleteSession(ctx, tenantID, channel, sourceKey); err != nil {
		t.Fatalf("falha ao deletar sessão: %v", err)
	}
	_, err = cache.GetSession(ctx, tenantID, channel, sourceKey)
	if err != sessioncache.ErrSessionNotFound {
		t.Fatalf("esperava ErrSessionNotFound após deleção, obteve %v", err)
	}
}
