package brain

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"whatshelper/internal/core"
	"whatshelper/internal/pack"
)

// PreGateInput reúne os dados necessários para decidir antes de chamar qualquer LLM.
type PreGateInput struct {
	Policy     core.TenantPolicy
	State      *core.SourceState
	Pack       *pack.Pack
	BatchTexts []string
}

// PreGateResult descreve se o pipeline pode prosseguir para os estágios cognitivos.
type PreGateResult struct {
	Allowed      bool
	Action       DecisionAction
	Reason       string
	UpdateStatus core.SourceStatus
}

// EvaluatePreLLMGates executa verificações puras e determinísticas antes de gastar tokens de LLM.
func EvaluatePreLLMGates(in PreGateInput) PreGateResult {
	if !in.Policy.BotEnabled {
		return PreGateResult{
			Allowed: false,
			Action:  DecisionSkip,
			Reason:  "bot desabilitado para este tenant (bot_enabled=false)",
		}
	}

	if in.State != nil {
		switch in.State.Meta.Status {
		case core.SourceStatusPaused, core.SourceStatusHuman, core.SourceStatusOptedOut, core.SourceStatusArchived:
			return PreGateResult{
				Allowed: false,
				Action:  DecisionSkip,
				Reason:  fmt.Sprintf("fonte bloqueada (status=%s)", in.State.Meta.Status),
			}
		}
	}

	if in.Pack != nil && len(in.Pack.OptOutKeywords) > 0 {
		for _, text := range in.BatchTexts {
			if MatchesOptOutKeyword(text, in.Pack.OptOutKeywords) {
				return PreGateResult{
					Allowed:      false,
					Action:       DecisionOptOut,
					Reason:       "opt-out detectado por palavra-chave",
					UpdateStatus: core.SourceStatusOptedOut,
				}
			}
		}
	}

	return PreGateResult{Allowed: true}
}

// MatchesOptOutKeyword verifica se o texto contém alguma frase/palavra de opt-out respeitando fronteiras de palavra.
func MatchesOptOutKeyword(text string, keywords []string) bool {
	normText := normalizeSpacesLower(text)
	if normText == "" {
		return false
	}
	for _, kw := range keywords {
		normKW := normalizeSpacesLower(kw)
		if normKW == "" {
			continue
		}
		if containsWordPhrase(normText, normKW) {
			return true
		}
	}
	return false
}

func normalizeSpacesLower(s string) string {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(s)))
	return strings.Join(fields, " ")
}

func containsWordPhrase(haystack, needle string) bool {
	offset := 0
	for {
		idx := strings.Index(haystack[offset:], needle)
		if idx < 0 {
			return false
		}
		start := offset + idx
		end := start + len(needle)

		leftOK := start == 0 || !isWordChar(rune(haystack[start-1]))
		rightOK := end == len(haystack) || !isWordChar(rune(haystack[end]))
		if leftOK && rightOK {
			return true
		}
		offset = start + 1
	}
}

func isWordChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

// UpdateSourceStatus atualiza o status no META da fonte preservando ContactID/Version/CreatedAt.
func UpdateSourceStatus(ctx context.Context, st core.Store, now time.Time, tenant, channel, sourceKey string, status core.SourceStatus) error {
	state, err := st.GetSourceState(ctx, tenant, channel, sourceKey)
	if err != nil {
		return err
	}
	meta := state.Meta
	meta.TenantID = tenant
	meta.Channel = channel
	meta.SourceKey = sourceKey
	meta.Status = status
	if meta.CreatedAt.IsZero() {
		meta.CreatedAt = now.UTC()
	}
	meta.UpdatedAt = now.UTC()
	return st.SaveSourceMeta(ctx, meta)
}
