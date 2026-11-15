package contextmod

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

const (
	// DefaultKnowledgeTopK define o número máximo padrão de seções relevantes retornadas na busca híbrida.
	DefaultKnowledgeTopK = 6
	// DefaultQAPassthroughMaxTokens define até quantos tokens o QA.md é mantido integralmente sem filtragem.
	DefaultQAPassthroughMaxTokens = 250
	// DefaultQAMaxEntries define quantos pares de QA.md relevantes são selecionados quando o arquivo cresce.
	DefaultQAMaxEntries = 5
)

var searchStopWords = map[string]bool{
	"a": true, "o": true, "as": true, "os": true,
	"um": true, "uma": true, "uns": true, "umas": true,
	"de": true, "do": true, "da": true, "dos": true, "das": true,
	"em": true, "no": true, "na": true, "nos": true, "nas": true,
	"e": true, "ou": true, "que": true, "se": true, "por": true,
	"para": true, "pra": true, "com": true, "sem": true, "ao": true, "aos": true,
	"qual": true, "quais": true, "como": true, "onde": true, "quando": true, "quem": true,
	"meu": true, "minha": true, "meus": true, "minhas": true, "seu": true, "sua": true,
	"este": true, "esta": true, "esse": true, "essa": true, "isso": true, "isto": true,
	"ser": true, "estar": true, "ter": true, "haver": true, "fazer": true,
	"estao": true, "sao": true, "foi": true, "era": true,
	"ola": true, "oi": true, "bom": true, "boa": true, "dia": true, "tarde": true, "noite": true,
	"obrigado": true, "obrigada": true, "porfavor": true, "favor": true,
	"the": true, "is": true, "are": true, "in": true, "on": true, "of": true, "to": true, "and": true, "for": true,
}

// KnowledgeChunk representa uma seção individual extraída de um arquivo Markdown.
type KnowledgeChunk struct {
	DocName      string `json:"doc_name"`
	DocTitle     string `json:"doc_title,omitempty"`
	SectionTitle string `json:"section_title,omitempty"`
	Content      string `json:"content"`
	Order        int    `json:"order"`
}

// KnowledgeSearcher define o contrato de busca sobre documentos de conhecimento (CTX-08).
type KnowledgeSearcher interface {
	Search(ctx context.Context, docs []KnowledgeDocView, query string, limit int) (*KnowledgeSearchResult, error)
}

// BM25Searcher implementa busca híbrida (BM25 + Grep exato/substring + stemming leve) em memória sobre documentos Markdown.
type BM25Searcher struct {
	K1 float64
	B  float64
}

// NewBM25Searcher cria um buscador híbrido BM25+Grep com parâmetros padrão (k1=1.2, b=0.75).
func NewBM25Searcher() *BM25Searcher {
	return &BM25Searcher{K1: 1.2, B: 0.75}
}

func (s *BM25Searcher) Search(ctx context.Context, docs []KnowledgeDocView, query string, limit int) (*KnowledgeSearchResult, error) {
	if limit <= 0 {
		limit = 5
	}
	res := &KnowledgeSearchResult{
		Version: ContractVersion,
		Query:   strings.TrimSpace(query),
		Hits:    []KnowledgeSearchHit{},
	}
	queryTokens := meaningfulQueryTokens(query)
	if len(queryTokens) == 0 || len(docs) == 0 {
		return res, nil
	}

	scored := scoreDocumentsHybrid(docs, query, queryTokens, s.effectiveK1(), s.effectiveB())
	hits := make([]KnowledgeSearchHit, 0, len(scored))
	for _, item := range scored {
		hits = append(hits, KnowledgeSearchHit{
			DocName: item.doc.Name,
			Score:   math.Round(item.score*1000) / 1000,
			Snippet: bestSnippet(item.doc.Content, queryTokens),
		})
	}
	if len(hits) > limit {
		hits = hits[:limit]
	}
	res.Hits = hits
	return res, nil
}

func (s *BM25Searcher) effectiveK1() float64 {
	if s != nil && s.K1 > 0 {
		return s.K1
	}
	return 1.2
}

func (s *BM25Searcher) effectiveB() float64 {
	if s != nil && s.B > 0 {
		return s.B
	}
	return 0.75
}

