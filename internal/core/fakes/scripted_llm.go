package fakes

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"whatshelper/internal/core"
)

// ScriptedLLM responde de forma determinística por estágio ou sequência de turnos.
type ScriptedLLM struct {
	mu       sync.Mutex
	calls    []core.LLMRequest
	byStage  map[string][]core.LLMResponse
	stageIdx map[string]int
	fallback FakeLLMHandler
}

func NewScriptedLLM(fallback FakeLLMHandler) *ScriptedLLM {
	return &ScriptedLLM{
		byStage:  make(map[string][]core.LLMResponse),
		stageIdx: make(map[string]int),
		fallback: fallback,
	}
}

// EnqueueStage adiciona respostas em ordem FIFO para um estágio específico ("analysis", "analysis_repair", "response", "nudge").
func (s *ScriptedLLM) EnqueueStage(stage string, responses ...core.LLMResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byStage[stage] = append(s.byStage[stage], responses...)
}

func (s *ScriptedLLM) Generate(ctx context.Context, req core.LLMRequest) (*core.LLMResponse, error) {
	s.mu.Lock()
	s.calls = append(s.calls, req)
	queue := s.byStage[req.Stage]
	idx := s.stageIdx[req.Stage]
	if idx < len(queue) {
		resp := queue[idx]
		s.stageIdx[req.Stage] = idx + 1
		s.mu.Unlock()
		return &resp, nil
	}
	fb := s.fallback
	s.mu.Unlock()

	if fb != nil {
		return fb(ctx, req)
	}
	return nil, fmt.Errorf("scripted llm: nenhuma resposta configurada para o estágio %q", req.Stage)
}

func (s *ScriptedLLM) Calls() []core.LLMRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := make([]core.LLMRequest, len(s.calls))
	copy(copied, s.calls)
	return copied
}

var (
	timestampPrefix = regexp.MustCompile(`\[\d{4}-\d{2}-\d{2}T[^\]]+\]\s*`)
	numberPattern   = regexp.MustCompile(`\b\d{3,7}\b`)
)

// NewOfflineDemoLLM cria um FakeLLM determinístico para uso no modo offline do simulador (LLM_PROVIDER=fake).
func NewOfflineDemoLLM() *ScriptedLLM {
	return NewScriptedLLM(func(ctx context.Context, req core.LLMRequest) (*core.LLMResponse, error) {
		switch req.Stage {
		case "analysis":
			facts := map[string]any{}
			cleanPrompt := timestampPrefix.ReplaceAllString(req.Prompt, "")
			if idx := strings.Index(cleanPrompt, "NEW MESSAGE:"); idx != -1 {
				cleanPrompt = cleanPrompt[idx:]
			}
			if num := numberPattern.FindString(cleanPrompt); num != "" {
				// Extrai número apenas se o schema do prompt possuir chave Budget
				if strings.Contains(req.System, `"Budget"`) {
					facts["Budget"] = num
				}
			}
			// Descobre o actor customer a partir do schema ou usa "customer"
			customerActor := "customer"
			payload := map[string]any{
				"summary":     "Cliente enviou mensagem no simulador e aguarda atendimento.",
				"last_sender": customerActor,
				"facts":       facts,
				"signals": map[string]bool{
					"has_new_question": strings.Contains(req.Prompt, "?"),
					"customer_engaged": true,
				},
				"context": map[string]any{
					"last_action": map[string]any{
						"type":        "other",
						"actor":       customerActor,
						"description": "Mensagem recebida pelo simulador",
					},
				},
			}
			if strings.Contains(req.Prompt, "?") {
				payload["context"].(map[string]any)["last_action"].(map[string]any)["type"] = "question"
			}
			raw, _ := json.Marshal(payload)
			return &core.LLMResponse{
				Content:          string(raw),
				PromptTokens:     120,
				CompletionTokens: 65,
				DurationMs:       18,
			}, nil

		case "response":
			return &core.LLMResponse{
				Content:          "Olá! Recebi sua mensagem no modo offline de demonstração. Como posso te ajudar com as informações disponíveis?",
				PromptTokens:     180,
				CompletionTokens: 28,
				DurationMs:       22,
			}, nil

		default:
			return &core.LLMResponse{
				Content:          "Olá! Tudo bem? Passando para saber se ainda posso ajudar.",
				PromptTokens:     80,
				CompletionTokens: 16,
				DurationMs:       12,
			}, nil
		}
	})
}
