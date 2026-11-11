package brain

import (
	"regexp"
	"sort"
	"strings"
	"time"

	"whatshelper/internal/core"
)

// FactChange registra uma alteração em um fato após análise.
type FactChange struct {
	Key      string `json:"key"`
	Action   string `json:"action"` // "added" | "updated" | "removed"
	OldValue string `json:"old_value,omitempty"`
	NewValue string `json:"new_value,omitempty"`
}

// MergeAndDiffFacts aplica novos fatos e remoções sobre o mapa existente sem mutar a entrada.
func MergeAndDiffFacts(existing map[string]string, incoming map[string]string, cleared []string) (map[string]string, []FactChange) {
	merged := make(map[string]string, len(existing)+len(incoming))
	for k, v := range existing {
		merged[k] = v
	}

	diffs := make([]FactChange, 0)

	for _, key := range cleared {
		if oldVal, ok := merged[key]; ok {
			delete(merged, key)
			diffs = append(diffs, FactChange{
				Key:      key,
				Action:   "removed",
				OldValue: oldVal,
			})
		}
	}

	for k, newVal := range incoming {
		oldVal, existed := merged[k]
		if !existed {
			merged[k] = newVal
			diffs = append(diffs, FactChange{
				Key:      k,
				Action:   "added",
				NewValue: newVal,
			})
			continue
		}
		if oldVal != newVal {
			merged[k] = newVal
			diffs = append(diffs, FactChange{
				Key:      k,
				Action:   "updated",
				OldValue: oldVal,
				NewValue: newVal,
			})
		}
	}

	sort.Slice(diffs, func(i, j int) bool {
		return diffs[i].Key < diffs[j].Key
	})
	return merged, diffs
}

// BuildConversationContext converte o mapa de contexto validado do pack na entidade de domínio.
func BuildConversationContext(sections map[string]map[string]string, updatedAt time.Time) core.ConversationContext {
	ctx := core.ConversationContext{
		UpdatedAt: updatedAt,
	}
	if len(sections) == 0 {
		return ctx
	}

	copied := make(map[string]map[string]string, len(sections))
	for secKey, fields := range sections {
		fCopy := make(map[string]string, len(fields))
		for fk, fv := range fields {
			fCopy[fk] = fv
		}
		copied[secKey] = fCopy
	}
	ctx.Sections = copied

	if lastAction, ok := copied["last_action"]; ok {
		ctx.LastActionType = lastAction["type"]
		ctx.LastActionBy = lastAction["actor"]
		ctx.Description = lastAction["description"]
	}
	return ctx
}

var (
	reCodeFence     = regexp.MustCompile("(?s)```[a-zA-Z0-9_-]*\\n?(.*?)```")
	reMarkdownLink  = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	reHeadingPrefix = regexp.MustCompile(`(?m)^#{1,6}\s+`)
	reBulletPrefix  = regexp.MustCompile(`(?m)^[ \t]*[-*•][ \t]+`)
	reNumberPrefix  = regexp.MustCompile(`(?m)^[ \t]*\d+\.[ \t]+`)
	reBoldDouble    = regexp.MustCompile(`\*\*(.*?)\*\*`)
	reUnderlineDbl  = regexp.MustCompile(`__(.*?)__`)
	reItalicStar    = regexp.MustCompile(`\*([^*\n]+)\*`)
	reInlineCode    = regexp.MustCompile("`([^`\n]+)`")
	reMultiNewlines = regexp.MustCompile(`\n{3,}`)
)

// SanitizePlainText remove formatação Markdown, listas e cercas para canais de texto puro.
func SanitizePlainText(input string) string {
	s := strings.TrimSpace(input)
	if s == "" {
		return ""
	}

	s = reCodeFence.ReplaceAllString(s, "$1")
	s = reMarkdownLink.ReplaceAllString(s, "$1 ($2)")
	s = reHeadingPrefix.ReplaceAllString(s, "")
	s = reBulletPrefix.ReplaceAllString(s, "")
	s = reNumberPrefix.ReplaceAllString(s, "")
	s = reBoldDouble.ReplaceAllString(s, "$1")
	s = reUnderlineDbl.ReplaceAllString(s, "$1")
	s = reItalicStar.ReplaceAllString(s, "$1")
	s = reInlineCode.ReplaceAllString(s, "$1")
	s = reMultiNewlines.ReplaceAllString(s, "\n\n")

	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
