package brain_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"whatshelper/internal/brain"
	"whatshelper/internal/core"
	"whatshelper/internal/core/fakes"
	"whatshelper/internal/llm"
	"whatshelper/internal/pack"
)

func TestPipeline_PerStageRoutingAndVisionEnrichment(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	packDir := filepath.Join(tmp, "packs", "sample")
	if err := os.MkdirAll(filepath.Join(packDir, "prompts"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(packDir, "pack.yaml"), []byte(testPackYAML), 0o644)
	_ = os.WriteFile(filepath.Join(packDir, "prompts", "analysis.tmpl"), []byte("Analyze:\n{{.OutputSchema}}\n<!-- USER_PROMPT -->\nMSG: {{.NewMessage.Text}}"), 0o644)
	_ = os.WriteFile(filepath.Join(packDir, "prompts", "reply.tmpl"), []byte("Reply using:\n{{range .Knowledge}}{{.Content}}\n{{end}}\n<!-- USER_PROMPT -->\nMsg: {{.NewMessage.Text}}"), 0o644)

	kDir := filepath.Join(tmp, "tenants", "demo", "knowledge")
	if err := os.MkdirAll(kDir, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(tmp, "tenants", "demo", "tenant.yaml"), []byte(`id: demo
name: Demo
pack: sample
llm_overrides:
  response:
    provider: anthropic
    model: claude-sonnet-4-20250514
  vision:
    provider: gemini
    model: gemini-2.5-flash
`), 0o644)
	_ = os.WriteFile(filepath.Join(kDir, "info.md"), []byte("Plano A custa 2000."), 0o644)

	clock := fakes.NewFakeClock(time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC))
	store := fakes.NewMemoryStore()
	blobs := fakes.NewMemoryBlobStore()
	bus := core.NewInMemoryEventBus(32)
	simCh := fakes.NewMemoryChannel("simulator", 16)
	_ = simCh.Start(ctx)

	const imgRef = "media/demo/img1.png"
	if err := blobs.Put(ctx, imgRef, []byte{0x89, 0x50, 0x4E, 0x47}, "image/png"); err != nil {
		t.Fatal(err)
	}

	var analysisReq, responseReq, visionReq core.LLMRequest

	openaiStub := fakes.NewScriptedLLM(func(_ context.Context, req core.LLMRequest) (*core.LLMResponse, error) {
		if req.Stage == "analysis" {
			analysisReq = req
			b, _ := json.Marshal(map[string]any{
				"summary":     "Cliente enviou imagem de documento.",
				"last_sender": "customer",
				"facts":       map[string]any{},
				"signals":     map[string]any{"has_new_question": true},
				"context":     map[string]any{"last_action": map[string]any{"type": "question"}},
			})
			return &core.LLMResponse{Content: string(b), Provider: "openai", Model: req.Model}, nil
		}
		return &core.LLMResponse{Content: "ok", Provider: "openai", Model: req.Model}, nil
	})

	anthropicStub := fakes.NewScriptedLLM(func(_ context.Context, req core.LLMRequest) (*core.LLMResponse, error) {
		responseReq = req
		return &core.LLMResponse{Content: "Plano A custa 2000.", Provider: "anthropic", Model: req.Model}, nil
	})

	geminiStub := fakes.NewScriptedLLM(func(_ context.Context, req core.LLMRequest) (*core.LLMResponse, error) {
		visionReq = req
		return &core.LLMResponse{Content: "Foto de documento nítido.", Provider: "gemini", Model: req.Model}, nil
	})

	router := llm.NewRouterFromMap("openai", map[string]core.LLM{
		"openai":    openaiStub,
		"anthropic": anthropicStub,
		"gemini":    geminiStub,
	}, map[string]core.StageLLMConfig{
		"analysis": {Provider: "openai", Model: "gpt-4.1"},
		"response": {Provider: "openai", Model: "gpt-4.1-mini"},
		"vision":   {Provider: "openai", Model: "gpt-4.1-mini"},
	})

	p := brain.NewPipeline(ctx, brain.PipelineConfig{
		Store:         store,
		Blobs:         blobs,
		LLM:           router,
		Bus:           bus,
		Clock:         clock,
		Tenants:       brain.NewLocalTenantLoader(filepath.Join(tmp, "tenants")),
		Knowledge:     brain.NewLocalKnowledgeSource(filepath.Join(tmp, "tenants")),
		PackLoader:    func(name string) (*pack.Pack, error) { return pack.LoadFromDir(filepath.Join(tmp, "packs", name)) },
		AnalysisModel: "gpt-4.1",
		ResponseModel: "gpt-4.1-mini",
		NudgeModel:    "gpt-4.1-nano",
	})
	p.RegisterChannel(simCh)

	sub := bus.Subscribe(core.EventFilter{TenantID: "demo"})
	defer sub.Cancel()

	err := p.Ingest(ctx, core.NormalizedMessage{
		TenantID:  "demo",
		Channel:   "simulator",
		SourceKey: "user1",
		MessageID: "msg1",
		Direction: core.DirectionInbound,
		Type:      core.MessageTypeImage,
		MediaRef:  imgRef,
		Timestamp: clock.Now(),
	})
	if err != nil {
		t.Fatalf("Ingest failed: %v", err)
	}

	var eventTypes []core.EventType
	deadline := time.After(2 * time.Second)
waitLoop:
	for {
		select {
		case ev := <-sub.Events():
			eventTypes = append(eventTypes, ev.Type)
			if ev.Type == core.EventReplySent {
				break waitLoop
			}
		case <-deadline:
			t.Fatalf("timeout aguardando EventReplySent; eventos recebidos: %v", eventTypes)
		}
	}
	p.Stop()

	if visionReq.Provider != "gemini" || visionReq.Model != "gemini-2.5-flash" || len(visionReq.MediaParts) != 1 {
		t.Fatalf("expected vision routed to gemini with media part, got %+v", visionReq)
	}
	if analysisReq.Provider != "openai" || analysisReq.Model != "gpt-4.1" {
		t.Fatalf("expected analysis routed to openai gpt-4.1, got %+v", analysisReq)
	}
	if !strings.Contains(analysisReq.Prompt, "Foto de documento nítido.") {
		t.Fatalf("expected analysis prompt to include vision description, got %q", analysisReq.Prompt)
	}
	if responseReq.Provider != "anthropic" || responseReq.Model != "claude-sonnet-4-20250514" {
		t.Fatalf("expected response routed to anthropic claude-sonnet-4-20250514, got %+v", responseReq)
	}
}

type staticTenantLoader struct {
	policy core.TenantPolicy
}

func (s staticTenantLoader) LoadTenant(_ context.Context, _ string) (core.TenantPolicy, error) {
	return s.policy, nil
}