// SplitMarkdownSections divide um documento Markdown em seções por headings (## ou ###),
// preservando o título principal (#) e o nome do arquivo para validação de Grounding.
func SplitMarkdownSections(doc KnowledgeDocView) []KnowledgeChunk {
	trimmed := strings.TrimSpace(doc.Content)
	if trimmed == "" || isPlaceholderReadme(doc.Name, trimmed) {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(trimmed, "\r\n", "\n"), "\n")

	docTitle := ""
	var introLines []string
	type rawSection struct {
		title string
		lines []string
	}
	var sections []rawSection
	currentIdx := -1

	for _, line := range lines {
		clean := strings.TrimSpace(line)
		if clean == "---" {
			continue
		}
		if strings.HasPrefix(clean, "# ") && docTitle == "" && currentIdx == -1 {
			docTitle = strings.TrimSpace(strings.TrimPrefix(clean, "# "))
			continue
		}
		if strings.HasPrefix(clean, "## ") || strings.HasPrefix(clean, "### ") {
			title := strings.TrimSpace(strings.TrimLeft(clean, "#"))
			sections = append(sections, rawSection{
				title: title,
				lines: []string{line},
			})
			currentIdx = len(sections) - 1
			continue
		}
		if currentIdx == -1 {
			introLines = append(introLines, line)
		} else {
			sections[currentIdx].lines = append(sections[currentIdx].lines, line)
		}
	}

	introText := strings.TrimSpace(strings.Join(introLines, "\n"))
	if len(sections) == 0 {
		body := trimmed
		return []KnowledgeChunk{{
			DocName:      doc.Name,
			DocTitle:     docTitle,
			SectionTitle: docTitle,
			Content:      body,
			Order:        0,
		}}
	}

	chunks := make([]KnowledgeChunk, 0, len(sections))
	for i, sec := range sections {
		secBody := strings.TrimSpace(strings.Join(sec.lines, "\n"))
		if secBody == "" {
			continue
		}
		// Se a seção for apenas um cabeçalho vazio (ex.: "## 2. Passo a Passo" antes de "### Passo 1"),
		// anexa a introdução somente na primeira seção com conteúdo real.
		body := buildChunkBody(docTitle, introText, secBody, len(chunks) == 0)
		chunks = append(chunks, KnowledgeChunk{
			DocName:      doc.Name,
			DocTitle:     docTitle,
			SectionTitle: sec.title,
			Content:      body,
			Order:        i,
		})
	}
	return chunks
}

func buildChunkBody(docTitle, introText, secBody string, includeIntro bool) string {
	var b strings.Builder
	if docTitle != "" {
		b.WriteString("# ")
		b.WriteString(docTitle)
		b.WriteByte('\n')
	}
	if includeIntro && introText != "" {
		b.WriteString(introText)
		b.WriteByte('\n')
	}
	b.WriteString(secBody)
	return strings.TrimSpace(b.String())
}

func isPlaceholderReadme(name, content string) bool {
	if !strings.EqualFold(strings.TrimSpace(name), "README.md") {
		return false
	}
	folded := foldAccents(strings.ToLower(content))
	return len([]rune(content)) < 240 && strings.Contains(folded, "coloque aqui os arquivos")
}

// RetrieveRelevantKnowledge executa busca híbrida (BM25 + Grep) por seções Markdown,
// reagrupa as Top-K seções mais relevantes por documento preservando a ordem de relevância do documento,
// e retorna lista vazia caso nenhuma seção tenha match com a query.
func RetrieveRelevantKnowledge(docs []KnowledgeDocView, query string, topK int) []KnowledgeDocView {
	if len(docs) == 0 {
		return nil
	}
	if topK <= 0 {
		topK = DefaultKnowledgeTopK
	}
	queryTokens := meaningfulQueryTokens(query)
	if len(queryTokens) == 0 {
		return []KnowledgeDocView{}
	}

	var allChunks []KnowledgeChunk
	for _, doc := range docs {
		allChunks = append(allChunks, SplitMarkdownSections(doc)...)
	}
	if len(allChunks) == 0 {
		return []KnowledgeDocView{}
	}

	scored := scoreChunksHybrid(allChunks, query, queryTokens, 1.2, 0.75)
	if len(scored) == 0 {
		return []KnowledgeDocView{}
	}
	if len(scored) > topK {
		scored = scored[:topK]
	}

	return regroupChunksByDoc(scored)
}

type scoredChunk struct {
	chunk KnowledgeChunk
	score float64
}

type scoredDoc struct {
	doc   KnowledgeDocView
	score float64
}

