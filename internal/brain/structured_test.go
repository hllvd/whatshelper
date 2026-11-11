package brain_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"whatshelper/internal/brain"
	"whatshelper/internal/core"
	"whatshelper/internal/core/fakes"
	"whatshelper/internal/pack"
)

const testPackYAML = `
name: generic
language: pt-BR
actors:
  business: agent
  customer: customer
facts:
  - key: Budget
    type: number
    description: Orçamento máximo
signals:
  - key: has_new_question
    description: Nova pergunta
context:
  - key: last_action
    fields:
      - key: type
        type: enum
        values: [question, other]
prompts:
  analysis: prompts/analysis.tmpl
  reply: prompts/reply.tmpl
`

func loadTestPack(t *testing.T) *pack.Pack {
	t.Helper()
	p, err := pack.ParsePackYAML([]byte(testPackYAML))
	if err != nil {
		t.Fatalf("ParsePackYAML failed: %v", err)
	}
	return p
}

func TestExtractJSON(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{
			name: "plain json",
			raw:  `{"summary":"ok"}`,
			want: `{"summary":"ok"}`,
		},
		{
			name: "fenced json",
			raw:  "```json\n{\"summary\":\"ok\"}\n```",
			want: `{"summary":"ok"}`,
		},
		{
			name: "prose around json with braces inside string",
			raw:  "Aqui está o JSON:\n{\"summary\":\"valor {entre chaves}\"}\nFim.",
			want: `{"summary":"valor {entre chaves}"}`,
		},
		{
			name:    "no json",
			raw:     "desculpe, não entendi",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := brain.ExtractJSON(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %s", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(got) != tc.want {
				t.Fatalf("want %s, got %s", tc.want, string(got))
			}
		})
	}
}

func TestGenerateStructuredAnalysis_DirectSuccess(t *testing.T) {
	p := loadTestPack(t)
	llm := fakes.NewScriptedLLM(nil)
	llm.EnqueueStage("analysis", core.LLMResponse{
		Content:          "```json\n{\"summary\":\"Cliente informou orçamento.\",\"last_sender\":\"customer\",\"facts\":{\"Budget\":2500},\"signals\":{\"has_new_question\":false},\"context\":{}}\n```",
		PromptTokens:     100,
		CompletionTokens: 40,
		DurationMs:       15,
	})

	outcome, err := brain.GenerateStructuredAnalysis(context.Background(), llm, "test-model", p, pack.RenderedPrompt{
		System: "sys",
		User:   "usr",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != "ok" {
		t.Fatalf("expected status ok, got %s", outcome.Status)
	}
	if outcome.Result.Facts["Budget"] != "2500" {
		t.Fatalf("expected Budget 2500, got %q", outcome.Result.Facts["Budget"])
	}
	if len(llm.Calls()) != 1 {
		t.Fatalf("expected 1 LLM call, got %d", len(llm.Calls()))
	}
}

func TestGenerateStructuredAnalysis_SuccessAfterRepair(t *testing.T) {
	p := loadTestPack(t)
	llm := fakes.NewScriptedLLM(nil)
	llm.EnqueueStage("analysis", core.LLMResponse{
		Content:          `{"summary":"Cliente informou orçamento.","last_sender":"customer","facts":{"Budget":"500 mil"},"signals":{},"context":{}}`,
		PromptTokens:     100,
		CompletionTokens: 30,
		DurationMs:       10,
	})
	llm.EnqueueStage("analysis_repair", core.LLMResponse{
		Content:          `{"summary":"Cliente informou orçamento.","last_sender":"customer","facts":{"Budget":500000},"signals":{"has_new_question":false},"context":{}}`,
		PromptTokens:     140,
		CompletionTokens: 35,
		DurationMs:       12,
	})

	outcome, err := brain.GenerateStructuredAnalysis(context.Background(), llm, "test-model", p, pack.RenderedPrompt{
		System: "sys",
		User:   "usr",
	})
	if err != nil {
		t.Fatalf("unexpected error after repair: %v", err)
	}
	if outcome.Status != "repaired" {
		t.Fatalf("expected status repaired, got %s", outcome.Status)
	}
	if outcome.Result.Facts["Budget"] != "500000" {
		t.Fatalf("expected repaired Budget 500000, got %q", outcome.Result.Facts["Budget"])
	}
	if !strings.Contains(outcome.RepairPrompt, "facts.Budget") {
		t.Fatalf("expected repair prompt to mention facts.Budget error")
	}
	if outcome.PromptTokens != 240 || outcome.CompletionTokens != 65 {
		t.Fatalf("expected summed tokens 240/65, got %d/%d", outcome.PromptTokens, outcome.CompletionTokens)
	}
}

func TestGenerateStructuredAnalysis_DoubleFailureFallback(t *testing.T) {
	p := loadTestPack(t)
	llm := fakes.NewScriptedLLM(nil)
	llm.EnqueueStage("analysis", core.LLMResponse{
		Content: `{"summary":"ok","last_sender":"customer","facts":{"InvalidKey":"x"}}`,
	})
	llm.EnqueueStage("analysis_repair", core.LLMResponse{
		Content: `não consegui gerar json válido`,
	})

	outcome, err := brain.GenerateStructuredAnalysis(context.Background(), llm, "test-model", p, pack.RenderedPrompt{
		System: "sys",
		User:   "usr",
	})
	if !errors.Is(err, brain.ErrAnalysisFallback) {
		t.Fatalf("expected ErrAnalysisFallback, got %v", err)
	}
	if outcome.Status != "fallback" {
		t.Fatalf("expected status fallback, got %s", outcome.Status)
	}
	if outcome.Result != nil {
		t.Fatalf("expected nil result on fallback")
	}
	if len(outcome.FinalErrors) == 0 {
		t.Fatalf("expected final validation errors recorded")
	}
}

func TestGenerateStructuredAnalysis_ContextTimeout(t *testing.T) {
	p := loadTestPack(t)
	llm := fakes.NewScriptedLLM(func(ctx context.Context, req core.LLMRequest) (*core.LLMResponse, error) {
		return nil, context.DeadlineExceeded
	})

	outcome, err := brain.GenerateStructuredAnalysis(context.Background(), llm, "test-model", p, pack.RenderedPrompt{
		System: "sys",
		User:   "usr",
	})
	if !errors.Is(err, brain.ErrAnalysisFallback) {
		t.Fatalf("expected ErrAnalysisFallback on timeout, got %v", err)
	}
	if outcome.Status != "fallback" {
		t.Fatalf("expected fallback status, got %s", outcome.Status)
	}
}
