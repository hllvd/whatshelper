package contextmod

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// RawContextData contém os dados já coletados pelos providers antes da filtragem e truncamento.
type RawContextData struct {
	Scope             Scope
	SourceVersion     int64
	SourceStatus      string
	SourceJourneyStage string
	Summary           *SummaryView
	Facts             map[string]string
	Signals           map[string]bool
	StructuredContext *StructuredContextView
	RecentMessages    []MessageView
	Knowledge         []KnowledgeDocView
	QAContent         string
	Personality       string
	DegradedProviders []string
	GeneratedAt       time.Time
}

// EstimateTokens estima tokens de forma determinística (~4 caracteres por token).
func EstimateTokens(text string) int {
	n := utf8.RuneCountInString(text)
	if n == 0 {
		return 0
	}
	return (n + 3) / 4
}

// BuildContextBundle aplica allowlists por view, executa truncamento determinístico por prioridade
// (fatos e resumo são preservados antes de mensagens antigas e documentos excedentes) e monta blocos ordenados.
func BuildContextBundle(spec ViewSpec, raw RawContextData) *ContextBundle {
	bundle := &ContextBundle{
		Version:           ContractVersion,
		Scope:             raw.Scope,
		View:              spec.Name,
		SourceVersion:      raw.SourceVersion,
		SourceStatus:       raw.SourceStatus,
		SourceJourneyStage: raw.SourceJourneyStage,
		DegradedProviders:  append([]string(nil), raw.DegradedProviders...),
		GeneratedAt:       raw.GeneratedAt,
	}

	hasBlock := make(map[string]bool, len(spec.Blocks))
	for _, b := range spec.Blocks {
		hasBlock[b] = true
	}

	if hasBlock["summary"] && raw.Summary != nil {
		s := *raw.Summary
		bundle.Summary = &s
	}
	if hasBlock["facts"] && len(raw.Facts) > 0 {
		bundle.Facts = filterStringMap(raw.Facts, spec.ApplyAllowlist, spec.AllowedFacts)
	}
	if hasBlock["signals"] && len(raw.Signals) > 0 {
		bundle.Signals = filterBoolMap(raw.Signals, spec.ApplyAllowlist, spec.AllowedSignals)
	}
	if hasBlock["structured_context"] && raw.StructuredContext != nil {
		bundle.StructuredContext = filterStructuredContext(raw.StructuredContext, spec.ApplyAllowlist, spec.AllowedSections)
	}
	if hasBlock["recent_messages"] && spec.IncludeMessages && len(raw.RecentMessages) > 0 {
		msgs := make([]MessageView, len(raw.RecentMessages))
		for i, m := range raw.RecentMessages {
			if spec.DelimitUntrustedMsg {
				m.Text = DelimitUntrustedMessage(m.Text)
			}
			msgs[i] = m
		}
		bundle.RecentMessages = msgs
	}
	if hasBlock["knowledge"] && len(raw.Knowledge) > 0 {
		docs := make([]KnowledgeDocView, len(raw.Knowledge))
		copy(docs, raw.Knowledge)
		bundle.Knowledge = docs
	}
	if hasBlock["qa"] && strings.TrimSpace(raw.QAContent) != "" {
		bundle.QAContent = raw.QAContent
	}
	if strings.TrimSpace(raw.Personality) != "" {
		bundle.Personality = raw.Personality
	}

	trunc := enforceBudget(bundle, spec.MaxTokens)
	bundle.Truncation = trunc
	bundle.Blocks = renderBlocks(bundle, spec)
	return bundle
}

func enforceBudget(b *ContextBundle, budget int) TruncationReport {
	report := TruncationReport{BudgetTokens: budget}
	if budget <= 0 {
		report.UsedTokens = measureBundleTokens(b)
		return report
	}

	var dropped []DroppedItem
	used := measureBundleTokens(b)

	// Prioridade de corte:
	// 1) Documentos de conhecimento do fim para o começo
	for used > budget && len(b.Knowledge) > 0 {
		lastIdx := len(b.Knowledge) - 1
		doc := b.Knowledge[lastIdx]
		saved := EstimateTokens(doc.Name + "\n" + doc.Content)
		b.Knowledge = b.Knowledge[:lastIdx]
		dropped = append(dropped, DroppedItem{
			Block:       "knowledge",
			ItemID:      doc.Name,
			Reason:      "budget_exceeded",
			TokensSaved: saved,
		})
		used = measureBundleTokens(b)
	}

	// 2) Mensagens antigas primeiro (preservando as mais recentes)
	for used > budget && len(b.RecentMessages) > 0 {
		oldest := b.RecentMessages[0]
		saved := EstimateTokens(oldest.Sender + ": " + oldest.Text)
		b.RecentMessages = b.RecentMessages[1:]
		dropped = append(dropped, DroppedItem{
			Block:       "recent_messages",
			ItemID:      oldest.MessageID,
			Reason:      "budget_exceeded",
			TokensSaved: saved,
		})
		used = measureBundleTokens(b)
	}

	// 3) QA.md somente se ainda exceder orçamento
	if used > budget && strings.TrimSpace(b.QAContent) != "" {
		saved := EstimateTokens(b.QAContent)
		b.QAContent = ""
		dropped = append(dropped, DroppedItem{
			Block:       "qa",
			ItemID:      "QA.md",
			Reason:      "budget_exceeded",
			TokensSaved: saved,
		})
		used = measureBundleTokens(b)
	}

	// 4) Contexto estruturado e sinais antes de fatos/resumo
	if used > budget && b.StructuredContext != nil {
		saved := EstimateTokens(formatStructuredContext(b.StructuredContext))
		b.StructuredContext = nil
		dropped = append(dropped, DroppedItem{
			Block:       "structured_context",
			ItemID:      "context",
			Reason:      "budget_exceeded",
			TokensSaved: saved,
		})
		used = measureBundleTokens(b)
	}
	if used > budget && len(b.Signals) > 0 {
		saved := EstimateTokens(formatBoolMap(b.Signals))
		b.Signals = nil
		dropped = append(dropped, DroppedItem{
			Block:       "signals",
			ItemID:      "signals",
			Reason:      "budget_exceeded",
			TokensSaved: saved,
		})
		used = measureBundleTokens(b)
	}

	report.UsedTokens = used
	report.Truncated = len(dropped) > 0
	report.DroppedItems = dropped
	return report
}