func scoreDocumentsHybrid(docs []KnowledgeDocView, rawQuery string, queryTokens []string, k1, b float64) []scoredDoc {
	chunks := make([]KnowledgeChunk, 0, len(docs))
	docMap := make(map[string]KnowledgeDocView, len(docs))
	for i, d := range docs {
		docMap[d.Name] = d
		chunks = append(chunks, KnowledgeChunk{
			DocName:  d.Name,
			DocTitle: d.Name,
			Content:  d.Content,
			Order:    i,
		})
	}
	scoredChunks := scoreChunksHybrid(chunks, rawQuery, queryTokens, k1, b)
	out := make([]scoredDoc, 0, len(scoredChunks))
	for _, sc := range scoredChunks {
		out = append(out, scoredDoc{
			doc:   docMap[sc.chunk.DocName],
			score: sc.score,
		})
	}
	return out
}

func scoreChunksHybrid(chunks []KnowledgeChunk, rawQuery string, queryTokens []string, k1, b float64) []scoredChunk {
	type indexedChunk struct {
		chunk       KnowledgeChunk
		tokens      []string
		tf          map[string]int
		titleTF     map[string]int
		foldedText  string
		foldedTitle string
	}

	indexed := make([]indexedChunk, len(chunks))
	df := make(map[string]int)
	totalLen := 0

	for i, ch := range chunks {
		headerText := ch.DocName + " " + ch.DocTitle + " " + ch.SectionTitle
		fullText := headerText + " " + ch.Content
		toks := tokenizeAndStem(fullText)
		titleToks := tokenizeAndStem(headerText)

		tf := make(map[string]int, len(toks))
		seen := make(map[string]bool, len(toks))
		for _, tok := range toks {
			tf[tok]++
			if !seen[tok] {
				df[tok]++
				seen[tok] = true
			}
		}
		titleTF := make(map[string]int, len(titleToks))
		for _, tok := range titleToks {
			titleTF[tok]++
		}

		indexed[i] = indexedChunk{
			chunk:       ch,
			tokens:      toks,
			tf:          tf,
			titleTF:     titleTF,
			foldedText:  foldAccents(strings.ToLower(fullText)),
			foldedTitle: foldAccents(strings.ToLower(headerText)),
		}
		totalLen += len(toks)
	}

	avgLen := float64(totalLen) / float64(len(chunks))
	if avgLen <= 0 {
		avgLen = 1
	}
	nDocs := float64(len(chunks))
	bigrams := buildQueryBigrams(rawQuery)
	exactTerms := extractExactTechTerms(rawQuery)

	var results []scoredChunk
	for _, ic := range indexed {
		if len(ic.tokens) == 0 {
			continue
		}
		score := 0.0
		docLen := float64(len(ic.tokens))
		matchedTerms := 0

		for _, qt := range queryTokens {
			freq := float64(ic.tf[qt])
			if freq == 0 {
				continue
			}
			matchedTerms++
			docFreq := float64(df[qt])
			idf := math.Log(1.0 + (nDocs-docFreq+0.5)/(docFreq+0.5))
			tfNorm := (freq * (k1 + 1.0)) / (freq + k1*(1.0-b+b*(docLen/avgLen)))
			score += idf * tfNorm

			// Boost quando o termo aparece no nome do arquivo ou cabeçalho da seção
			if ic.titleTF[qt] > 0 {
				score += idf * 0.85
			}
		}

		// Boost Grep: match literal de bigramas da pergunta
		for _, bg := range bigrams {
			if strings.Contains(ic.foldedTitle, bg) {
				score += 2.0
			} else if strings.Contains(ic.foldedText, bg) {
				score += 1.0
			}
		}

		// Boost Grep: termos técnicos exatos (rotas #/..., códigos ERR-502, números, flags)
		for _, et := range exactTerms {
			if strings.Contains(ic.foldedText, et) {
				score += 2.5
			}
		}

		if score > 0 && matchedTerms > 0 {
			results = append(results, scoredChunk{
				chunk: ic.chunk,
				score: score,
			})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].score == results[j].score {
			if results[i].chunk.DocName == results[j].chunk.DocName {
				return results[i].chunk.Order < results[j].chunk.Order
			}
			return results[i].chunk.DocName < results[j].chunk.DocName
		}
		return results[i].score > results[j].score
	})
	return results
}

