package contextmod

import (
	"context"
	"math"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// KnowledgeSearcher define o contrato de busca sobre documentos de conhecimento (CTX-08).
type KnowledgeSearcher interface {
	Search(ctx context.Context, docs []KnowledgeDocView, query string, limit int) (*KnowledgeSearchResult, error)
}

// BM25Searcher implementa busca léxica BM25 em memória sobre documentos Markdown (ponto de extensão futuro).
type BM25Searcher struct {
	K1 float64
	B  float64
}

// NewBM25Searcher cria um buscador BM25 com parâmetros padrão (k1=1.2, b=0.75).
func NewBM25Searcher() *BM25Searcher {
	return &BM25Searcher{K1: 1.2, B: 0.75}
}

func (s *BM25Searcher) Search(ctx context.Context, docs []KnowledgeDocView, query string, limit int) (*KnowledgeSearchResult, error) {
	if limit <= 0 {
		limit = 5
	}
	queryTokens := tokenizeText(query)
	res := &KnowledgeSearchResult{
		Version: ContractVersion,
		Query:   strings.TrimSpace(query),
		Hits:    []KnowledgeSearchHit{},
	}
	if len(queryTokens) == 0 || len(docs) == 0 {
		return res, nil
	}

	type tokenizedDoc struct {
		doc    KnowledgeDocView
		tokens []string
		tf     map[string]int
	}

	parsed := make([]tokenizedDoc, len(docs))
	df := make(map[string]int)
	totalLen := 0

	for i, d := range docs {
		toks := tokenizeText(d.Name + " " + d.Content)
		tf := make(map[string]int, len(toks))
		seenInDoc := make(map[string]bool)
		for _, tok := range toks {
			tf[tok]++
			if !seenInDoc[tok] {
				df[tok]++
				seenInDoc[tok] = true
			}
		}
		parsed[i] = tokenizedDoc{doc: d, tokens: toks, tf: tf}
		totalLen += len(toks)
	}

	avgLen := float64(totalLen) / float64(len(docs))
	if avgLen == 0 {
		avgLen = 1
	}
	nDocs := float64(len(docs))
	k1 := s.K1
	if k1 <= 0 {
		k1 = 1.2
	}
	b := s.B
	if b <= 0 {
		b = 0.75
	}

	hits := make([]KnowledgeSearchHit, 0, len(docs))
	for _, pd := range parsed {
		if len(pd.tokens) == 0 {
			continue
		}
		score := 0.0
		docLen := float64(len(pd.tokens))
		for _, qt := range queryTokens {
			freq := float64(pd.tf[qt])
			if freq == 0 {
				continue
			}
			docFreq := float64(df[qt])
			idf := math.Log(1.0 + (nDocs-docFreq+0.5)/(docFreq+0.5))
			tfNorm := (freq * (k1 + 1.0)) / (freq + k1*(1.0-b+b*(docLen/avgLen)))
			score += idf * tfNorm
		}
		if score > 0 {
			hits = append(hits, KnowledgeSearchHit{
				DocName: pd.doc.Name,
				Score:   math.Round(score*1000) / 1000,
				Snippet: bestSnippet(pd.doc.Content, queryTokens),
			})
		}
	}

	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score == hits[j].Score {
			return hits[i].DocName < hits[j].DocName
		}
		return hits[i].Score > hits[j].Score
	})

	if len(hits) > limit {
		hits = hits[:limit]
	}
	res.Hits = hits
	return res, nil
}

func bestSnippet(content string, queryTokens []string) string {
	lines := strings.Split(content, "\n")
	bestLine := ""
	bestMatches := -1
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		lineToks := tokenizeText(trimmed)
		matches := 0
		for _, qt := range queryTokens {
			for _, lt := range lineToks {
				if lt == qt {
					matches++
					break
				}
			}
		}
		if matches > bestMatches {
			bestMatches = matches
			bestLine = trimmed
		}
	}
	if len([]rune(bestLine)) > 220 {
		r := []rune(bestLine)
		return string(r[:220]) + "..."
	}
	return bestLine
}

func tokenizeText(s string) []string {
	folded := foldAccents(strings.ToLower(s))
	return strings.FieldsFunc(folded, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

func foldAccents(s string) string {
	t := norm.NFD.String(s)
	var b strings.Builder
	b.Grow(len(t))
	for _, r := range t {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
