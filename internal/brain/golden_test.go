package brain_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"whatshelper/internal/brain"
	"whatshelper/internal/core"
	"whatshelper/internal/core/fakes"
	"whatshelper/internal/pack"
)

type conversationFixture struct {
	Name     string `yaml:"name"`
	TenantID string `yaml:"tenant_id"`
	Pack     string `yaml:"pack"`
	Turns    []struct {
		MessageID    string `yaml:"message_id"`
		Text         string `yaml:"text"`
		MockAnalysis string `yaml:"mock_analysis"`
		MockReply    string `yaml:"mock_reply"`
		Expect       struct {
			Intent         string            `yaml:"intent"`
			DecisionAction string            `yaml:"decision_action"`
			Facts          map[string]string `yaml:"facts"`
			TasksMin       int               `yaml:"tasks_min"`
			TaskType       string            `yaml:"task_type"`
			SourceStatus   string            `yaml:"source_status"`
			LLMCallsDelta  *int              `yaml:"llm_calls_delta"`
			ReplyContains  string            `yaml:"reply_contains"`
		} `yaml:"expect"`
	} `yaml:"turns"`
}

func TestGolden_ConversationFixtures(t *testing.T) {
	fixturePaths, err := filepath.Glob(filepath.Join("..", "..", "testdata", "conversations", "*.yaml"))
	if err != nil || len(fixturePaths) == 0 {
		t.Fatalf("expected conversation fixtures, found=%v err=%v", fixturePaths, err)
	}

	tenantsDir := filepath.Join("..", "..", "tenants")
	packsDir := filepath.Join("..", "..", "packs")
	ctx := context.Background()

	for _, path := range fixturePaths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var fx conversationFixture
		if err := yaml.Unmarshal(raw, &fx); err != nil {
			t.Fatalf("invalid fixture %s: %v", path, err)
		}

		t.Run(fx.Name, func(t *testing.T) {
			now := time.Date(2026, 11, 11, 14, 0, 0, 0, time.UTC) // 11:00 America/Sao_Paulo (dentro do horário)
			clock := fakes.NewFakeClock(now)
			memStore := fakes.NewMemoryStoreWithClock(clock)
			blobs := fakes.NewMemoryBlobStore()
			if _, err := brain.SeedLocalTenants(ctx, tenantsDir, memStore); err != nil {
				t.Fatal(err)
			}
			if _, err := brain.SeedLocalKnowledge(ctx, tenantsDir, blobs); err != nil {
				t.Fatal(err)
			}

			llm := fakes.NewScriptedLLM(nil)
			ch := fakes.NewMemoryChannel("simulator", 16)
			_ = ch.Start(ctx)

			p := brain.NewPipeline(ctx, brain.PipelineConfig{
				Store:         memStore,
				Blobs:         blobs,
				LLM:           llm,
				Bus:           core.NewInMemoryEventBus(64),
				Clock:         clock,
				Tenants:       brain.NewStoreTenantPolicyLoader(memStore, nil),
				Knowledge:     brain.NewBlobKnowledgeSource(blobs, clock, 5*time.Second),
				PackLoader:    func(name string) (*pack.Pack, error) { return pack.LoadFromDir(filepath.Join(packsDir, name)) },
				AnalysisModel: "golden-a",
				ResponseModel: "golden-r",
			})
			p.RegisterChannel(ch)
			defer p.Stop()

			sourceKey := "golden-" + fx.Name
			for _, turn := range fx.Turns {
				if turn.MockAnalysis != "" {
					llm.EnqueueStage("analysis", core.LLMResponse{Content: turn.MockAnalysis, PromptTokens: 100, CompletionTokens: 40, DurationMs: 10})
				}
				if turn.MockReply != "" {
					llm.EnqueueStage("response", core.LLMResponse{Content: turn.MockReply, PromptTokens: 120, CompletionTokens: 30, DurationMs: 12})
				}
				callsBefore := len(llm.Calls())
				sentBefore := len(ch.SentMessages())

				clock.Advance(2 * time.Second)
				msg := core.NormalizedMessage{
					TenantID: fx.TenantID, Channel: "simulator", SourceKey: sourceKey,
					MessageID: turn.MessageID, Timestamp: clock.Now(),
					Direction: core.DirectionInbound, Type: core.MessageTypeText, Text: turn.Text,
				}
				if err := p.ProcessBatch(ctx, []core.NormalizedMessage{msg}); err != nil {
					t.Fatalf("turn %s ProcessBatch failed: %v", turn.MessageID, err)
				}

				if turn.Expect.LLMCallsDelta != nil {
					gotDelta := len(llm.Calls()) - callsBefore
					if gotDelta != *turn.Expect.LLMCallsDelta {
						t.Fatalf("turn %s: expected %d LLM calls, got %d", turn.MessageID, *turn.Expect.LLMCallsDelta, gotDelta)
					}
				}

				state, _ := memStore.GetSourceState(ctx, fx.TenantID, "simulator", sourceKey)
				for k, v := range turn.Expect.Facts {
					if state.Facts[k] != v {
						t.Fatalf("turn %s: expected fact %s=%q, got %q (all=%+v)", turn.MessageID, k, v, state.Facts[k], state.Facts)
					}
				}
				if turn.Expect.SourceStatus != "" && string(state.Meta.Status) != turn.Expect.SourceStatus {
					t.Fatalf("turn %s: expected source status %q, got %q", turn.MessageID, turn.Expect.SourceStatus, state.Meta.Status)
				}

				tasks, _ := memStore.ListSourceTasks(ctx, fx.TenantID, "simulator", sourceKey)
				if len(tasks) < turn.Expect.TasksMin {
					t.Fatalf("turn %s: expected >= %d tasks, got %+v", turn.MessageID, turn.Expect.TasksMin, tasks)
				}
				if turn.Expect.TaskType != "" {
					found := false
					for _, tk := range tasks {
						if tk.Type == turn.Expect.TaskType {
							found = true
							break
						}
					}
					if !found {
						t.Fatalf("turn %s: expected task type %q in %+v", turn.MessageID, turn.Expect.TaskType, tasks)
					}
				}

				if turn.Expect.ReplyContains != "" {
					sent := ch.SentMessages()
					if len(sent) <= sentBefore || !strings.Contains(strings.ToLower(sent[len(sent)-1].Text), strings.ToLower(turn.Expect.ReplyContains)) {
						t.Fatalf("turn %s: expected outbound reply containing %q, got %+v", turn.MessageID, turn.Expect.ReplyContains, sent)
					}
				}

				if turn.Expect.Intent != "" || turn.Expect.DecisionAction != "" {
					runs, _ := memStore.ListRuns(ctx, fx.TenantID, "simulator", sourceKey, 1)
					if len(runs) == 0 || runs[0].TraceBlobRef == "" {
						t.Fatalf("turn %s: expected run trace", turn.MessageID)
					}
					rawTrace, _ := blobs.Get(ctx, runs[0].TraceBlobRef)
					var tr brain.RunTrace
					if err := json.Unmarshal(rawTrace, &tr); err != nil {
						t.Fatal(err)
					}
					if turn.Expect.Intent != "" && (tr.ParsedResult == nil || tr.ParsedResult.Intent != turn.Expect.Intent) {
						t.Fatalf("turn %s: expected intent %q, got %+v", turn.MessageID, turn.Expect.Intent, tr.ParsedResult)
					}
					if turn.Expect.DecisionAction != "" && (tr.Decision == nil || string(tr.Decision.Action) != turn.Expect.DecisionAction) {
						t.Fatalf("turn %s: expected decision %q, got %+v", turn.MessageID, turn.Expect.DecisionAction, tr.Decision)
					}
				}
			}
		})
	}
}