func regroupChunksByDoc(scored []scoredChunk) []KnowledgeDocView {
	type docGroup struct {
		docName  string
		docTitle string
		maxScore float64
		chunks   []KnowledgeChunk
	}
	groupsByName := make(map[string]*docGroup)
	var docOrder []string

	for _, sc := range scored {
		g, exists := groupsByName[sc.chunk.DocName]
		if !exists {
			g = &docGroup{
				docName:  sc.chunk.DocName,
				docTitle: sc.chunk.DocTitle,
				maxScore: sc.score,
			}
			groupsByName[sc.chunk.DocName] = g
			docOrder = append(docOrder, sc.chunk.DocName)
		} else if sc.score > g.maxScore {
			g.maxScore = sc.score
		}
		g.chunks = append(g.chunks, sc.chunk)
	}

	// Ordena os documentos pelo maior score de seção (mais relevante primeiro, menos relevante no fim para enforceBudget)
	sort.SliceStable(docOrder, func(i, j int) bool {
		gi := groupsByName[docOrder[i]]
		gj := groupsByName[docOrder[j]]
		if gi.maxScore == gj.maxScore {
			return gi.docName < gj.docName
		}
		return gi.maxScore > gj.maxScore
	})

	out := make([]KnowledgeDocView, 0, len(docOrder))
	for _, name := range docOrder {
		g := groupsByName[name]
		// Dentro do mesmo documento, preserva a ordem natural das seções para leitura fluida
		sort.Slice(g.chunks, func(i, j int) bool {
			return g.chunks[i].Order < g.chunks[j].Order
		})
		var sections []string
		titlePrefix := ""
		if g.docTitle != "" {
			titlePrefix = "# " + g.docTitle + "\n"
		}
		for idx, ch := range g.chunks {
			content := ch.Content
			if idx > 0 && titlePrefix != "" && strings.HasPrefix(content, titlePrefix) {
				content = strings.TrimSpace(strings.TrimPrefix(content, titlePrefix))
			}
			if content != "" {
				sections = append(sections, content)
			}
		}
		out = append(out, KnowledgeDocView{
			Name:    g.docName,
			Content: strings.Join(sections, "\n\n"),
		})
	}
	return out
}

// FilterRelevantQA mantém QA.md integral quando pequeno (<= passthroughMaxTokens) ou sem query,
// e filtra apenas os Top-K pares de pergunta/resposta relevantes quando o arquivo cresce.
func FilterRelevantQA(qaContent, query string, maxEntries, passthroughMaxTokens int) string {
	trimmed := strings.TrimSpace(qaContent)
	if trimmed == "" || strings.TrimSpace(query) == "" {
		return trimmed
	}
	if passthroughMaxTokens <= 0 {
		passthroughMaxTokens = DefaultQAPassthroughMaxTokens
	}
	if maxEntries <= 0 {
		maxEntries = DefaultQAMaxEntries
	}
	if EstimateTokens(trimmed) <= passthroughMaxTokens {
		return trimmed
	}

	blocks := splitQABlocks(trimmed)
	if len(blocks) <= maxEntries {
		return trimmed
	}

	queryTokens := meaningfulQueryTokens(query)
	if len(queryTokens) == 0 {
		return ""
	}

	chunks := make([]KnowledgeChunk, len(blocks))
	for i, blk := range blocks {
		chunks[i] = KnowledgeChunk{
			DocName:      fmt.Sprintf("qa-%d", i),
			SectionTitle: blk.question,
			Content:      blk.raw,
			Order:        i,
		}
	}

	scored := scoreChunksHybrid(chunks, query, queryTokens, 1.2, 0.75)
	if len(scored) == 0 {
		return ""
	}
	if len(scored) > maxEntries {
		scored = scored[:maxEntries]
	}

	selected := make([]string, 0, len(scored))
	for _, sc := range scored {
		selected = append(selected, strings.TrimSpace(sc.chunk.Content))
	}
	return strings.Join(selected, "\n\n")
}

type qaBlock struct {
	question string
	raw      string
}

func splitQABlocks(content string) []qaBlock {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	var blocks []qaBlock
	var currentQ string
	var currentLines []string

	flush := func() {
		raw := strings.TrimSpace(strings.Join(currentLines, "\n"))
		if currentQ != "" && raw != "" {
			blocks = append(blocks, qaBlock{question: currentQ, raw: raw})
		}
		currentQ = ""
		currentLines = nil
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "# "):
			continue
		case strings.HasPrefix(trimmed, "## "):
			flush()
			currentQ = strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))
			currentLines = []string{line}
		case strings.HasPrefix(trimmed, "P:") || strings.HasPrefix(trimmed, "p:"):
			flush()
			currentQ = strings.TrimSpace(trimmed[2:])
			currentLines = []string{line}
		default:
			if currentQ != "" {
				currentLines = append(currentLines, line)
			}
		}
	}
	flush()
	return blocks
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
		lineToks := tokenizeAndStem(trimmed)
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

