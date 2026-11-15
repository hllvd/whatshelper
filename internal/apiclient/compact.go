package apiclient

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

const (
	// DefaultMaxArrayItems define o limite padrão de itens mantidos em listas retornadas pela API.
	DefaultMaxArrayItems = 5
	// DefaultCompactPageSize define o pageSize injetado automaticamente em endpoints paginados.
	DefaultCompactPageSize = 5
	// DefaultPlannerTopEndpoints define quantos endpoints mais relevantes vão para o prompt do LLM planner.
	DefaultPlannerTopEndpoints = 10
	// maxCompactBodyChars limita o tamanho final formatado de cada resposta de API no prompt.
	maxCompactBodyChars = 3200
)

var (
	uuidPattern = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)

	noisyJSONKeys = map[string]bool{
		"passwordhash":       true,
		"securitystamp":      true,
		"concurrencystamp":   true,
		"normalizedusername": true,
		"normalizedemail":    true,
		"avatarbase64":       true,
		"photo":              true,
		"stacktrace":         true,
	}

	apiStopWords = map[string]bool{
		"a": true, "o": true, "as": true, "os": true,
		"um": true, "uma": true, "de": true, "do": true, "da": true, "dos": true, "das": true,
		"em": true, "no": true, "na": true, "nos": true, "nas": true,
		"e": true, "ou": true, "que": true, "se": true, "por": true, "para": true, "com": true,
		"qual": true, "quais": true, "como": true, "onde": true, "quando": true,
		"meu": true, "minha": true, "meus": true, "minhas": true,
		"api": true, "get": true, "listar": true, "consultar": true, "buscar": true,
	}
)

// EnsureCompactQueryParams injeta automaticamente pageSize/limit enxuto em chamadas GET
// cujo endpoint no OpenAPI suporta paginação e a URL ainda não define limite.
func EnsureCompactQueryParams(rawPath string, endpoints []EndpointDef, defaultPageSize int) string {
	if defaultPageSize <= 0 {
		defaultPageSize = DefaultCompactPageSize
	}
	trimmed := strings.TrimSpace(rawPath)
	u, err := url.ParseRequestURI(trimmed)
	if err != nil || u.Path == "" {
		return trimmed
	}
	ep := matchEndpointDef(u.Path, endpoints)
	if ep == nil {
		return trimmed
	}

	q := u.Query()
	for _, p := range ep.Parameters {
		if p.In != "query" {
			continue
		}
		lower := strings.ToLower(p.Name)
		if lower == "pagesize" || lower == "page_size" || lower == "limit" || lower == "perpage" || lower == "per_page" {
			if q.Get(p.Name) == "" {
				q.Set(p.Name, strconv.Itoa(defaultPageSize))
			} else if cur, convErr := strconv.Atoi(q.Get(p.Name)); convErr == nil && cur > defaultPageSize*2 {
				q.Set(p.Name, strconv.Itoa(defaultPageSize))
			}
		}
	}

	encoded := q.Encode()
	if encoded == "" {
		return u.Path
	}
	return u.Path + "?" + encoded
}

func matchEndpointDef(reqPath string, endpoints []EndpointDef) *EndpointDef {
	reqSegs := strings.Split(strings.Trim(reqPath, "/"), "/")
	for i := range endpoints {
		ep := &endpoints[i]
		epSegs := strings.Split(strings.Trim(ep.Path, "/"), "/")
		if len(reqSegs) != len(epSegs) {
			continue
		}
		match := true
		for j := range epSegs {
			if strings.HasPrefix(epSegs[j], "{") && strings.HasSuffix(epSegs[j], "}") {
				if strings.TrimSpace(reqSegs[j]) == "" {
					match = false
					break
				}
				continue
			}
			if epSegs[j] != reqSegs[j] {
				match = false
				break
			}
		}
		if match {
			return ep
		}
	}
	return nil
}

