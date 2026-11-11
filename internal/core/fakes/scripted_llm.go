package fakes

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
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

// NewOfflineDemoLLM cria um FakeLLM determinístico e agnóstico de pack para uso offline (LLM_PROVIDER=fake).
func NewOfflineDemoLLM() *ScriptedLLM {
	return NewScriptedLLM(func(ctx context.Context, req core.LLMRequest) (*core.LLMResponse, error) {
		switch req.Stage {
		case "analysis":
			hints := extractSchemaHints(req.System)
			facts := map[string]any{}
			cleanPrompt := timestampPrefix.ReplaceAllString(req.Prompt, "")
			if idx := strings.Index(cleanPrompt, "NEW MESSAGE:"); idx != -1 {
				cleanPrompt = cleanPrompt[idx:]
			}
			if num := numberPattern.FindString(cleanPrompt); num != "" && hints.firstNumericFact != "" {
				facts[hints.firstNumericFact] = num
			}

			signals := map[string]bool{}
			if hints.allowedSignals["has_new_question"] {
				signals["has_new_question"] = strings.Contains(req.Prompt, "?")
			}
			if hints.allowedSignals["customer_engaged"] {
				signals["customer_engaged"] = true
			}

			payload := map[string]any{
				"summary":     "Cliente enviou mensagem no simulador e aguarda atendimento.",
				"last_sender": hints.customerActor,
				"facts":       facts,
				"signals":     signals,
				"context":     map[string]any{},
			}
			if hints.hasLastAction {
				actType := "other"
				if strings.Contains(req.Prompt, "?") && hints.lastActionTypes["question"] {
					actType = "question"
				} else if hints.defaultLastActionType != "" {
					actType = hints.defaultLastActionType
				}
				la := map[string]any{
					"type":  actType,
					"actor": hints.customerActor,
				}
				if hints.hasLastActionDesc {
					la["description"] = "Mensagem recebida pelo simulador"
				}
				payload["context"] = map[string]any{"last_action": la}
			}
			if hints.defaultIntent != "" {
				payload["intent"] = hints.defaultIntent
			}

			raw, _ := json.Marshal(payload)
			return &core.LLMResponse{
				Content:          string(raw),
				PromptTokens:     120,
				CompletionTokens: 65,
				DurationMs:       18,
			}, nil

		case "response":
			if strings.Contains(req.System, `"answerable"`) {
				payload := map[string]any{
					"answerable": true,
					"sources":    extractDocNames(req.System),
					"reply":      "Olá! Recebi sua mensagem no modo offline de demonstração. Como posso te ajudar com as informações disponíveis?",
				}
				raw, _ := json.Marshal(payload)
				return &core.LLMResponse{
					Content:          string(raw),
					PromptTokens:     180,
					CompletionTokens: 32,
					DurationMs:       22,
				}, nil
			}
			return &core.LLMResponse{
				Content:          "Olá! Recebi sua mensagem no modo offline de demonstração. Como posso te ajudar com as informações disponíveis?",
				PromptTokens:     180,
				CompletionTokens: 28,
				DurationMs:       22,
			}, nil

		case "learning":
			q := extractPromptLine(req.Prompt, "PERGUNTA DO CLIENTE:")
			a := extractPromptLine(req.Prompt, "CORREÇÃO DO DONO:")
			if q == "" {
				q = "Dúvida geral do cliente"
			}
			if a == "" {
				a = "Resposta corrigida pelo operador."
			}
			payload := map[string]any{
				"question":        q,
				"answer":          a,
				"action":          "add",
				"target_question": "",
			}
			raw, _ := json.Marshal(payload)
			return &core.LLMResponse{
				Content:          string(raw),
				PromptTokens:     90,
				CompletionTokens: 35,
				DurationMs:       15,
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

type schemaHints struct {
	customerActor         string
	firstNumericFact      string
	allowedSignals        map[string]bool
	defaultIntent         string
	hasLastAction         bool
	hasLastActionDesc     bool
	lastActionTypes       map[string]bool
	defaultLastActionType string
}

func extractSchemaHints(systemPrompt string) schemaHints {
	hints := schemaHints{
		customerActor:   "customer",
		allowedSignals:  make(map[string]bool),
		lastActionTypes: make(map[string]bool),
	}

	start := strings.Index(systemPrompt, `"$schema"`)
	if start == -1 {
		return hints
	}
	braceStart := strings.LastIndex(systemPrompt[:start], "{")
	if braceStart == -1 {
		return hints
	}

	var schema map[string]any
	dec := json.NewDecoder(strings.NewReader(systemPrompt[braceStart:]))
	if err := dec.Decode(&schema); err != nil {
		return hints
	}

	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		return hints
	}

	if ls, ok := props["last_sender"].(map[string]any); ok {
		if enum, ok := ls["enum"].([]any); ok && len(enum) >= 2 {
			if s, ok := enum[1].(string); ok && s != "" {
				hints.customerActor = s
			}
		}
	}

	if intentProp, ok := props["intent"].(map[string]any); ok {
		if enum, ok := intentProp["enum"].([]any); ok && len(enum) > 0 {
			if s, ok := enum[0].(string); ok {
				hints.defaultIntent = s
			}
		}
	}

	if factsObj, ok := props["facts"].(map[string]any); ok {
		if fProps, ok := factsObj["properties"].(map[string]any); ok {
			keys := make([]string, 0, len(fProps))
			for k := range fProps {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				def, _ := fProps[k].(map[string]any)
				if isNumericSchemaType(def["type"]) {
					hints.firstNumericFact = k
					break
				}
			}
		}
	}

	if sigObj, ok := props["signals"].(map[string]any); ok {
		if sProps, ok := sigObj["properties"].(map[string]any); ok {
			for k := range sProps {
				hints.allowedSignals[k] = true
			}
		}
	}

	if ctxObj, ok := props["context"].(map[string]any); ok {
		if cProps, ok := ctxObj["properties"].(map[string]any); ok {
			if la, ok := cProps["last_action"].(map[string]any); ok {
				hints.hasLastAction = true
				if laProps, ok := la["properties"].(map[string]any); ok {
					if _, ok := laProps["description"]; ok {
						hints.hasLastActionDesc = true
					}
					if tProp, ok := laProps["type"].(map[string]any); ok {
						if enum, ok := tProp["enum"].([]any); ok {
							for _, v := range enum {
								if s, ok := v.(string); ok {
									hints.lastActionTypes[s] = true
									if hints.defaultLastActionType == "" {
										hints.defaultLastActionType = s
									}
								}
							}
						}
					}
				}
			}
		}
	}

	return hints
}

func isNumericSchemaType(t any) bool {
	switch v := t.(type) {
	case string:
		return v == "number" || v == "integer"
	case []any:
		for _, elem := range v {
			if s, ok := elem.(string); ok && (s == "number" || s == "integer") {
				return true
			}
		}
	}
	return false
}

func extractDocNames(systemPrompt string) []string {
	var docs []string
	for _, line := range strings.Split(systemPrompt, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "### ") {
			name := strings.TrimSpace(strings.TrimPrefix(trimmed, "### "))
			if name != "" {
				docs = append(docs, name)
				break
			}
		}
	}
	return docs
}

func extractPromptLine(prompt, prefix string) string {
	for _, line := range strings.Split(prompt, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
		}
	}
	return ""
}