func measureBundleTokens(b *ContextBundle) int {
	total := 0
	if b.Summary != nil {
		total += EstimateTokens(b.Summary.Text)
	}
	if len(b.Facts) > 0 {
		total += EstimateTokens(formatStringMap(b.Facts))
	}
	if len(b.Signals) > 0 {
		total += EstimateTokens(formatBoolMap(b.Signals))
	}
	if b.StructuredContext != nil {
		total += EstimateTokens(formatStructuredContext(b.StructuredContext))
	}
	for _, m := range b.RecentMessages {
		total += EstimateTokens(m.Sender + ": " + m.Text)
	}
	for _, doc := range b.Knowledge {
		total += EstimateTokens(doc.Name + "\n" + doc.Content)
	}
	if b.QAContent != "" {
		total += EstimateTokens(b.QAContent)
	}
	return total
}

func renderBlocks(b *ContextBundle, spec ViewSpec) []ContextBlock {
	blocks := make([]ContextBlock, 0, len(spec.Blocks))
	for _, kind := range spec.Blocks {
		var content string
		switch kind {
		case "summary":
			if b.Summary != nil && strings.TrimSpace(b.Summary.Text) != "" {
				content = b.Summary.Text
			}
		case "facts":
			if len(b.Facts) > 0 {
				content = formatStringMap(b.Facts)
			}
		case "signals":
			if len(b.Signals) > 0 {
				content = formatBoolMap(b.Signals)
			}
		case "structured_context":
			if b.StructuredContext != nil {
				content = formatStructuredContext(b.StructuredContext)
			}
		case "recent_messages":
			if len(b.RecentMessages) > 0 {
				var lines []string
				for _, m := range b.RecentMessages {
					lines = append(lines, fmt.Sprintf("[%s] %s: %s", m.Timestamp.UTC().Format(time.RFC3339), m.Sender, m.Text))
				}
				content = strings.Join(lines, "\n")
			}
		case "knowledge":
			if len(b.Knowledge) > 0 {
				var docs []string
				for _, d := range b.Knowledge {
					docs = append(docs, fmt.Sprintf("## %s\n%s", d.Name, d.Content))
				}
				content = strings.Join(docs, "\n\n")
			}
		case "qa":
			if strings.TrimSpace(b.QAContent) != "" {
				content = b.QAContent
			}
		}
		if strings.TrimSpace(content) == "" {
			continue
		}
		blocks = append(blocks, ContextBlock{
			Kind:          kind,
			Format:        spec.Format,
			Content:       content,
			TokenEstimate: EstimateTokens(content),
		})
	}
	return blocks
}

func filterStringMap(src map[string]string, apply bool, allowed map[string]bool) map[string]string {
	out := make(map[string]string)
	for k, v := range src {
		if apply && !allowed[k] {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func filterBoolMap(src map[string]bool, apply bool, allowed map[string]bool) map[string]bool {
	out := make(map[string]bool)
	for k, v := range src {
		if apply && !allowed[k] {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func filterStructuredContext(src *StructuredContextView, apply bool, allowed map[string]bool) *StructuredContextView {
	if src == nil {
		return nil
	}
	out := &StructuredContextView{
		Version:   ContractVersion,
		Scope:     src.Scope,
		UpdatedAt: src.UpdatedAt,
	}
	if len(src.Sections) > 0 {
		secs := make(map[string]map[string]string)
		for sk, fields := range src.Sections {
			if apply && !allowed[sk] {
				continue
			}
			fCopy := make(map[string]string, len(fields))
			for fk, fv := range fields {
				fCopy[fk] = fv
			}
			secs[sk] = fCopy
		}
		if len(secs) > 0 {
			out.Sections = secs
		}
	}
	if !apply || allowed["last_action"] {
		out.LastActionType = src.LastActionType
		out.LastActionBy = src.LastActionBy
		out.Description = src.Description
	}
	if len(out.Sections) == 0 && out.LastActionType == "" && out.Description == "" {
		return nil
	}
	return out
}

func formatStringMap(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, len(keys))
	for i, k := range keys {
		lines[i] = fmt.Sprintf("- %s: %s", k, m[k])
	}
	return strings.Join(lines, "\n")
}

func formatBoolMap(m map[string]bool) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, len(keys))
	for i, k := range keys {
		lines[i] = fmt.Sprintf("- %s: %t", k, m[k])
	}
	return strings.Join(lines, "\n")
}

func formatStructuredContext(sc *StructuredContextView) string {
	if sc == nil {
		return ""
	}
	raw, err := json.Marshal(sc)
	if err != nil {
		return ""
	}
	return string(raw)
}
