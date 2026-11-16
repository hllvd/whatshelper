package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"whatshelper/internal/config"
	"whatshelper/internal/core"
	"whatshelper/internal/core/fakes"
	"whatshelper/internal/server"
)

func TestContactsAPI_SearchGetToggleAndDelete(t *testing.T) {
	memStore := fakes.NewMemoryStore()
	blobs := fakes.NewMemoryBlobStore()
	bus := core.NewInMemoryEventBus(32)

	srv := server.NewWithDeps(config.Config{
		HTTPHost: "127.0.0.1",
		HTTPPort: 8080,
	}, nil, server.Dependencies{
		Store: memStore,
		Blobs: blobs,
		Bus:   bus,
	})
	handler := srv.Handler()
	ctx := context.Background()

	// 1. Cadastra contato e vincula fonte
	now := time.Now().UTC()
	c1 := core.Contact{
		ID:        "c-joao",
		TenantID:  "demo",
		Name:      "João da Silva",
		Email:     "joao@silva.com",
		Phone:     "11988887777",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := memStore.SaveContact(ctx, c1); err != nil {
		t.Fatalf("SaveContact failed: %v", err)
	}
	if err := memStore.LinkSourceToContact(ctx, "demo", "whatsapp", "11988887777", "c-joao"); err != nil {
		t.Fatalf("LinkSourceToContact failed: %v", err)
	}
	_ = memStore.CommitAnalysis(ctx, core.CommitAnalysisInput{
		TenantID:  "demo",
		Channel:   "whatsapp",
		SourceKey: "11988887777",
		Summary: core.Summary{
			TenantID:  "demo",
			Channel:   "whatsapp",
			SourceKey: "11988887777",
			Text:      "Cliente interessado em planos de consórcio.",
		},
		UpdatedAt: now,
	})

	// 2. Busca contatos GET /api/contacts?q=silva
	req := httptest.NewRequest("GET", "/api/contacts?tenant=demo&q=silva", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var searchResp struct {
		Contacts []server.ContactSummaryView `json:"contacts"`
		Total    int                         `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &searchResp); err != nil {
		t.Fatalf("unmarshal search failed: %v", err)
	}
	if searchResp.Total != 1 || searchResp.Contacts[0].ID != "c-joao" {
		t.Fatalf("unexpected search results: %+v", searchResp)
	}
	if searchResp.Contacts[0].LastSummary != "Cliente interessado em planos de consórcio." {
		t.Fatalf("unexpected summary: %s", searchResp.Contacts[0].LastSummary)
	}

	// 3. Obter detalhes do contato GET /api/contacts/c-joao
	req = httptest.NewRequest("GET", "/api/contacts/c-joao?tenant=demo", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var detail server.ContactDetailView
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("unmarshal detail failed: %v", err)
	}
	if len(detail.Sources) != 1 || detail.SourceStates["whatsapp/11988887777"] == nil {
		t.Fatalf("expected 1 source state, got %+v", detail)
	}

	// 4. Toggle Listening no contato POST /api/contacts/c-joao/listening
	toggleBody := []byte(`{"listening": true}`)
	req = httptest.NewRequest("POST", "/api/contacts/c-joao/listening?tenant=demo", bytes.NewReader(toggleBody))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on toggle listening, got %d: %s", rec.Code, rec.Body.String())
	}
	var updatedContact core.Contact
	if err := json.Unmarshal(rec.Body.Bytes(), &updatedContact); err != nil {
		t.Fatalf("unmarshal toggle failed: %v", err)
	}
	if !updatedContact.Listening {
		t.Fatal("expected listening to be true on updated contact")
	}

	// 5. Configuração Global de Listening GET/PUT /api/listening/config
	putConfig := []byte(`{"global": true, "whitelist": ["teste@empresa.com"]}`)
	req = httptest.NewRequest("PUT", "/api/listening/config?tenant=demo", bytes.NewReader(putConfig))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on put listening config, got %d", rec.Code)
	}

	req = httptest.NewRequest("GET", "/api/listening/config?tenant=demo", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on get listening config, got %d", rec.Code)
	}
	var lCfg struct {
		Global    bool     `json:"global"`
		Whitelist []string `json:"whitelist"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &lCfg); err != nil {
		t.Fatalf("unmarshal listening config failed: %v", err)
	}
	if !lCfg.Global || len(lCfg.Whitelist) != 1 {
		t.Fatalf("unexpected listening config: %+v", lCfg)
	}

	// 6. Deletar contato e todo histórico DELETE /api/contacts/c-joao
	req = httptest.NewRequest("DELETE", "/api/contacts/c-joao?tenant=demo", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on delete contact, got %d: %s", rec.Code, rec.Body.String())
	}

	// Confirma que não existe mais
	req = httptest.NewRequest("GET", "/api/contacts/c-joao?tenant=demo", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after deletion, got %d", rec.Code)
	}
}
