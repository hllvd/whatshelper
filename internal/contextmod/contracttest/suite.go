package contracttest

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"whatshelper/internal/contextmod"
	"whatshelper/internal/core"
	"whatshelper/internal/core/fakes"
	"whatshelper/internal/pack"
	"whatshelper/internal/store"
)

// Harness encapsula um serviço de contexto isolado pronto para executar a suíte de contrato.
type Harness struct {
	Service   contextmod.Service
	ScopeA    contextmod.Scope
	ScopeB    contextmod.Scope
	SeedStore *fakes.MemoryStore
	SeedBlobs *fakes.MemoryBlobStore
}

// Factory cria uma instância isolada do serviço (in-process, REST ou MCP) já semeada para testes.
type Factory func(t *testing.T) Harness

// NewInProcessHarness cria um Harness padrão usando o adapter InProcessService sobre MemoryStore/MemoryBlobStore.
func NewInProcessHarness(t *testing.T) Harness {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	clock := fakes.NewFakeClock(now)
	st := fakes.NewMemoryStoreWithClock(clock)
	blobs := fakes.NewMemoryBlobStore()

	pk, err := pack.ParsePackYAML([]byte(`
name: generic_test
language: pt-BR
actors:
  business: agent
  customer: user
facts:
  - key: Plan
    type: string
    description: Plano
  - key: InternalNote
    type: string
    description: Nota interna
signals:
  - key: customer_engaged
    description: Engajado
  - key: internal_alert
    description: Alerta interno
context:
  - key: last_action
    fields:
      - key: type
        type: string
prompts:
  analysis: prompts/analysis.tmpl
  reply: prompts/reply.tmpl
context_module:
  providers: [summary, facts, signals, recent_messages, knowledge]
  exposed_fields:
    facts: [Plan]
    signals: [customer_engaged]
    context_sections: [last_action]
  views:
    external_agent:
      blocks: [summary, facts, signals, structured_context, recent_messages]
      format: delimited
      max_tokens: 2000
`))
	if err != nil {
		t.Fatal(err)
	}

	scopeA := contextmod.Scope{TenantID: "tenant-a", Channel: "whatsapp", SourceKey: "5511999998888"}
	scopeB := contextmod.Scope{TenantID: "tenant-b", Channel: "whatsapp", SourceKey: "5511977776666"}

	// Semear PII cifrada em SourceMeta (nunca pode vazar pelo módulo de contexto)
	_ = st.SaveSourceMeta(ctx, core.SourceMeta{
		TenantID:             scopeA.TenantID,
		Channel:              scopeA.Channel,
		SourceKey:            scopeA.SourceKey,
		Status:               core.SourceStatusActive,
		Version:              0,
		EncryptedContactInfo: "enc:v1:SEGREDO_PII_NAO_EXPOR",
		CreatedAt:            now,
		UpdatedAt:            now,
	})
	_ = st.SaveSourceMeta(ctx, core.SourceMeta{
		TenantID:             scopeB.TenantID,
		Channel:              scopeB.Channel,
		SourceKey:            scopeB.SourceKey,
		Status:               core.SourceStatusActive,
		Version:              0,
		EncryptedContactInfo: "enc:v1:SEGREDO_TENANT_B",
		CreatedAt:            now,
		UpdatedAt:            now,
	})

	// Semear 5 mensagens no scopeA e 1 no scopeB
	for i := 1; i <= 5; i++ {
		ts := now.Add(time.Duration(i) * time.Minute)
		msgID := "msg-" + string(rune('0'+i))
		norm := core.NormalizedMessage{
			TenantID:  scopeA.TenantID,
			Channel:   scopeA.Channel,
			SourceKey: scopeA.SourceKey,
			MessageID: msgID,
			Timestamp: ts,
			Direction: core.DirectionInbound,
			Type:      core.MessageTypeText,
			Text:      "Mensagem de teste número " + string(rune('0'+i)) + " " + strings.Repeat("detalhe ", 8),
		}
		rawKey := store.S3RawMessageKey(scopeA.TenantID, scopeA.Channel, scopeA.SourceKey, ts, msgID)
		norm.RawRef = rawKey
		rawBytes, _ := json.Marshal(norm)
		_ = blobs.Put(ctx, rawKey, rawBytes, "application/json")
		_ = st.PutMessageMeta(ctx, core.MessageMeta{
			TenantID:  scopeA.TenantID,
			Channel:   scopeA.Channel,
			SourceKey: scopeA.SourceKey,
			MessageID: msgID,
			Timestamp: ts,
			Direction: core.DirectionInbound,
			Type:      core.MessageTypeText,
			RawRef:    rawKey,
			Sender:    "user",
		})
	}

	tsB := now.Add(10 * time.Minute)
	rawBKey := store.S3RawMessageKey(scopeB.TenantID, scopeB.Channel, scopeB.SourceKey, tsB, "msg-b1")
	normB, _ := json.Marshal(core.NormalizedMessage{
		TenantID: scopeB.TenantID, Channel: scopeB.Channel, SourceKey: scopeB.SourceKey,
		MessageID: "msg-b1", Timestamp: tsB, Direction: core.DirectionInbound, Type: core.MessageTypeText, Text: "Segredo tenant B",
	})
	_ = blobs.Put(ctx, rawBKey, normB, "application/json")
	_ = st.PutMessageMeta(ctx, core.MessageMeta{
		TenantID: scopeB.TenantID, Channel: scopeB.Channel, SourceKey: scopeB.SourceKey,
		MessageID: "msg-b1", Timestamp: tsB, Direction: core.DirectionInbound, Type: core.MessageTypeText, RawRef: rawBKey, Sender: "user",
	})

	_ = blobs.Put(ctx, store.S3KnowledgeKey(scopeA.TenantID, "FAQ.md"), []byte("Horário de suporte: 9h às 18h.\n"), "text/markdown")
	_ = blobs.Put(ctx, store.S3KnowledgeKey(scopeA.TenantID, "QA.md"), []byte("P: Aceita cartão?\nR: Sim, aceitamos.\n"), "text/markdown")
	_ = blobs.Put(ctx, store.S3KnowledgeKey(scopeB.TenantID, "SEGREDO_B.md"), []byte("Documento exclusivo do tenant B.\n"), "text/markdown")

	svc := contextmod.NewInProcessService(contextmod.InProcessConfig{
		Store:     st,
		Blobs:     blobs,
		Knowledge: staticKnowledgeReader{blobs: blobs},
		Packs:     staticPackResolver{pk: pk},
		Clock:     clock,
	})

	// Semear fatos + resumo iniciais no scopeA (versão 0 -> 1)
	_, err = svc.ApplyFacts(ctx, contextmod.ApplyFactsRequest{
		Scope:           scopeA,
		ExpectedVersion: 0,
		Summary:         strPtr("Resumo inicial do atendimento"),
		Facts:           map[string]string{"Plan": "pro", "InternalNote": "vip-interno"},
		Signals:         map[string]bool{"customer_engaged": true, "internal_alert": true},
		StructuredContext: &contextmod.StructuredContextView{
			Sections: map[string]map[string]string{
				"last_action": {"type": "question"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	return Harness{
		Service:   svc,
		ScopeA:    scopeA,
		ScopeB:    scopeB,
		SeedStore: st,
		SeedBlobs: blobs,
	}
}

// RunSuite executa todos os testes de contrato CTX-13 contra qualquer implementação de contextmod.Service.
func RunSuite(t *testing.T, newHarness Factory) {
	t.Run("ViewsAllowlistAndNoPIILeak", func(t *testing.T) {
		h := newHarness(t)
		ctx := context.Background()

		bundle, err := h.Service.GetContext(ctx, contextmod.GetContextRequest{
			Scope: h.ScopeA,
			View:  contextmod.ViewExternalAgent,
		})
		if err != nil {
			t.Fatalf("GetContext external_agent failed: %v", err)
		}
		if bundle.Facts["Plan"] != "pro" {
			t.Fatalf("expected allowed fact Plan=pro, got %+v", bundle.Facts)
		}
		if _, leaked := bundle.Facts["InternalNote"]; leaked {
			t.Fatalf("expected InternalNote filtered out by allowlist")
		}
		if _, leaked := bundle.Signals["internal_alert"]; leaked {
			t.Fatalf("expected internal_alert filtered out by allowlist")
		}
		if len(bundle.RecentMessages) == 0 || !strings.Contains(bundle.RecentMessages[0].Text, "<<<UNTRUSTED_MESSAGE>>>") {
			t.Fatalf("expected untrusted message delimiter, got %+v", bundle.RecentMessages)
		}
		rawJSON, _ := json.Marshal(bundle)
		if strings.Contains(string(rawJSON), "SEGREDO_PII_NAO_EXPOR") || strings.Contains(string(rawJSON), "encrypted_contact_info") {
			t.Fatalf("PII leaked in ContextBundle JSON: %s", string(rawJSON))
		}

		sumOnly, err := h.Service.GetContext(ctx, contextmod.GetContextRequest{
			Scope: h.ScopeA,
			View:  contextmod.ViewSummaryOnly,
		})
		if err != nil {
			t.Fatal(err)
		}
		if sumOnly.Summary == nil || len(sumOnly.RecentMessages) != 0 || len(sumOnly.Facts) != 0 {
			t.Fatalf("expected summary_only to omit messages and facts, got %+v", sumOnly)
		}
	})

	t.Run("BudgetTruncationDeterministic", func(t *testing.T) {
		h := newHarness(t)
		ctx := context.Background()

		bundle, err := h.Service.GetContext(ctx, contextmod.GetContextRequest{
			Scope:        h.ScopeA,
			View:         contextmod.ViewAnalysis,
			BudgetTokens: 45,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !bundle.Truncation.Truncated || len(bundle.Truncation.DroppedItems) == 0 {
			t.Fatalf("expected budget truncation, got %+v", bundle.Truncation)
		}
		if bundle.Summary == nil || bundle.Facts["Plan"] != "pro" {
			t.Fatalf("expected summary and facts preserved under budget, got summary=%v facts=%v", bundle.Summary, bundle.Facts)
		}
	})

	t.Run("OptimisticConcurrencyExpectedVersion", func(t *testing.T) {
		h := newHarness(t)
		ctx := context.Background()

		facts, err := h.Service.GetFacts(ctx, h.ScopeA)
		if err != nil {
			t.Fatal(err)
		}
		if facts.SourceVersion != 1 {
			t.Fatalf("expected initial SourceVersion=1, got %d", facts.SourceVersion)
		}

		// Conflito com versão obsoleta (0 != 1)
		if _, err := h.Service.ApplyFacts(ctx, contextmod.ApplyFactsRequest{
			Scope:           h.ScopeA,
			ExpectedVersion: 0,
			Facts:           map[string]string{"Plan": "enterprise"},
		}); !errors.Is(err, contextmod.ErrVersionConflict) {
			t.Fatalf("expected ErrVersionConflict, got %v", err)
		}

		// Sucesso com versão atual (1 -> 2)
		applied, err := h.Service.ApplyFacts(ctx, contextmod.ApplyFactsRequest{
			Scope:           h.ScopeA,
			ExpectedVersion: 1,
			Facts:           map[string]string{"Plan": "enterprise"},
		})
		if err != nil {
			t.Fatalf("ApplyFacts failed: %v", err)
		}
		if applied.SourceVersion != 2 || applied.Facts["Plan"] != "enterprise" {
			t.Fatalf("unexpected ApplyFactsResult: %+v", applied)
		}
	})

	t.Run("MessagePaginationAndTenantIsolation", func(t *testing.T) {
		h := newHarness(t)
		ctx := context.Background()

		p1, err := h.Service.ListMessages(ctx, h.ScopeA, "", 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(p1.Items) != 2 || !p1.HasMore || p1.NextCursor == "" {
			t.Fatalf("expected 2 items with next cursor, got %+v", p1)
		}

		p2, err := h.Service.ListMessages(ctx, h.ScopeA, p1.NextCursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(p2.Items) != 2 || p2.Items[0].MessageID == p1.Items[0].MessageID {
			t.Fatalf("expected distinct second page, got p1=%v p2=%v", p1.Items, p2.Items)
		}

		// Isolamento entre tenants
		kA, err := h.Service.ListKnowledge(ctx, h.ScopeA.TenantID)
		if err != nil {
			t.Fatal(err)
		}
		for _, doc := range kA {
			if doc.Name == "SEGREDO_B.md" {
				t.Fatalf("tenant A saw tenant B knowledge doc")
			}
		}
		searchRes, err := h.Service.SearchKnowledge(ctx, h.ScopeA.TenantID, "exclusivo tenant B", 5)
		if err != nil {
			t.Fatal(err)
		}
		if len(searchRes.Hits) != 0 {
			t.Fatalf("expected zero cross-tenant search hits, got %+v", searchRes.Hits)
		}
	})
}

type staticKnowledgeReader struct {
	blobs core.BlobStore
}

func (r staticKnowledgeReader) Load(ctx context.Context, tenantID string) ([]contextmod.KnowledgeDocView, string, error) {
	prefix := tenantID + "/knowledge/"
	keys, err := r.blobs.List(ctx, prefix)
	if err != nil {
		return nil, "", err
	}
	var docs []contextmod.KnowledgeDocView
	var qa string
	for _, k := range keys {
		data, err := r.blobs.Get(ctx, k)
		if err != nil {
			continue
		}
		name := strings.TrimPrefix(k, prefix)
		if strings.EqualFold(name, "QA.md") {
			qa = string(data)
			continue
		}
		docs = append(docs, contextmod.KnowledgeDocView{Name: name, Content: string(data)})
	}
	return docs, qa, nil
}

type staticPackResolver struct {
	pk *pack.Pack
}

func (r staticPackResolver) ResolvePack(ctx context.Context, tenantID string) (*pack.Pack, error) {
	return r.pk, nil
}

func strPtr(s string) *string { return &s }
