//go:build integration

package brain_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"whatshelper/internal/brain"
	"whatshelper/internal/config"
	"whatshelper/internal/core"
	"whatshelper/internal/core/fakes"
	"whatshelper/internal/pack"
	"whatshelper/internal/store/dynamo"
	"whatshelper/internal/store/s3blob"
)

func TestWindowAndAdaptiveDebounce_DynamoAndS3_Integration(t *testing.T) {
	cfg := config.LoadFromEnv()
	ctx := context.Background()

	ddbClient, err := dynamo.NewClient(ctx, cfg.DynamoDBEndpoint, cfg.S3Region, cfg.S3AccessKey, cfg.S3SecretKey)
	if err != nil {
		t.Fatalf("dynamo.NewClient failed: %v", err)
	}
	tableName := fmt.Sprintf("whatshelper-win-int-%d", time.Now().UnixNano())
	if err := dynamo.EnsureTable(ctx, ddbClient, tableName); err != nil {
		t.Fatalf("EnsureTable(%s) failed: %v", tableName, err)
	}
	t.Cleanup(func() {
		_, _ = ddbClient.DeleteTable(context.Background(), &dynamodb.DeleteTableInput{
			TableName: aws.String(tableName),
		})
	})

	s3Client, err := s3blob.NewClient(ctx, cfg.S3Endpoint, cfg.S3Region, cfg.S3AccessKey, cfg.S3SecretKey)
	if err != nil {
		t.Fatalf("s3blob.NewClient failed: %v", err)
	}
	bucket := fmt.Sprintf("whatshelper-win-int-%d", time.Now().UnixNano())
	if err := s3blob.EnsureBucket(ctx, s3Client, bucket); err != nil {
		t.Fatalf("EnsureBucket(%s) failed: %v", bucket, err)
	}

	clock := fakes.NewFakeClock(time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC))
	st := dynamo.NewStore(ddbClient, tableName, clock)
	blobs := s3blob.NewStore(s3Client, bucket)

	llm := fakes.NewScriptedLLM(nil)
	llm.EnqueueStage("analysis", core.LLMResponse{
		Content: `{"summary":"Pedido triplo consolidado.","last_sender":"customer","facts":{"Goal":"comprar_vender_alugar","Budget":2000},"signals":{"has_new_question":true},"context":{"last_action":{"type":"question"}}}`,
	})
	llm.EnqueueStage("response", core.LLMResponse{
		Content: `{"reply":"Bom dia! Registrei compra, venda e locação em março no plano de 2000.","confidence":"high","used_facts":["Goal","Budget"]}`,
	})

	tmp := t.TempDir()
	packDir := filepath.Join(tmp, "packs", "sample")
	_ = os.MkdirAll(filepath.Join(packDir, "prompts"), 0o755)
	_ = os.WriteFile(filepath.Join(packDir, "pack.yaml"), []byte(m4gTestPackYAML), 0o644)
	_ = os.WriteFile(filepath.Join(packDir, "prompts", "analysis.tmpl"), []byte("Analyze:\n{{.OutputSchema}}\n<!-- USER_PROMPT -->\nMSG: {{.NewMessage.Text}}"), 0o644)
	_ = os.WriteFile(filepath.Join(packDir, "prompts", "reply.tmpl"), []byte("Reply\n<!-- USER_PROMPT -->\nMsg: {{.NewMessage.Text}}"), 0o644)

	kDir := filepath.Join(tmp, "tenants", "demo", "knowledge")
	_ = os.MkdirAll(kDir, 0o755)
	_ = os.WriteFile(filepath.Join(tmp, "tenants", "demo", "tenant.yaml"), []byte("id: demo\nname: Demo\npack: sample\n"), 0o644)
	_ = os.WriteFile(filepath.Join(kDir, "info.md"), []byte("Atendemos compra, venda e locação em março no plano de 2000."), 0o644)

	fs := &fakeScheduler{}
	bus := core.NewInMemoryEventBus(64)
	ch := fakes.NewMemoryChannel("simulator", 16)
	_ = ch.Start(ctx)

	p := brain.NewPipeline(ctx, brain.PipelineConfig{
		Store:         st,
		Blobs:         blobs,
		LLM:           llm,
		Bus:           bus,
		Clock:         clock,
		Tenants:       brain.NewLocalTenantLoader(filepath.Join(tmp, "tenants")),
		Knowledge:     brain.NewLocalKnowledgeSource(filepath.Join(tmp, "tenants")),
		PackLoader:    func(name string) (*pack.Pack, error) { return pack.LoadFromDir(filepath.Join(tmp, "packs", name)) },
		AnalysisModel: "model-a",
		ResponseModel: "model-r",
		Debounce:      8 * time.Second,
		TypingMaxWait: 16 * time.Second,
		AfterFunc:     fs.AfterFunc,
	})
	p.RegisterChannel(ch)
	defer p.Stop()

	fixtureRaw, err := os.ReadFile(filepath.Join("testdata", "m4g_burst_ptbr.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Fragments []string `json:"fragments"`
	}
	if err := json.Unmarshal(fixtureRaw, &fixture); err != nil {
		t.Fatal(err)
	}

	for i, txt := range fixture.Fragments {
		_, err := p.IngestWithStatus(ctx, core.NormalizedMessage{
			TenantID:  "demo",
			Channel:   "simulator",
			SourceKey: "int-user-1",
			MessageID: fmt.Sprintf("int-m-%d", i+1),
			Timestamp: clock.Now().Add(time.Duration(i*2) * time.Second),
			Direction: core.DirectionInbound,
			Origin:    core.OriginCustomer,
			Type:      core.MessageTypeText,
			Text:      txt,
		})
		if err != nil {
			t.Fatalf("Ingest %d failed: %v", i, err)
		}
	}

	fs.FireAllActive()
	sent := waitForSentCount(t, ch, 1)
	if len(sent) != 1 {
		t.Fatalf("expected 1 unified reply in DynamoDB+S3 integration, got %d", len(sent))
	}

	runs, err := st.ListRuns(ctx, "demo", "simulator", "int-user-1", 5)
	if err != nil || len(runs) != 1 {
		t.Fatalf("expected 1 persisted run in DynamoDB, got %d err=%v", len(runs), err)
	}
	rawTrace, err := blobs.Get(ctx, runs[0].TraceBlobRef)
	if err != nil {
		t.Fatalf("Get trace from MinIO failed: %v", err)
	}
	var trace brain.RunTrace
	if err := json.Unmarshal(rawTrace, &trace); err != nil {
		t.Fatal(err)
	}
	if trace.BatchSize != 4 || trace.Window == nil || len(trace.Window.Batch) != 4 {
		t.Fatalf("expected trace window with 4 batch items in MinIO, got batch_size=%d window=%+v", trace.BatchSize, trace.Window)
	}
}
