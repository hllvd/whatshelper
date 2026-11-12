//go:build llm_live

package brain_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"whatshelper/internal/brain"
	"whatshelper/internal/config"
	"whatshelper/internal/core"
	"whatshelper/internal/core/fakes"
	"whatshelper/internal/llm"
	"whatshelper/internal/pack"
)

func TestLive_SmokeLLM(t *testing.T) {
	_ = config.LoadDotEnv(filepath.Join("..", "..", ".env"))
	cfg := config.LoadFromEnv()
	if cfg.LLMProvider == "fake" {
		t.Fatal("defina LLM_PROVIDER=anthropic|gemini|deepseek e chave/modelos no .env ou ambiente para testes -tags=llm_live")
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config inválida para live LLM: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client, aModel, rModel, nModel, err := llm.NewFromConfig(ctx, cfg, core.SystemClock{})
	if err != nil {
		t.Fatal(err)
	}

	clock := fakes.NewFakeClock(time.Date(2026, 11, 11, 14, 0, 0, 0, time.UTC))
	memStore := fakes.NewMemoryStoreWithClock(clock)
	blobs := fakes.NewMemoryBlobStore()
	tenantsDir := filepath.Join("..", "..", "tenants")
	packsDir := filepath.Join("..", "..", "packs")
	_, _ = brain.SeedLocalTenants(ctx, tenantsDir, memStore)
	_, _ = brain.SeedLocalKnowledge(ctx, tenantsDir, blobs)

	p := brain.NewPipeline(ctx, brain.PipelineConfig{
		Store:         memStore,
		Blobs:         blobs,
		LLM:           client,
		Bus:           core.NewInMemoryEventBus(32),
		Clock:         clock,
		Tenants:       brain.NewStoreTenantPolicyLoader(memStore, nil),
		Knowledge:     brain.NewBlobKnowledgeSource(blobs, clock, 5*time.Second),
		PackLoader:    func(name string) (*pack.Pack, error) { return pack.LoadFromDir(filepath.Join(packsDir, name)) },
		AnalysisModel: aModel,
		ResponseModel: rModel,
		NudgeModel:    nModel,
	})
	defer p.Stop()

	res, err := p.ReplayIsolated(ctx, brain.ReplayRequest{
		TenantID: "demo-support",
		Messages: []brain.ReplayMessage{{Text: "Qual o SLA do plano Pro?"}},
	})
	if err != nil {
		t.Fatalf("live replay (%s) failed: %v", cfg.LLMProvider, err)
	}
	if len(res.Turns) != 1 || res.Turns[0].ReplyText == "" {
		t.Fatalf("expected non-empty live reply (%s), got %+v", cfg.LLMProvider, res)
	}
	t.Logf("Live reply (%s): %s", cfg.LLMProvider, res.Turns[0].ReplyText)
}
