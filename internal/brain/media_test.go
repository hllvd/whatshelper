package brain_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"whatshelper/internal/brain"
	"whatshelper/internal/core"
	"whatshelper/internal/core/fakes"
	"whatshelper/internal/pack"
	"whatshelper/internal/store"
)

func setupMediaPipelineTest(t *testing.T, transcriber core.AudioTranscriber, llm *fakes.ScriptedLLM) (*brain.Pipeline, *fakes.MemoryStore, *fakes.MemoryBlobStore, *fakes.MemoryChannel) {
	t.Helper()
	tmp := t.TempDir()
	packDir := filepath.Join(tmp, "packs", "sample")
	_ = os.MkdirAll(filepath.Join(packDir, "prompts"), 0o755)
	_ = os.WriteFile(filepath.Join(packDir, "pack.yaml"), []byte(testPackYAML), 0o644)
	_ = os.WriteFile(filepath.Join(packDir, "prompts", "analysis.tmpl"), []byte("Analyze:\n{{.OutputSchema}}\n<!-- USER_PROMPT -->\nMSG: {{.NewMessage.Text}}"), 0o644)
	_ = os.WriteFile(filepath.Join(packDir, "prompts", "reply.tmpl"), []byte("Reply using:\n{{range .Knowledge}}{{.Content}}\n{{end}}\n<!-- USER_PROMPT -->\nMsg: {{.NewMessage.Text}}"), 0o644)

	clock := fakes.NewFakeClock(time.Date(2026, 11, 11, 18, 0, 0, 0, time.UTC))
	st := fakes.NewMemoryStoreWithClock(clock)
	blobs := fakes.NewMemoryBlobStore()
	bus := core.NewInMemoryEventBus(16)

	_ = brain.SeedTenantPolicy(context.Background(), st, core.TenantPolicy{
		ID:         "demo",
		Pack:       "sample",
		Mode:       core.ModeAuto,
		BotEnabled: true,
	}.WithDefaults())
	_ = blobs.Put(context.Background(), store.S3KnowledgeKey("demo", "FAQ.md"), []byte("Funcionamos das 9h às 18h."), "text/markdown")

	ch := fakes.NewMemoryChannel("whatsapp", 16)
	_ = ch.Start(context.Background())

	p := brain.NewPipeline(context.Background(), brain.PipelineConfig{
		Store:         st,
		Blobs:         blobs,
		LLM:           llm,
		Bus:           bus,
		Clock:         clock,
		Tenants:       brain.NewStoreTenantPolicyLoader(st, nil),
		Knowledge:     brain.NewBlobKnowledgeSource(blobs, clock, 60*time.Second),
		PackLoader:    func(name string) (*pack.Pack, error) { return pack.LoadFromDir(filepath.Join(tmp, "packs", name)) },
		AnalysisModel: "m-a",
		ResponseModel: "m-r",
		Debounce:      time.Hour,
		Transcriber:   transcriber,
	})
	p.RegisterChannel(ch)
	return p, st, blobs, ch
}

func TestPipeline_AudioTranscribedFeedsLLM(t *testing.T) {
	ctx := context.Background()
	llm := fakes.NewScriptedLLM(nil)
	llm.EnqueueStage("analysis", core.LLMResponse{
		Content: `{"summary":"Cliente perguntou horário via áudio.","last_sender":"customer","intent":"ask_info","facts":{},"signals":{"has_new_question":true},"context":{}}`,
	})
	llm.EnqueueStage("response", core.LLMResponse{
		Content: `{"answerable":true,"sources":["FAQ.md"],"reply":"Nosso atendimento funciona das 9h às 18h."}`,
	})

	tr := &fakes.FakeTranscriber{Replies: []string{"Qual o horário de atendimento?"}}
	p, _, blobs, ch := setupMediaPipelineTest(t, tr, llm)
	defer p.Stop()

	ts := time.Date(2026, 11, 11, 18, 0, 0, 0, time.UTC)
	mediaKey := store.S3MediaKey("demo", "whatsapp", "5511999998888", ts, "aud-1", "ogg")
	_ = blobs.Put(ctx, mediaKey, []byte("ogg-bytes"), "audio/ogg")

	msg := core.NormalizedMessage{
		TenantID: "demo", Channel: "whatsapp", SourceKey: "5511999998888",
		MessageID: "wa-aud-1", Timestamp: ts, Direction: core.DirectionInbound,
		Type: core.MessageTypeAudio, MediaRef: mediaKey,
	}
	if err := p.Ingest(ctx, msg); err != nil {
		t.Fatal(err)
	}
	if err := p.ProcessBatch(ctx, []core.NormalizedMessage{msg}); err != nil {
		t.Fatal(err)
	}

	if tr.Calls != 1 {
		t.Fatalf("expected 1 transcriber call, got %d", tr.Calls)
	}
	calls := llm.Calls()
	if len(calls) < 1 || !strings.Contains(calls[0].Prompt, "Qual o horário de atendimento?") {
		t.Fatalf("expected transcribed audio text in analysis prompt, got %+v", calls)
	}
	if len(ch.SentMessages()) != 1 || !strings.Contains(ch.SentMessages()[0].Text, "9h às 18h") {
		t.Fatalf("expected grounded reply to audio, got %+v", ch.SentMessages())
	}
}

func TestPipeline_AudioWithoutTranscriberEscalatesWithNeutralFallback(t *testing.T) {
	ctx := context.Background()
	llm := fakes.NewScriptedLLM(nil)
	p, st, blobs, ch := setupMediaPipelineTest(t, nil, llm)
	defer p.Stop()

	ts := time.Date(2026, 11, 11, 18, 0, 0, 0, time.UTC)
	mediaKey := store.S3MediaKey("demo", "whatsapp", "5511999998888", ts, "aud-2", "ogg")
	_ = blobs.Put(ctx, mediaKey, []byte("ogg-bytes"), "audio/ogg")

	msg := core.NormalizedMessage{
		TenantID: "demo", Channel: "whatsapp", SourceKey: "5511999998888",
		MessageID: "wa-aud-2", Timestamp: ts, Direction: core.DirectionInbound,
		Type: core.MessageTypeAudio, MediaRef: mediaKey,
	}
	if err := p.Ingest(ctx, msg); err != nil {
		t.Fatal(err)
	}
	if err := p.ProcessBatch(ctx, []core.NormalizedMessage{msg}); err != nil {
		t.Fatal(err)
	}

	if len(ch.SentMessages()) != 1 || ch.SentMessages()[0].Text != brain.NeutralAudioFallbackReply {
		t.Fatalf("expected neutral audio fallback reply, got %+v", ch.SentMessages())
	}
	tasks, err := st.ListSourceTasks(ctx, "demo", "whatsapp", "5511999998888")
	if err != nil || len(tasks) != 1 || tasks[0].Kind != "escalation" {
		t.Fatalf("expected escalation task for untranscribed audio, got %+v err=%v", tasks, err)
	}
	if len(llm.Calls()) != 0 {
		t.Fatalf("expected 0 LLM calls when audio cannot be transcribed, got %d", len(llm.Calls()))
	}
}
