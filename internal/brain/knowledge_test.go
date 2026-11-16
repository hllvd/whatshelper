package brain_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"whatshelper/internal/brain"
	"whatshelper/internal/contextmod"
	"whatshelper/internal/core"
	"whatshelper/internal/core/fakes"
	"whatshelper/internal/pack"
	"whatshelper/internal/store"
)

func TestLocalTenantAndKnowledgeSource_ReloadsOnChange(t *testing.T) {
	tmp := t.TempDir()
	kDir := filepath.Join(tmp, "demo", "knowledge")
	if err := os.MkdirAll(kDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(tmp, "demo", "tenant.yaml"), []byte("id: demo\nname: Demo\npack: sample\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kDir, "faq.md"), []byte("# FAQ v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kDir, "QA.md"), []byte("P: Oi?\nR: Olá!"), 0o644); err != nil {
		t.Fatal(err)
	}

	tl := brain.NewLocalTenantLoader(tmp)
	cfg, err := tl.LoadTenant(context.Background(), "demo")
	if err != nil {
		t.Fatalf("LoadTenant failed: %v", err)
	}
	if cfg.Pack != "sample" {
		t.Fatalf("expected pack sample, got %s", cfg.Pack)
	}

	ks := brain.NewLocalKnowledgeSource(tmp)
	b1, err := ks.Load(context.Background(), "demo")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(b1.Docs) != 1 || b1.Docs[0].Name != "faq.md" || b1.Docs[0].Content != "# FAQ v1" {
		t.Fatalf("unexpected docs: %+v", b1.Docs)
	}
	if b1.QAContent != "P: Oi?\nR: Olá!" {
		t.Fatalf("unexpected QAContent: %q", b1.QAContent)
	}

	// Segunda leitura sem alteração mantém mesmo fingerprint
	b2, err := ks.Load(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if b1.Fingerprint != b2.Fingerprint {
		t.Fatalf("expected identical fingerprint on cached load")
	}

	// Alterando faq.md deve invalidar cache
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(kDir, "faq.md"), []byte("# FAQ v2 atualizado"), 0o644); err != nil {
		t.Fatal(err)
	}
	b3, err := ks.Load(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if b3.Fingerprint == b1.Fingerprint {
		t.Fatalf("expected fingerprint to change after file edit")
	}
	if b3.Docs[0].Content != "# FAQ v2 atualizado" {
		t.Fatalf("expected updated content, got %q", b3.Docs[0].Content)
	}

	// Adicionando personality.md na raiz do tenant deve carregar no bundle e invalidar cache
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(tmp, "demo", "personality.md"), []byte("# Tom Amigável"), 0o644); err != nil {
		t.Fatal(err)
	}
	b4, err := ks.Load(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if b4.Fingerprint == b3.Fingerprint {
		t.Fatalf("expected fingerprint to change after adding personality.md")
	}
	if b4.Personality != "# Tom Amigável" {
		t.Fatalf("expected Personality to be loaded, got %q", b4.Personality)
	}
}

func TestBlobKnowledgeSource_TTLCache(t *testing.T) {
	ctx := context.Background()
	blobs := fakes.NewMemoryBlobStore()
	clock := fakes.NewFakeClock(time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC))

	_ = blobs.Put(ctx, store.S3KnowledgeKey("t1", "servicos.md"), []byte("Serviço A"), "text/markdown")
	_ = blobs.Put(ctx, store.S3KnowledgeKey("t1", "QA.md"), []byte("QA v1"), "text/markdown")

	ks := brain.NewBlobKnowledgeSource(blobs, clock, 5*time.Second)
	b1, err := ks.Load(ctx, "t1")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(b1.Docs) != 1 || b1.QAContent != "QA v1" {
		t.Fatalf("unexpected bundle: %+v", b1)
	}

	// Atualiza no blob dentro do TTL -> ainda retorna cache
	_ = blobs.Put(ctx, store.S3KnowledgeKey("t1", "QA.md"), []byte("QA v2"), "text/markdown")
	bCached, _ := ks.Load(ctx, "t1")
	if bCached.QAContent != "QA v1" {
		t.Fatalf("expected cached QA v1 within TTL, got %q", bCached.QAContent)
	}

	// Avança relógio além do TTL -> recarrega
	clock.Advance(6 * time.Second)
	bFresh, _ := ks.Load(ctx, "t1")
	if bFresh.QAContent != "QA v2" {
		t.Fatalf("expected refreshed QA v2 after TTL, got %q", bFresh.QAContent)
	}
}

func TestStoreTenantPolicyLoader_AndSeed(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	tDir := filepath.Join(tmp, "demo")
	if err := os.MkdirAll(tDir, 0o755); err != nil {
		t.Fatal(err)
	}
	yamlBody := `id: demo
name: Demo
pack: sample
mode: approval
timezone: America/Sao_Paulo
bot_enabled: true
proactive_enabled: false
business_hours:
  enabled: true
  days: [mon, tue, wed, thu, fri]
  start: "09:00"
  end: "18:00"
`
	if err := os.WriteFile(filepath.Join(tDir, "tenant.yaml"), []byte(yamlBody), 0o644); err != nil {
		t.Fatal(err)
	}

	mem := fakes.NewMemoryStore()
	local := brain.NewLocalTenantLoader(tmp)
	loader := brain.NewStoreTenantPolicyLoader(mem, local)

	// Antes do seed, cai no fallback local
	fallbackCfg, err := loader.LoadTenant(ctx, "demo")
	if err != nil {
		t.Fatalf("fallback LoadTenant failed: %v", err)
	}
	if fallbackCfg.Mode != core.ModeApproval || !fallbackCfg.BotEnabled {
		t.Fatalf("unexpected fallback policy: %+v", fallbackCfg)
	}

	// Faz seed para o Store
	seeded, err := brain.SeedLocalTenants(ctx, tmp, mem)
	if err != nil {
		t.Fatalf("SeedLocalTenants failed: %v", err)
	}
	if len(seeded) != 1 || seeded[0] != "demo" {
		t.Fatalf("expected [demo], got %v", seeded)
	}

	// Atualiza política no Store diretamente e confirma leitura do Store
	override := fallbackCfg
	override.Mode = core.ModeDraft
	if err := brain.SeedTenantPolicy(ctx, mem, override); err != nil {
		t.Fatalf("SeedTenantPolicy failed: %v", err)
	}

	storeOnly := brain.NewStoreTenantPolicyLoader(mem, nil)
	got, err := storeOnly.LoadTenant(ctx, "demo")
	if err != nil {
		t.Fatalf("storeOnly LoadTenant failed: %v", err)
	}
	if got.Mode != core.ModeDraft {
		t.Fatalf("expected mode draft from store, got %s", got.Mode)
	}
}

func TestSeedLocalKnowledge_And_InvalidateCache(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	kDir := filepath.Join(tmp, "demo", "knowledge")
	_ = os.MkdirAll(kDir, 0o755)
	_ = os.WriteFile(filepath.Join(kDir, "info.md"), []byte("Versão 1"), 0o644)

	blobs := fakes.NewMemoryBlobStore()
	seeded, err := brain.SeedLocalKnowledge(ctx, tmp, blobs)
	if err != nil || len(seeded) != 1 {
		t.Fatalf("expected 1 seeded doc, got %v err=%v", seeded, err)
	}

	// Não sobrescreve documento já existente no BlobStore
	_ = blobs.Put(ctx, store.S3KnowledgeKey("demo", "info.md"), []byte("Editado no UI"), "text/markdown")
	seededAgain, err := brain.SeedLocalKnowledge(ctx, tmp, blobs)
	if err != nil || len(seededAgain) != 0 {
		t.Fatalf("expected 0 docs re-seeded, got %v err=%v", seededAgain, err)
	}

	clock := fakes.NewFakeClock(time.Date(2026, 11, 11, 14, 0, 0, 0, time.UTC))
	ks := brain.NewBlobKnowledgeSource(blobs, clock, 60*time.Second)
	b1, _ := ks.Load(ctx, "demo")
	if len(b1.Docs) != 1 || b1.Docs[0].Content != "Editado no UI" {
		t.Fatalf("unexpected doc content: %+v", b1.Docs)
	}

	_ = blobs.Put(ctx, store.S3KnowledgeKey("demo", "info.md"), []byte("Editado v3"), "text/markdown")
	ks.Invalidate("demo")
	b2, _ := ks.Load(ctx, "demo")
	if b2.Docs[0].Content != "Editado v3" {
		t.Fatalf("expected immediate reload after Invalidate, got %q", b2.Docs[0].Content)
	}
}

func TestBuildKnowledgeSearchQuery_AndSupporterTokenSavings(t *testing.T) {
	ctx := context.Background()
	ks := brain.NewLocalKnowledgeSource(filepath.Join("..", "..", "tenants"))
	bundle, err := ks.Load(ctx, "supporter")
	if err != nil {
		t.Fatalf("failed to load supporter knowledge: %v", err)
	}
	if len(bundle.Docs) < 10 {
		t.Fatalf("expected at least 10 docs in supporter tenant, got %d", len(bundle.Docs))
	}

	query := brain.BuildKnowledgeSearchQuery(
		"Como faço para importar contratos do PowerBI?",
		"Usuário quer importar contratos via PowerBI.",
		"ask_question",
		map[string]string{"Topic": "powerbi", "Confirmed": "true"},
	)

	docsView := make([]contextmod.KnowledgeDocView, len(bundle.Docs))
	totalRawChars := 0
	for i, d := range bundle.Docs {
		docsView[i] = contextmod.KnowledgeDocView{Name: d.Name, Content: d.Content}
		totalRawChars += len(d.Content)
	}

	relevant := contextmod.RetrieveRelevantKnowledge(docsView, query, 4)
	if len(relevant) == 0 || len(relevant) >= len(bundle.Docs) {
		t.Fatalf("expected selective subset of docs, got %d out of %d", len(relevant), len(bundle.Docs))
	}
	if relevant[0].Name != "como-importar-contratos-powerbi.md" {
		t.Fatalf("expected top doc como-importar-contratos-powerbi.md, got %s", relevant[0].Name)
	}

	filteredChars := 0
	for _, d := range relevant {
		filteredChars += len(d.Content)
	}
	if filteredChars >= totalRawChars/3 {
		t.Fatalf("expected >66%% reduction in knowledge payload size, got filtered=%d total=%d", filteredChars, totalRawChars)
	}

	// Verifica que o Grounding continua válido com os documentos filtrados por seção
	filteredBundle := brain.KnowledgeBundle{
		Docs:      make([]pack.KnowledgeDoc, len(relevant)),
		QAContent: bundle.QAContent,
	}
	for i, d := range relevant {
		filteredBundle.Docs[i] = pack.KnowledgeDoc{Name: d.Name, Content: d.Content}
	}
	gr := brain.CheckGrounding(
		brain.StructuredReply{
			Answerable: true,
			Sources:    []string{"como-importar-contratos-powerbi.md"},
			Reply:      "Você pode importar os contratos na tela de importação do PowerBI.",
		},
		filteredBundle,
		nil,
		pack.PromptMessage{Sender: "user", Text: "Como faço para importar contratos do PowerBI?"},
		nil,
	)
	if !gr.Grounded {
		t.Fatalf("expected CheckGrounding to succeed with section-filtered knowledge, got: %+v", gr)
	}
}