// SelectRelevantEndpoints filtra e ranqueia os endpoints GET do OpenAPI usando Grep/BM25 sobre a conversa,
// descartando rotas que exigem UUID/ID no path quando não há identificador correspondente na consulta.
func SelectRelevantEndpoints(endpoints []EndpointDef, query string, maxEndpoints int) []EndpointDef {
	if len(endpoints) == 0 {
		return nil
	}
	if maxEndpoints <= 0 {
		maxEndpoints = DefaultPlannerTopEndpoints
	}

	hasUUID := uuidPattern.MatchString(query)
	hasNumber := shortNumPattern.MatchString(query)
	queryTokens := tokenizeAPIQuery(query)
	foldedQuery := foldText(query)

	type scoredEP struct {
		ep    EndpointDef
		score float64
	}

	var candidates []scoredEP
	for _, ep := range endpoints {
		if requiresMissingPathID(ep.Path, hasUUID, hasNumber) {
			continue
		}
		score := scoreEndpoint(ep, queryTokens, foldedQuery)
		candidates = append(candidates, scoredEP{ep: ep, score: score})
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			return candidates[i].ep.Path < candidates[j].ep.Path
		}
		return candidates[i].score > candidates[j].score
	})

	if len(candidates) > maxEndpoints {
		candidates = candidates[:maxEndpoints]
	}
	out := make([]EndpointDef, len(candidates))
	for i, c := range candidates {
		out[i] = c.ep
	}
	return out
}

func requiresMissingPathID(path string, hasUUID, hasNumber bool) bool {
	matches := pathParamPattern.FindAllStringSubmatch(path, -1)
	if len(matches) == 0 {
		return false
	}
	for _, m := range matches {
		param := strings.ToLower(m[1])
		switch {
		case strings.Contains(param, "uuid") || param == "userid" || param == "teamid" || param == "leaderid":
			if !hasUUID {
				return true
			}
		case param == "id" || param == "contractnumber" || param == "matricula":
			if !hasNumber && !hasUUID {
				return true
			}
		}
	}
	return false
}

func scoreEndpoint(ep EndpointDef, queryTokens []string, foldedQuery string) float64 {
	var paramNames []string
	for _, p := range ep.Parameters {
		paramNames = append(paramNames, p.Name, p.Description)
	}
	epText := foldText(ep.Path + " " + ep.Summary + " " + ep.Description + " " + strings.Join(paramNames, " "))
	epTokens := tokenizeAPIQuery(epText)
	epSet := make(map[string]int, len(epTokens))
	for _, t := range epTokens {
		epSet[t]++
	}

	score := 0.0
	for _, qt := range queryTokens {
		if freq := epSet[qt]; freq > 0 {
			score += 2.0 + float64(freq)*0.5
		} else if strings.Contains(epText, qt) {
			score += 1.2
		}
	}

	// Boost para endpoints de busca/listagem geral quando não há path param
	if !pathParamPattern.MatchString(ep.Path) {
		score += 0.3
	}
	for _, p := range ep.Parameters {
		if strings.EqualFold(p.Name, "search") || strings.EqualFold(p.Name, "contractNumber") {
			score += 0.4
		}
	}
	if strings.Contains(foldedQuery, "excluido") || strings.Contains(foldedQuery, "deletado") {
		if strings.Contains(ep.Path, "deleted") {
			score += 4.0
		}
	}
	return score
}

// CompactJSONBody poda valores vazios/ruído de um JSON de resposta HTTP, filtra arrays longos
// mantendo apenas os Top-N itens relevantes para a query e formata em Markdown Key-Value enxuto.
func CompactJSONBody(rawBody string, query string, maxItems int) string {
	trimmed := strings.TrimSpace(rawBody)
	if trimmed == "" {
		return "(vazio)"
	}
	if maxItems <= 0 {
		maxItems = DefaultMaxArrayItems
	}

	var parsed any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return truncateChars(trimmed, maxCompactBodyChars)
	}

	queryTokens := tokenizeAPIQuery(query)
	pruned := pruneAndCapValue(parsed, queryTokens, maxItems)
	if pruned == nil {
		return "(sem dados relevantes)"
	}

	formatted := formatCompactValue(pruned, 0)
	return truncateChars(strings.TrimSpace(formatted), maxCompactBodyChars)
}