func meaningfulQueryTokens(query string) []string {
	raw := tokenizeText(query)
	seen := make(map[string]bool, len(raw))
	out := make([]string, 0, len(raw))
	for _, tok := range raw {
		if len(tok) <= 1 && !isAllDigits(tok) {
			continue
		}
		if searchStopWords[tok] {
			continue
		}
		stemmed := stemToken(tok)
		if !seen[stemmed] {
			seen[stemmed] = true
			out = append(out, stemmed)
		}
	}
	return out
}

func tokenizeAndStem(s string) []string {
	raw := tokenizeText(s)
	out := make([]string, 0, len(raw))
	for _, tok := range raw {
		if searchStopWords[tok] {
			continue
		}
		stemmed := stemToken(tok)
		out = append(out, stemmed)
		for _, syn := range expandDomainSynonyms(tok, stemmed) {
			out = append(out, syn)
		}
	}
	return out
}

func expandDomainSynonyms(rawTok, stemmed string) []string {
	// Expressões de hora (ex.: 9h, 18h, 24h) e funcionamento indexam também como "horario" e "atendimento"
	if isHourToken(rawTok) || strings.HasPrefix(stemmed, "funcion") {
		return []string{"horario", "atendimento"}
	}
	return nil
}

func isHourToken(tok string) bool {
	if len(tok) < 2 || len(tok) > 4 || !strings.HasSuffix(tok, "h") {
		return false
	}
	return isAllDigits(tok[:len(tok)-1])
}

func stemToken(tok string) string {
	if len(tok) <= 3 || isAllDigits(tok) {
		return tok
	}
	// Redução determinística de sufixos comuns em PT-BR para aproximar variações morfológicas
	switch {
	case strings.HasSuffix(tok, "acoes") && len(tok) > 6:
		return tok[:len(tok)-5] + "acao"
	case strings.HasSuffix(tok, "coes") && len(tok) > 5:
		return tok[:len(tok)-4] + "cao"
	case strings.HasSuffix(tok, "mente") && len(tok) > 7:
		return tok[:len(tok)-5]
	case strings.HasSuffix(tok, "ando") && len(tok) > 6:
		return tok[:len(tok)-4]
	case strings.HasSuffix(tok, "endo") && len(tok) > 6:
		return tok[:len(tok)-4]
	case strings.HasSuffix(tok, "indo") && len(tok) > 6:
		return tok[:len(tok)-4]
	case strings.HasSuffix(tok, "aram") && len(tok) > 6:
		return tok[:len(tok)-4]
	case strings.HasSuffix(tok, "ores") && len(tok) > 6:
		return tok[:len(tok)-2]
	case strings.HasSuffix(tok, "res") && len(tok) > 5:
		return tok[:len(tok)-2]
	case strings.HasSuffix(tok, "ais") && len(tok) > 5:
		return tok[:len(tok)-2] + "l"
	case strings.HasSuffix(tok, "eis") && len(tok) > 5:
		return tok[:len(tok)-3] + "el"
	case strings.HasSuffix(tok, "es") && len(tok) > 4:
		return tok[:len(tok)-1]
	case strings.HasSuffix(tok, "s") && !strings.HasSuffix(tok, "ss") && len(tok) > 3:
		return tok[:len(tok)-1]
	}
	return tok
}

func buildQueryBigrams(query string) []string {
	raw := tokenizeText(query)
	filtered := make([]string, 0, len(raw))
	for _, t := range raw {
		if len(t) > 1 && !searchStopWords[t] {
			filtered = append(filtered, t)
		}
	}
	if len(filtered) < 2 {
		return nil
	}
	bigrams := make([]string, 0, len(filtered)-1)
	for i := 0; i < len(filtered)-1; i++ {
		bigrams = append(bigrams, filtered[i]+" "+filtered[i+1])
	}
	return bigrams
}

func extractExactTechTerms(query string) []string {
	folded := foldAccents(strings.ToLower(query))
	fields := strings.Fields(folded)
	var terms []string
	seen := make(map[string]bool)
	for _, f := range fields {
		clean := strings.Trim(f, `.,;:!?()"'[]{}`)
		if clean == "" {
			continue
		}
		if strings.Contains(clean, "#/") || strings.Contains(clean, "/") || strings.Contains(clean, ":") || strings.Contains(clean, "-") || hasDigit(clean) {
			if !seen[clean] {
				seen[clean] = true
				terms = append(terms, clean)
			}
		}
	}
	return terms
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func hasDigit(s string) bool {
	for _, r := range s {
		if unicode.IsDigit(r) {
			return true
		}
	}
	return false
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
