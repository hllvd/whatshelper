package brain

import (
	"fmt"
	"strings"
	"unicode"
)

// QAEntry representa um par pergunta/resposta consolidado em QA.md.
type QAEntry struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

// QAMergeOutcome indica se a entrada foi adicionada, mesclada em uma existente ou já existia igual.
type QAMergeOutcome string

const (
	QAMergeAdded     QAMergeOutcome = "added"
	QAMergeMerged    QAMergeOutcome = "merged"
	QAMergeUnchanged QAMergeOutcome = "unchanged"
)

var qaStopWords = map[string]bool{
	"a": true, "o": true, "as": true, "os": true,
	"um": true, "uma": true, "uns": true, "umas": true,
	"de": true, "do": true, "da": true, "dos": true, "das": true,
	"em": true, "no": true, "na": true, "nos": true, "nas": true,
	"e": true, "ou": true, "que": true, "se": true, "por": true,
	"para": true, "com": true, "ao": true, "aos": true,
	"qual": true, "quais": true, "como": true, "onde": true, "quando": true,
	"the": true, "is": true, "are": true, "in": true, "on": true, "of": true, "to": true, "and": true,
}

// ParseQA interpreta QA.md aceitando tanto blocos "## Pergunta\nResposta" quanto "P: ...\nR: ...".
func ParseQA(content string) []QAEntry {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	var entries []QAEntry
	var currentQ string
	var answerLines []string

	flush := func() {
		q := strings.TrimSpace(currentQ)
		a := strings.TrimSpace(strings.Join(answerLines, "\n"))
		if q != "" && a != "" {
			entries = append(entries, QAEntry{Question: q, Answer: a})
		}
		currentQ = ""
		answerLines = nil
	}

	for _, rawLine := range lines {
		trimmed := strings.TrimSpace(rawLine)
		switch {
		case strings.HasPrefix(trimmed, "## "):
			flush()
			currentQ = strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))
		case strings.HasPrefix(trimmed, "# "):
			// Título principal do documento (# Perguntas e Respostas...)
			continue
		case strings.HasPrefix(trimmed, "P:") || strings.HasPrefix(trimmed, "p:"):
			flush()
			currentQ = strings.TrimSpace(trimmed[2:])
		case strings.HasPrefix(trimmed, "R:") || strings.HasPrefix(trimmed, "r:"):
			if currentQ != "" {
				ans := strings.TrimSpace(trimmed[2:])
				if ans != "" {
					answerLines = append(answerLines, ans)
				}
			}
		default:
			if currentQ != "" {
				answerLines = append(answerLines, rawLine)
			}
		}
	}
	flush()
	return entries
}

// FormatQA serializa entradas de QA.md de forma estável, legível e idempotente.
func FormatQA(entries []QAEntry) string {
	var b strings.Builder
	b.WriteString("# Perguntas e Respostas Aprendidas (QA)\n")
	for _, e := range entries {
		q := strings.TrimSpace(e.Question)
		a := strings.TrimSpace(e.Answer)
		if q == "" || a == "" {
			continue
		}
		fmt.Fprintf(&b, "\n## %s\n%s\n", q, a)
	}
	return b.String()
}

// MergeQAEntry adiciona ou atualiza entrada equivalente sem mutar o slice de entrada.
func MergeQAEntry(existing []QAEntry, incoming QAEntry, targetQuestion string) ([]QAEntry, QAMergeOutcome, int) {
	inQ := strings.TrimSpace(incoming.Question)
	inA := strings.TrimSpace(incoming.Answer)
	out := make([]QAEntry, len(existing))
	copy(out, existing)
	if inQ == "" || inA == "" {
		return out, QAMergeUnchanged, -1
	}

	bestIdx := findMatchingQAIndex(out, inQ, targetQuestion)
	if bestIdx >= 0 {
		if normalizeForCompare(out[bestIdx].Answer) == normalizeForCompare(inA) &&
			normalizeForCompare(out[bestIdx].Question) == normalizeForCompare(inQ) {
			return out, QAMergeUnchanged, bestIdx
		}
		out[bestIdx] = QAEntry{
			Question: inQ,
			Answer:   inA,
		}
		return out, QAMergeMerged, bestIdx
	}

	out = append(out, QAEntry{Question: inQ, Answer: inA})
	return out, QAMergeAdded, len(out) - 1
}

func findMatchingQAIndex(entries []QAEntry, question, targetQuestion string) int {
	normIn := normalizeForCompare(question)
	normTarget := normalizeForCompare(targetQuestion)

	if normTarget != "" {
		for i, e := range entries {
			if normalizeForCompare(e.Question) == normTarget {
				return i
			}
		}
	}

	bestIdx := -1
	bestScore := 0.0
	for i, e := range entries {
		normExisting := normalizeForCompare(e.Question)
		if normExisting == normIn {
			return i
		}
		score := tokenJaccard(normIn, normExisting)
		if normTarget != "" {
			if tScore := tokenJaccard(normTarget, normExisting); tScore > score {
				score = tScore
			}
		}
		if score >= 0.60 && score > bestScore {
			bestScore = score
			bestIdx = i
		}
	}
	return bestIdx
}

func tokenJaccard(a, b string) float64 {
	setA := meaningfulTokens(a)
	setB := meaningfulTokens(b)
	if len(setA) == 0 || len(setB) == 0 {
		return 0
	}
	intersection := 0
	for tok := range setA {
		if setB[tok] {
			intersection++
		}
	}
	union := len(setA) + len(setB) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

func meaningfulTokens(norm string) map[string]bool {
	out := make(map[string]bool)
	for _, tok := range strings.Fields(norm) {
		if len(tok) <= 1 || qaStopWords[tok] {
			continue
		}
		out[tok] = true
	}
	return out
}

func normalizeForCompare(s string) string {
	lower := strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	b.Grow(len(lower))
	lastSpace := true
	for _, r := range lower {
		folded := foldLatinRune(r)
		if unicode.IsLetter(folded) || unicode.IsDigit(folded) {
			b.WriteRune(folded)
			lastSpace = false
		} else if !lastSpace {
			b.WriteByte(' ')
			lastSpace = true
		}
	}
	return strings.TrimSpace(b.String())
}

func foldLatinRune(r rune) rune {
	switch r {
	case 'á', 'à', 'â', 'ã', 'ä', 'å':
		return 'a'
	case 'é', 'è', 'ê', 'ë':
		return 'e'
	case 'í', 'ì', 'î', 'ï':
		return 'i'
	case 'ó', 'ò', 'ô', 'õ', 'ö':
		return 'o'
	case 'ú', 'ù', 'û', 'ü':
		return 'u'
	case 'ç':
		return 'c'
	case 'ñ':
		return 'n'
	default:
		return r
	}
}