func pruneAndCapValue(v any, queryTokens []string, maxItems int) any {
	switch val := v.(type) {
	case nil:
		return nil
	case string:
		s := strings.TrimSpace(val)
		if s == "" {
			return nil
		}
		return s
	case bool, float64:
		return val
	case []any:
		cleaned := make([]any, 0, len(val))
		for _, item := range val {
			if p := pruneAndCapValue(item, queryTokens, maxItems); p != nil {
				cleaned = append(cleaned, p)
			}
		}
		if len(cleaned) == 0 {
			return nil
		}
		return capAndRankSlice(cleaned, queryTokens, maxItems)
	case map[string]any:
		// Desembrulha envelopes redundantes como {"success": true, "data": ...} mantendo apenas o conteúdo útil
		out := make(map[string]any, len(val))
		for k, sub := range val {
			lowerK := strings.ToLower(strings.TrimSpace(k))
			if noisyJSONKeys[lowerK] {
				continue
			}
			p := pruneAndCapValue(sub, queryTokens, maxItems)
			if p == nil {
				continue
			}
			out[k] = p
		}
		if len(out) == 0 {
			return nil
		}
		return out
	default:
		return val
	}
}

func capAndRankSlice(items []any, queryTokens []string, maxItems int) any {
	total := len(items)
	if total <= maxItems {
		return items
	}

	type rankedItem struct {
		idx   int
		item  any
		score float64
	}
	ranked := make([]rankedItem, total)
	for i, it := range items {
		ranked[i] = rankedItem{
			idx:   i,
			item:  it,
			score: scoreJSONItem(it, queryTokens),
		}
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score == ranked[j].score {
			return ranked[i].idx < ranked[j].idx
		}
		return ranked[i].score > ranked[j].score
	})

	selected := make([]any, 0, maxItems+1)
	for i := 0; i < maxItems; i++ {
		selected = append(selected, ranked[i].item)
	}
	selected = append(selected, fmt.Sprintf("[total_items: %d | showing_top: %d]", total, maxItems))
	return selected
}

func scoreJSONItem(item any, queryTokens []string) float64 {
	if len(queryTokens) == 0 {
		return 0
	}
	raw, err := json.Marshal(item)
	if err != nil {
		return 0
	}
	folded := foldText(string(raw))
	score := 0.0
	for _, qt := range queryTokens {
		if strings.Contains(folded, qt) {
			score += 1.0
			if hasDigitToken(qt) {
				score += 2.5
			}
		}
	}
	return score
}

func formatCompactValue(v any, depth int) string {
	switch val := v.(type) {
	case nil:
		return ""
	case string:
		return val
	case bool:
		if val {
			return "true"
		}
		return "false"
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64)
	case []any:
		var lines []string
		for idx, item := range val {
			if s, ok := item.(string); ok && strings.HasPrefix(s, "[total_items:") {
				lines = append(lines, s)
				continue
			}
			if m, ok := item.(map[string]any); ok {
				lines = append(lines, fmt.Sprintf("%d. %s", idx+1, formatInlineMap(m)))
			} else {
				lines = append(lines, fmt.Sprintf("- %s", formatCompactValue(item, depth+1)))
			}
		}
		return strings.Join(lines, "\n")
	case map[string]any:
		// Se o mapa possui apenas "success" + "data" (envelope padrão da API), promove "data"
		if dataVal, hasData := unwrapEnvelopeMap(val); hasData {
			return formatCompactValue(dataVal, depth)
		}
		if depth > 0 {
			return formatInlineMap(val)
		}
		keys := sortedMapKeys(val)
		var lines []string
		for _, k := range keys {
			sub := val[k]
			switch subVal := sub.(type) {
			case []any:
				lines = append(lines, fmt.Sprintf("%s:\n%s", k, formatCompactValue(subVal, depth+1)))
			case map[string]any:
				lines = append(lines, fmt.Sprintf("%s: %s", k, formatInlineMap(subVal)))
			default:
				lines = append(lines, fmt.Sprintf("%s: %s", k, formatCompactValue(subVal, depth+1)))
			}
		}
		return strings.Join(lines, "\n")
	default:
		return fmt.Sprintf("%v", val)
	}
}

