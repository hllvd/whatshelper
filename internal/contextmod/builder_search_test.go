package contextmod_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"whatshelper/internal/contextmod"
	"whatshelper/internal/pack"
)

type failingProvider struct{}

func (failingProvider) Name() string { return "summary" }
func (failingProvider) Schema() contextmod.ProviderSchema {
	return contextmod.ProviderSchema{Name: "summary", BlockKind: "summary"}
}
func (failingProvider) Fetch(ctx context.Context, req contextmod.ProviderRequest) (*contextmod.ProviderResult, error) {
	return nil, errors.New("boom")
}

type staticFactsProvider struct{}

func (staticFactsProvider) Name() string { return "facts" }
func (staticFactsProvider) Schema() contextmod.ProviderSchema {
	return contextmod.ProviderSchema{Name: "facts", BlockKind: "facts"}
}
func (staticFactsProvider) Fetch(ctx context.Context, req contextmod.ProviderRequest) (*contextmod.ProviderResult, error) {
	return &contextmod.ProviderResult{
		SourceVersion: 3,
		SourceStatus:  "active",
		Facts:         map[string]string{"Plan": "pro", "InternalOnly": "secret"},
	}, nil
}

func TestBuildContextBundle_BudgetTruncationAndAllowlist(t *testing.T) {
	pk := &pack.Pack{
		ContextModule: &pack.ContextModuleDef{
			ExposedFields: pack.ExposedFieldsDef{
				Facts:   []string{"Plan"},
				Signals: []string{"customer_engaged"},
			},
		},
	}

	specExt, err := contextmod.ResolveViewSpec(contextmod.ViewExternalAgent, pk, 45)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	bundle := contextmod.BuildContextBundle(specExt, contextmod.RawContextData{
		Scope:         contextmod.Scope{TenantID: "demo", Channel: "whatsapp", SourceKey: "5511999998888"},
		SourceVersion: 4,
		SourceStatus:  "active",
		Summary:       &contextmod.SummaryView{Text: "Cliente plano pro."},
		Facts:         map[string]string{"Plan": "pro", "SecretInternal": "nao-expor"},
		Signals:       map[string]bool{"customer_engaged": true, "internal_flag": true},
		RecentMessages: []contextmod.MessageView{
			{MessageID: "m1", Timestamp: now.Add(-2 * time.Minute), Sender: "user", Text: strings.Repeat("antiga ", 30)},
			{MessageID: "m2", Timestamp: now.Add(-time.Minute), Sender: "user", Text: "ultima"},
		},
		GeneratedAt: now,
	})

	if _, ok := bundle.Facts["SecretInternal"]; ok {
		t.Fatalf("expected SecretInternal filtered out by external_agent allowlist")
	}
	if bundle.Facts["Plan"] != "pro" {
		t.Fatalf("expected Plan=pro preserved, got %+v", bundle.Facts)
	}
	if _, ok := bundle.Signals["internal_flag"]; ok {
		t.Fatalf("expected internal_flag filtered out")
	}
	if !bundle.Truncation.Truncated || len(bundle.Truncation.DroppedItems) == 0 {
		t.Fatalf("expected truncation report when budget exceeded, got %+v", bundle.Truncation)
	}
	if bundle.Truncation.DroppedItems[0].ItemID != "m1" {
		t.Fatalf("expected oldest message m1 dropped first, got %+v", bundle.Truncation.DroppedItems)
	}
	if len(bundle.RecentMessages) == 1 && !strings.Contains(bundle.RecentMessages[0].Text, "<<<UNTRUSTED_MESSAGE>>>") {
		t.Fatalf("expected delimited untrusted message in external_agent view, got %q", bundle.RecentMessages[0].Text)
	}
}

func TestProviderRegistry_IsolatesFailures(t *testing.T) {
	reg := contextmod.NewProviderRegistry(nil, failingProvider{}, staticFactsProvider{})
	raw := reg.Collect(context.Background(), contextmod.ProviderRequest{
		Scope: contextmod.Scope{TenantID: "demo", Channel: "simulator", SourceKey: "u1"},
	}, nil)

	if len(raw.DegradedProviders) != 1 || raw.DegradedProviders[0] != "summary" {
		t.Fatalf("expected degraded provider summary, got %v", raw.DegradedProviders)
	}
	if raw.Facts["Plan"] != "pro" {
		t.Fatalf("expected facts provider to still succeed despite summary failure")
	}
}

func TestBM25Searcher_AccentFoldingAndRanking(t *testing.T) {
	searcher := contextmod.NewBM25Searcher()
	docs := []contextmod.KnowledgeDocView{
		{Name: "FAQ.md", Content: "Horário de atendimento: segunda a sexta das 9h às 18h.\n"},
		{Name: "SLA.md", Content: "O SLA do plano Pro é de 4 horas úteis para incidentes críticos.\n"},
		{Name: "QA.md", Content: "P: Qual o horário?\nR: Das 9h às 18h.\n"},
	}

	res, err := searcher.Search(context.Background(), docs, "horario atendimento", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) == 0 || res.Hits[0].DocName != "FAQ.md" {
		t.Fatalf("expected FAQ.md ranked first, got %+v", res.Hits)
	}
}
