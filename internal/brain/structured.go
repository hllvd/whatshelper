package brain

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"whatshelper/internal/core"
	"whatshelper/internal/pack"
)

var (
	ErrNoJSONObject      = errors.New("nenhum objeto JSON encontrado na resposta do LLM")
	ErrAnalysisFallback  = errors.New("análise falhou após tentativa de reparo; fallback seguro ativado")
)

// StructuredAnalysisOutcome captura toda a observabilidade da etapa de análise (incluindo tentativas de repair).
type StructuredAnalysisOutcome struct {
	Result           *pack.AnalysisResult   `json:"result,omitempty"`
	RawOutput        string                 `json:"raw_output"`
	RepairPrompt     string                 `json:"repair_prompt,omitempty"`
	RepairRawOutput  string                 `json:"repair_raw_output,omitempty"`
	InitialErrors    []pack.ValidationError `json:"initial_errors,omitempty"`
	FinalErrors      []pack.ValidationError `json:"final_errors,omitempty"`
	Status           string                 `json:"status"` // "ok" | "repaired" | "fallback"
	PromptTokens     int                    `json:"prompt_tokens"`
	CompletionTokens int                    `json:"completion_tokens"`
	DurationMs       int64                  `json:"duration_ms"`
}

// ExtractJSON extrai um objeto JSON de uma string que pode conter cercas Markdown ou texto auxiliar.
func ExtractJSON(raw string) ([]byte, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, ErrNoJSONObject
	}

	// Remove cercas ```json ou ``` se envolverem o bloco
	if strings.HasPrefix(trimmed, "```") {
		lines := strings.Split(trimmed, "\n")
		if len(lines) >= 2 {
			lines = lines[1:]
			if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "```" {
				lines = lines[:len(lines)-1]
			}
			trimmed = strings.TrimSpace(strings.Join(lines, "\n"))
		}
	}

	start := strings.IndexByte(trimmed, '{')
	if start == -1 {
		return nil, ErrNoJSONObject
	}

	depth := 0
	inString := false
	escaped := false

	for i := start; i < len(trimmed); i++ {
		ch := trimmed[i]
		if escaped {
			escaped = false
			continue
		}
		if inString {
			if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return []byte(trimmed[start : i+1]), nil
			}
		}
	}

	return nil, ErrNoJSONObject
}

// GenerateStructuredAnalysis executa a chamada de análise, valida contra o pack e faz até 1 retry de repair.
func GenerateStructuredAnalysis(
	ctx context.Context,
	llm core.LLM,
	model string,
	p *pack.Pack,
	rendered pack.RenderedPrompt,
) (*StructuredAnalysisOutcome, error) {
	return GenerateStructuredAnalysisWithProvider(ctx, llm, "", model, p, rendered)
}

// GenerateStructuredAnalysisWithProvider executa a chamada de análise especificando também o provedor.
func GenerateStructuredAnalysisWithProvider(
	ctx context.Context,
	llm core.LLM,
	provider string,
	model string,
	p *pack.Pack,
	rendered pack.RenderedPrompt,
) (*StructuredAnalysisOutcome, error) {
	outcome := &StructuredAnalysisOutcome{
		Status: "fallback",
	}

	resp1, err := llm.Generate(ctx, core.LLMRequest{
		Stage:       "analysis",
		Provider:    provider,
		Model:       model,
		System:      rendered.System,
		Prompt:      rendered.User,
		Temperature: 0,
		MaxTokens:   1024,
	})
	if err != nil {
		return outcome, fmt.Errorf("%w: chamada inicial falhou: %v", ErrAnalysisFallback, err)
	}

	outcome.RawOutput = resp1.Content
	outcome.PromptTokens += resp1.PromptTokens
	outcome.CompletionTokens += resp1.CompletionTokens
	outcome.DurationMs += resp1.DurationMs

	result, valErrs := extractAndValidate(p, resp1.Content)
	if len(valErrs) == 0 {
		outcome.Result = result
		outcome.Status = "ok"
		return outcome, nil
	}

	outcome.InitialErrors = valErrs

	// Tentativa única de repair enviando os erros de validação
	repairPrompt := buildRepairPrompt(resp1.Content, valErrs)
	outcome.RepairPrompt = repairPrompt

	resp2, err := llm.Generate(ctx, core.LLMRequest{
		Stage:       "analysis_repair",
		Provider:    provider,
		Model:       model,
		System:      rendered.System,
		Prompt:      repairPrompt,
		Temperature: 0,
		MaxTokens:   1024,
	})
	if err != nil {
		outcome.FinalErrors = valErrs
		return outcome, fmt.Errorf("%w: chamada de reparo falhou: %v", ErrAnalysisFallback, err)
	}

	outcome.RepairRawOutput = resp2.Content
	outcome.PromptTokens += resp2.PromptTokens
	outcome.CompletionTokens += resp2.CompletionTokens
	outcome.DurationMs += resp2.DurationMs

	result2, valErrs2 := extractAndValidate(p, resp2.Content)
	if len(valErrs2) == 0 {
		outcome.Result = result2
		outcome.Status = "repaired"
		return outcome, nil
	}

	outcome.FinalErrors = valErrs2
	return outcome, fmt.Errorf("%w: erros persistentes após reparo (%d)", ErrAnalysisFallback, len(valErrs2))
}

func extractAndValidate(p *pack.Pack, raw string) (*pack.AnalysisResult, []pack.ValidationError) {
	extracted, err := ExtractJSON(raw)
	if err != nil {
		return nil, []pack.ValidationError{{Field: "$", Message: err.Error()}}
	}
	return pack.ValidateAnalysis(p, extracted)
}

func buildRepairPrompt(invalidRaw string, errs []pack.ValidationError) string {
	var b strings.Builder
	b.WriteString("A sua resposta JSON anterior falhou na validação contra o schema do pack.\n")
	b.WriteString("Corrija os erros abaixo e retorne APENAS o objeto JSON válido:\n\n")
	b.WriteString("ERROS:\n")
	for _, e := range errs {
		b.WriteString("- ")
		b.WriteString(e.Error())
		b.WriteByte('\n')
	}
	b.WriteString("\nRESPOSTA ANTERIOR:\n")
	b.WriteString(invalidRaw)
	return b.String()
}
