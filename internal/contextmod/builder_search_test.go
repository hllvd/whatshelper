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

func TestRetrieveRelevantKnowledge_SectionChunkingAndTokenReduction(t *testing.T) {
	docs := []contextmod.KnowledgeDocView{
		{
			Name:    "README.md",
			Content: "# Base de Conhecimento\n\nColoque aqui os arquivos .md com regras comerciais.",
		},
		{
			Name: "como-gerenciar-contratos-admin.md",
			Content: strings.Join([]string{
				"# Como Gerenciar Contratos (Visão Administrativa)",
				"",
				"A tela **Contratos** (`#/contracts`) é a central administrativa.",
				"",
				"## 1. Pré-requisitos",
				"- Possuir perfil Admin (`system:admin`).",
				"",
				"## 2. Passo a Passo",
				"### Passo 1: Acessar e Filtrar Contratos",
				"No menu lateral, clique em Contratos (`#/contracts`) e filtre por Matrícula.",
				"",
				"### Passo 4: Importar ou Exportar Contratos",
				"Clique em Exportar no topo para baixar a planilha consolidada.",
				"",
				"## 3. Dúvidas Comuns / Troubleshooting",
				"- Sincronização com o Avapro pode demorar até 24h.",
			}, "\n"),
		},
		{
			Name: "como-atualizar-senha-powerbi-scrape.md",
			Content: strings.Join([]string{
				"# Como Atualizar Senha do PowerBI Scrape",
				"",
				"## Passo 1: Acessar Credenciais",
				"Vá em Configurações > Integrações > PowerBI e atualize a senha do robô de scrape.",
			}, "\n"),
		},
		{
			Name: "como-gerenciar-equipes.md",
			Content: strings.Join([]string{
				"# Como Gerenciar Equipes",
				"",
				"## Criar Nova Equipe",
				"Acesse o menu Equipes e adicione os líderes e consultores.",
			}, "\n"),
		},
	}

	fullTokens := 0
	for _, d := range docs {
		fullTokens += contextmod.EstimateTokens(d.Content)
	}

	// 1) Busca específica sobre Avapro e contratos -> deve trazer apenas seções relevantes, ignorando PowerBI, Equipes e README placeholder
	rel := contextmod.RetrieveRelevantKnowledge(docs, "contrato nao aparece sincronizacao avapro 24h", 2)
	if len(rel) != 1 {
		t.Fatalf("expected only 1 relevant doc grouped, got %d (%+v)", len(rel), rel)
	}
	if rel[0].Name != "como-gerenciar-contratos-admin.md" {
		t.Fatalf("expected como-gerenciar-contratos-admin.md, got %s", rel[0].Name)
	}
	if !strings.Contains(rel[0].Content, "Avapro") || !strings.Contains(rel[0].Content, "24h") {
		t.Fatalf("expected troubleshooting section with Avapro and 24h, got:\n%s", rel[0].Content)
	}
	if strings.Contains(rel[0].Content, "Baixar a planilha consolidada") {
		t.Fatalf("expected unrelated section (Passo 4) omitted from chunked doc, got:\n%s", rel[0].Content)
	}
	filteredTokens := contextmod.EstimateTokens(rel[0].Content)
	if filteredTokens >= fullTokens/2 {
		t.Fatalf("expected significant token reduction: filtered=%d full=%d", filteredTokens, fullTokens)
	}

	// 2) Saudação ou pergunta fora da base -> deve retornar lista vazia (0 tokens de conhecimento)
	empty := contextmod.RetrieveRelevantKnowledge(docs, "oi bom dia tudo bem", 4)
	if len(empty) != 0 {
		t.Fatalf("expected empty knowledge slice for greeting without domain terms, got %+v", empty)
	}
	unrelated := contextmod.RetrieveRelevantKnowledge(docs, "previsao do tempo em curitiba amanha", 4)
	if len(unrelated) != 0 {
		t.Fatalf("expected empty knowledge slice for unrelated query, got %+v", unrelated)
	}
}

func TestFilterRelevantQA_SmallPassthroughAndLargeFiltering(t *testing.T) {
	smallQA := "# Perguntas e Respostas Aprendidas (QA)\n\n## Qual o prazo?\nO prazo é de 24h úteis.\n"
	if got := contextmod.FilterRelevantQA(smallQA, "pergunta qualquer", 3, 250); got != strings.TrimSpace(smallQA) {
		t.Fatalf("expected small QA kept intact, got %q", got)
	}

	var b strings.Builder
	b.WriteString("# Perguntas e Respostas Aprendidas (QA)\n")
	for i := 1; i <= 25; i++ {
		b.WriteString("\n## Como configurar o módulo secundário número ")
		b.WriteString(strings.Repeat("x", 30))
		b.WriteString("?\nPara configurar o módulo secundário, acesse o painel lateral e salve a opção correspondente.\n")
	}
	b.WriteString("\n## Como corrigir erro 502 na exportação CSV?\nUse o endpoint assíncrono /v2/exports para arquivos acima de 50000 linhas.\n")
	largeQA := b.String()

	filtered := contextmod.FilterRelevantQA(largeQA, "erro 502 exportacao csv", 2, 100)
	if !strings.Contains(filtered, "/v2/exports") {
		t.Fatalf("expected matching QA entry preserved, got:\n%s", filtered)
	}
	if contextmod.EstimateTokens(filtered) >= contextmod.EstimateTokens(largeQA)/3 {
		t.Fatalf("expected large QA filtered down significantly: filtered=%d full=%d",
			contextmod.EstimateTokens(filtered), contextmod.EstimateTokens(largeQA))
	}
}