func unwrapEnvelopeMap(m map[string]any) (any, bool) {
	dataVal, ok := m["data"]
	if !ok {
		return nil, false
	}
	// Se houver paginação no envelope (totalCount, totalItems, totalPages), preserva metadado enxuto junto aos itens
	for k := range m {
		lower := strings.ToLower(k)
		if lower != "data" && lower != "success" && lower != "error" && lower != "message" {
			return nil, false
		}
	}
	if msg, hasMsg := m["message"].(string); hasMsg && strings.TrimSpace(msg) != "" {
		return map[string]any{
			"message": msg,
			"data":    dataVal,
		}, true
	}
	return dataVal, true
}

func formatInlineMap(m map[string]any) string {
	keys := sortedMapKeys(m)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		sub := m[k]
		switch subVal := sub.(type) {
		case map[string]any:
			parts = append(parts, fmt.Sprintf("%s: {%s}", k, formatInlineMap(subVal)))
		case []any:
			var subItems []string
			for _, item := range subVal {
				if subMap, ok := item.(map[string]any); ok {
					subItems = append(subItems, "{"+formatInlineMap(subMap)+"}")
				} else {
					subItems = append(subItems, formatCompactValue(item, 2))
				}
			}
			parts = append(parts, fmt.Sprintf("%s: [%s]", k, strings.Join(subItems, "; ")))
		default:
			parts = append(parts, fmt.Sprintf("%s: %s", k, formatCompactValue(subVal, 2)))
		}
	}
	return strings.Join(parts, " | ")
}

func sortedMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		pi := keyPriority(keys[i])
		pj := keyPriority(keys[j])
		if pi != pj {
			return pi < pj
		}
		return keys[i] < keys[j]
	})
	return keys
}

func keyPriority(k string) int {
	switch strings.ToLower(k) {
	case "id", "contractnumber", "number", "code", "matricula":
		return 1
	case "name", "username", "clientname", "teamname", "title":
		return 2
	case "status", "isactive", "isowner", "role":
		return 3
	case "totalvalue", "value", "amount":
		return 4
	default:
		return 5
	}
}

func tokenizeAPIQuery(s string) []string {
	folded := foldText(s)
	raw := strings.FieldsFunc(folded, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	seen := make(map[string]bool, len(raw))
	out := make([]string, 0, len(raw))
	for _, tok := range raw {
		if len(tok) <= 1 && !hasDigitToken(tok) {
			continue
		}
		if apiStopWords[tok] {
			continue
		}
		stemmed := stemAPIToken(tok)
		if !seen[stemmed] {
			seen[stemmed] = true
			out = append(out, stemmed)
		}
	}
	return out
}

func stemAPIToken(tok string) string {
	if len(tok) <= 3 || hasDigitToken(tok) {
		return tok
	}
	if strings.HasSuffix(tok, "s") && !strings.HasSuffix(tok, "ss") {
		return tok[:len(tok)-1]
	}
	return tok
}

func foldText(s string) string {
	t := norm.NFD.String(strings.ToLower(s))
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

func hasDigitToken(s string) bool {
	for _, r := range s {
		if unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func truncateChars(s string, maxChars int) string {
	runes := []rune(s)
	if len(runes) <= maxChars {
		return s
	}
	return string(runes[:maxChars]) + "\n...(truncado para economia de tokens)"
}
