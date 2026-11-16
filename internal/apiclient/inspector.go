package apiclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"whatshelper/internal/core"
)

const (
	// LiveAPIDocName é o nome do documento injetado no KnowledgeBundle e autorizado no CheckGrounding.
	LiveAPIDocName = "api-live-data.md"

	defaultTokenPath = "/api/auth/token"
	maxBodyBytes     = 64 * 1024
	maxChainedCalls  = 4
)

var (
	contractNumPattern = regexp.MustCompile(`\b\d{6,14}\b`)
	shortNumPattern    = regexp.MustCompile(`\b\d{3,14}\b`)
	pathParamPattern   = regexp.MustCompile(`\{([a-zA-Z0-9_]+)\}`)
	uuidValuePattern   = regexp.MustCompile(`"(?:userId|id)"\s*:\s*"([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})"`)
)

// TenantAPIConfig reúne as credenciais e a especificação OpenAPI somente-leitura de um tenant.
type TenantAPIConfig struct {
	TenantID  string
	BaseURL   string
	TokenPath string
	Username  string
	Password  string
	SpecName  string
	Endpoints []EndpointDef
}

// EndpointDef descreve um endpoint GET extraído do Swagger/OpenAPI.
type EndpointDef struct {
	Path        string     `json:"path"`
	Summary     string     `json:"summary"`
	Description string     `json:"description,omitempty"`
	Parameters  []ParamDef `json:"parameters,omitempty"`
}

// ParamDef descreve um parâmetro de path ou query de um endpoint GET.
type ParamDef struct {
	Name        string `json:"name"`
	In          string `json:"in"` // "path" ou "query"
	Required    bool   `json:"required"`
	Description string `json:"description,omitempty"`
}

// PlannedRequest representa uma chamada GET planejada contra a API do tenant.
type PlannedRequest struct {
	Path         string `json:"path"`
	Reason       string `json:"reason"`
	FallbackPath string `json:"fallback_path,omitempty"`
}

// ExecutedCall registra o resultado de uma chamada GET realizada.
type ExecutedCall struct {
	Path       string `json:"path"`
	Reason     string `json:"reason"`
	StatusCode int    `json:"status_code"`
	Body       string `json:"body"`
}

// InspectInput reúne o contexto da conversa para planejar e executar consultas GET na API.
type InspectInput struct {
	TenantID       string
	MessageText    string
	RecentMessages []string
	Summary        string
	Intent         string
	Facts          map[string]string
	Signals        map[string]bool
	LLM            core.LLM
	Provider       string
	Model          string
}

// InspectResult retorna o documento Markdown formatado e a lista de chamadas realizadas.
type InspectResult struct {
	DocName string
	Content string
	Calls   []ExecutedCall
}

type cachedToken struct {
	token     string
	expiresAt time.Time
}

// Inspector gerencia descoberta de specs em tenants/<tenant>/apis/, cache de token JWT e chamadas GET.
type Inspector struct {
	tenantsDir string
	clock      core.Clock
	httpClient *http.Client
	logger     *slog.Logger

	mu         sync.RWMutex
	cfgCache   map[string]*TenantAPIConfig
	tokenCache map[string]cachedToken
}

// NewInspector cria um Inspector agnóstico de domínio para tenants/<tenant>/apis/.
func NewInspector(tenantsDir string, clock core.Clock, logger *slog.Logger) *Inspector {
	if strings.TrimSpace(tenantsDir) == "" {
		tenantsDir = "tenants"
	}
	if clock == nil {
		clock = core.SystemClock{}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Inspector{
		tenantsDir: tenantsDir,
		clock:      clock,
		httpClient: &http.Client{Timeout: 8 * time.Second},
		logger:     logger,
		cfgCache:   make(map[string]*TenantAPIConfig),
		tokenCache: make(map[string]cachedToken),
	}
}

// WithHTTPClient permite injetar um *http.Client customizado em testes.
func (ins *Inspector) WithHTTPClient(c *http.Client) *Inspector {
	if c != nil {
		ins.httpClient = c
	}
	return ins
}

// ParseDotEnv é uma função pura que interpreta linhas CHAVE=VALOR de um arquivo .env.
func ParseDotEnv(raw string) map[string]string {
	out := make(map[string]string)
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		idx := strings.IndexByte(trimmed, '=')
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(trimmed[:idx])
		val := strings.TrimSpace(trimmed[idx+1:])
		val = strings.Trim(val, `"'`)
		if key != "" {
			out[key] = val
		}
	}
	return out
}

// ParseOpenAPIGetEndpoints é uma função pura que extrai apenas operações HTTP GET de um YAML OpenAPI 3.0.
func ParseOpenAPIGetEndpoints(rawYAML []byte) ([]EndpointDef, error) {
	var doc struct {
		Paths map[string]struct {
			Get *struct {
				Summary     string `yaml:"summary"`
				Description string `yaml:"description"`
				Parameters  []struct {
					Name        string `yaml:"name"`
					In          string `yaml:"in"`
					Required    bool   `yaml:"required"`
					Description string `yaml:"description"`
				} `yaml:"parameters"`
			} `yaml:"get"`
		} `yaml:"paths"`
	}
	if err := yaml.Unmarshal(rawYAML, &doc); err != nil {
		return nil, fmt.Errorf("falha ao parsear spec OpenAPI: %w", err)
	}
	if len(doc.Paths) == 0 {
		return nil, nil
	}

	paths := make([]string, 0, len(doc.Paths))
	for p, item := range doc.Paths {
		if item.Get != nil && strings.HasPrefix(p, "/") {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)

	endpoints := make([]EndpointDef, 0, len(paths))
	for _, p := range paths {
		g := doc.Paths[p].Get
		params := make([]ParamDef, 0, len(g.Parameters))
		for _, prm := range g.Parameters {
			in := strings.ToLower(strings.TrimSpace(prm.In))
			if in != "path" && in != "query" {
				continue
			}
			params = append(params, ParamDef{
				Name:        strings.TrimSpace(prm.Name),
				In:          in,
				Required:    prm.Required,
				Description: strings.TrimSpace(prm.Description),
			})
		}
		endpoints = append(endpoints, EndpointDef{
			Path:        p,
			Summary:     strings.TrimSpace(g.Summary),
			Description: firstSentence(strings.TrimSpace(g.Description)),
			Parameters:  params,
		})
	}
	return endpoints, nil
}

func firstSentence(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if idx := strings.Index(s, ". "); idx > 0 && idx < 180 {
		return s[:idx+1]
	}
	if len(s) > 180 {
		return s[:180] + "..."
	}
	return s
}

// PlanDeterministicRequests é uma função pura que gera chamadas GET determinísticas
// quando números de contrato, matrículas ou buscas explícitas estão presentes na conversa.
func PlanDeterministicRequests(endpoints []EndpointDef, messageText string, recentTexts []string, facts map[string]string) []PlannedRequest {
	available := make(map[string]bool, len(endpoints))
	for _, ep := range endpoints {
		available[ep.Path] = true
	}

	var planned []PlannedRequest
	seen := make(map[string]bool)
	addPlan := func(path, reason, fallback string) {
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		planned = append(planned, PlannedRequest{
			Path:         path,
			Reason:       reason,
			FallbackPath: fallback,
		})
	}

	// 1. Extrai possíveis números de contrato/referência (priorizando mensagem atual e ReferenceId).
	var candidateNumbers []string
	seenNums := make(map[string]bool)
	pushNum := func(n string) {
		n = strings.TrimSpace(n)
		if n == "" || seenNums[n] {
			return
		}
		seenNums[n] = true
		candidateNumbers = append(candidateNumbers, n)
	}

	for _, m := range contractNumPattern.FindAllString(messageText, -1) {
		pushNum(m)
	}
	if ref := strings.TrimSpace(facts["ReferenceId"]); ref != "" {
		for _, m := range contractNumPattern.FindAllString(ref, -1) {
			pushNum(m)
		}
	}
	for i := len(recentTexts) - 1; i >= 0; i-- {
		for _, m := range contractNumPattern.FindAllString(recentTexts[i], -1) {
			pushNum(m)
		}
	}

	for _, num := range candidateNumbers {
		if available["/api/contracts/number/{contractNumber}"] {
			fallback := ""
			if available["/api/admintools/deleted-contracts"] {
				fallback = "/api/admintools/deleted-contracts?contractNumber=" + url.QueryEscape(num)
			}
			addPlan(
				"/api/contracts/number/"+url.PathEscape(num),
				fmt.Sprintf("Consultar contrato pelo número %s", num),
				fallback,
			)
		}
		if len(planned) >= 2 {
			break
		}
	}

	// 2. Se mencionar matrícula com número (ex.: "matrícula 8338"), consulta /api/matriculas?search=...
	lowerMsg := strings.ToLower(messageText)
	if (strings.Contains(lowerMsg, "matrícula") || strings.Contains(lowerMsg, "matricula")) && available["/api/matriculas"] {
		for _, num := range shortNumPattern.FindAllString(messageText, -1) {
			if seenNums[num] {
				continue
			}
			addPlan(
				"/api/matriculas?search="+url.QueryEscape(num),
				fmt.Sprintf("Consultar matrícula %s", num),
				"",
			)
			break
		}
	}

	// 3. Se houver TargetUser nos fatos ou se um consultor específico for citado, consulta /api/users?search=... ou /api/admintools/users/search?q=...
	if targetUser := strings.TrimSpace(facts["TargetUser"]); targetUser != "" {
		if available["/api/users"] {
			addPlan(
				"/api/users?search="+url.QueryEscape(targetUser),
				fmt.Sprintf("Buscar consultor/usuário %s no sistema", targetUser),
				"",
			)
		} else if available["/api/admintools/users/search"] {
			addPlan(
				"/api/admintools/users/search?q="+url.QueryEscape(targetUser),
				fmt.Sprintf("Buscar consultor/usuário %s no sistema", targetUser),
				"",
			)
		}
	}

	return planned
}

// PlanFollowUpUserRequests gera chamadas GET encadeadas por userId quando a resposta anterior contém um UUID de usuário
// e a pergunta menciona perfil/gestor/equipe, plano de carreira, classificação/nível ou matrículas do usuário.
func PlanFollowUpUserRequests(endpoints []EndpointDef, combinedText, bodyJSON string) []PlannedRequest {
	matches := uuidValuePattern.FindStringSubmatch(bodyJSON)
	if len(matches) < 2 {
		return nil
	}
	userID := matches[1]
	available := make(map[string]bool, len(endpoints))
	for _, ep := range endpoints {
		available[ep.Path] = true
	}

	lower := strings.ToLower(combinedText)
	var out []PlannedRequest

	// 1. Perfil completo do usuário e gestor responsável (/api/users/{id} ou /api/users/{userId})
	var userPathTmpl string
	if available["/api/users/{id}"] {
		userPathTmpl = "/api/users/" + url.PathEscape(userID)
	} else if available["/api/users/{userId}"] {
		userPathTmpl = "/api/users/" + url.PathEscape(userID)
	}
	if userPathTmpl != "" {
		if lower == "" ||
			strings.Contains(lower, "gestor") || strings.Contains(lower, "líder") || strings.Contains(lower, "lider") ||
			strings.Contains(lower, "equipe") || strings.Contains(lower, "responsável") || strings.Contains(lower, "responsavel") ||
			strings.Contains(lower, "quem") || strings.Contains(lower, "email") || strings.Contains(lower, "e-mail") ||
			strings.Contains(lower, "pertence") || strings.Contains(lower, "consultor") || strings.Contains(lower, "dono") ||
			strings.Contains(lower, "atribuído") || strings.Contains(lower, "atribuido") || strings.Contains(lower, "usuario") ||
			strings.Contains(lower, "usuário") || strings.Contains(lower, "contrato") {
			out = append(out, PlannedRequest{
				Path:   userPathTmpl,
				Reason: "Consultar perfil e gestor da equipe do usuário identificado",
			})
		}
	}

	if (strings.Contains(lower, "carreira") || strings.Contains(lower, "meta") || strings.Contains(lower, "retenção") || strings.Contains(lower, "retencao")) &&
		available["/api/careerplan/users/{userId}"] {
		out = append(out, PlannedRequest{
			Path:   "/api/careerplan/users/" + url.PathEscape(userID),
			Reason: "Consultar plano de carreira do usuário identificado",
		})
	}
	if (strings.Contains(lower, "classificação") || strings.Contains(lower, "classificacao") || strings.Contains(lower, "nível") || strings.Contains(lower, "nivel") || strings.Contains(lower, "carreira")) &&
		available["/api/classifications/users/{userId}/active"] {
		out = append(out, PlannedRequest{
			Path:   "/api/classifications/users/" + url.PathEscape(userID) + "/active",
			Reason: "Verificar classificação ativa no plano de carreira",
		})
	}
	if (strings.Contains(lower, "matrícula") || strings.Contains(lower, "matricula")) &&
		available["/api/usermatriculas/user/{userId}"] {
		out = append(out, PlannedRequest{
			Path:   "/api/usermatriculas/user/" + url.PathEscape(userID),
			Reason: "Listar matrículas vinculadas ao usuário",
		})
	}
	return out
}

// IsSafeGetPath valida puramente se um path relativo (com query string opcional)
// corresponde a algum template GET declarado no OpenAPI do tenant.
func IsSafeGetPath(rawPath string, endpoints []EndpointDef) bool {
	trimmed := strings.TrimSpace(rawPath)
	if !strings.HasPrefix(trimmed, "/api/") || strings.Contains(trimmed, "..") || strings.Contains(trimmed, "://") {
		return false
	}
	u, err := url.ParseRequestURI(trimmed)
	if err != nil || u.Path == "" {
		return false
	}
	reqSegs := strings.Split(strings.Trim(u.Path, "/"), "/")
	for _, ep := range endpoints {
		epSegs := strings.Split(strings.Trim(ep.Path, "/"), "/")
		if len(reqSegs) != len(epSegs) {
			continue
		}
		match := true
		for i := range epSegs {
			if strings.HasPrefix(epSegs[i], "{") && strings.HasSuffix(epSegs[i], "}") {
				if strings.TrimSpace(reqSegs[i]) == "" {
					match = false
					break
				}
				continue
			}
			if epSegs[i] != reqSegs[i] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// FormatInspectionMarkdown formata de modo puro os resultados das chamadas GET em Markdown compacto legível pelo LLM.
func FormatInspectionMarkdown(calls []ExecutedCall) string {
	return FormatInspectionMarkdownWithQuery(calls, "")
}

// FormatInspectionMarkdownWithQuery formata de modo puro os resultados das chamadas GET
// podando campos vazios/ruído, filtrando listas longas por relevância (Grep/BM25) e convertendo para Key-Value enxuto.
func FormatInspectionMarkdownWithQuery(calls []ExecutedCall, query string) string {
	if len(calls) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Dados Reais Consultados na API do Sistema (Tempo Real)\n\n")
	b.WriteString("Use os dados oficiais abaixo (obtidos em tempo real na API) para responder com precisão ao usuário:\n\n")
	for _, c := range calls {
		fmt.Fprintf(&b, "## Consulta: `GET %s` (HTTP %d)\n", c.Path, c.StatusCode)
		if c.Reason != "" {
			fmt.Fprintf(&b, "- **Motivo**: %s\n", c.Reason)
		}
		compact := CompactJSONBody(c.Body, query, DefaultMaxArrayItems)
		fmt.Fprintf(&b, "- **Dados**:\n%s\n\n", compact)
	}
	return strings.TrimSpace(b.String())
}

// LoadTenantConfig carrega e faz cache da configuração em <tenantsDir>/<tenant>/apis/.
func (ins *Inspector) LoadTenantConfig(tenantID string) (*TenantAPIConfig, error) {
	cleanID := filepath.Base(strings.TrimSpace(tenantID))
	if cleanID == "" || cleanID == "." || cleanID == ".." {
		return nil, nil
	}

	ins.mu.RLock()
	if cached, ok := ins.cfgCache[cleanID]; ok {
		ins.mu.RUnlock()
		return cached, nil
	}
	ins.mu.RUnlock()

	apisDir := filepath.Join(ins.tenantsDir, cleanID, "apis")
	envBytes, err := os.ReadFile(filepath.Join(apisDir, ".env"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	envMap := ParseDotEnv(string(envBytes))
	baseURL := firstNonEmpty(envMap["SALESAPP_API_URL"], envMap["API_BASE_URL"], os.Getenv("SALESAPP_API_URL"))
	username := firstNonEmpty(envMap["SUPERADMIN_USERNAME"], envMap["API_USERNAME"], os.Getenv("SUPERADMIN_USERNAME"))
	password := firstNonEmpty(envMap["SUPERADMIN_PASSWORD"], envMap["API_PASSWORD"], os.Getenv("SUPERADMIN_PASSWORD"))
	tokenPath := firstNonEmpty(envMap["API_TOKEN_PATH"], defaultTokenPath)

	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" || username == "" || password == "" {
		return nil, nil
	}

	entries, err := os.ReadDir(apisDir)
	if err != nil {
		return nil, err
	}
	var allEndpoints []EndpointDef
	var specNames []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		lower := strings.ToLower(entry.Name())
		if !strings.HasSuffix(lower, ".yml") && !strings.HasSuffix(lower, ".yaml") {
			continue
		}
		rawSpec, readErr := os.ReadFile(filepath.Join(apisDir, entry.Name()))
		if readErr != nil {
			continue
		}
		eps, parseErr := ParseOpenAPIGetEndpoints(rawSpec)
		if parseErr != nil || len(eps) == 0 {
			continue
		}
		specNames = append(specNames, entry.Name())
		allEndpoints = append(allEndpoints, eps...)
	}
	if len(allEndpoints) == 0 {
		return nil, nil
	}

	cfg := &TenantAPIConfig{
		TenantID:  cleanID,
		BaseURL:   baseURL,
		TokenPath: tokenPath,
		Username:  username,
		Password:  password,
		SpecName:  strings.Join(specNames, ","),
		Endpoints: allEndpoints,
	}

	ins.mu.Lock()
	ins.cfgCache[cleanID] = cfg
	ins.mu.Unlock()
	return cfg, nil
}

// Inspect planeja e executa consultas GET somente-leitura na API do tenant quando aplicável.
func (ins *Inspector) Inspect(ctx context.Context, in InspectInput) (*InspectResult, error) {
	cfg, err := ins.LoadTenantConfig(in.TenantID)
	if err != nil || cfg == nil {
		return nil, err
	}

	planned := PlanDeterministicRequests(cfg.Endpoints, in.MessageText, in.RecentMessages, in.Facts)

	// O planejador leve via LLM só roda se:
	// 1. O fast-path determinístico não resolveu a consulta;
	// 2. O sinal do estágio de análise confirmou needs_api_lookup == true (ou fallback heurístico para compatibilidade quando signals for omitido);
	// 3. Um LLM estiver disponível.
	needsLookup := false
	if in.Signals != nil {
		needsLookup = in.Signals["needs_api_lookup"]
	} else {
		needsLookup = shouldRunLLMPlanner(in.MessageText, in.Summary)
	}

	if len(planned) == 0 && needsLookup && in.LLM != nil {
		llmPlans := ins.planWithLLM(ctx, cfg, in)
		planned = append(planned, llmPlans...)
	}

	if len(planned) == 0 {
		return nil, nil
	}

	token, err := ins.getOrFetchToken(ctx, cfg, false)
	if err != nil {
		ins.logger.Warn("falha ao autenticar na API do tenant; seguindo sem dados ao vivo",
			slog.String("tenant", in.TenantID),
			slog.Any("error", err),
		)
		return nil, nil
	}

	var executed []ExecutedCall
	executedPaths := make(map[string]bool)

	for i := 0; i < len(planned) && len(executed) < maxChainedCalls; i++ {
		reqPlan := planned[i]
		compactPath := EnsureCompactQueryParams(reqPlan.Path, cfg.Endpoints, DefaultCompactPageSize)
		if !IsSafeGetPath(compactPath, cfg.Endpoints) || executedPaths[compactPath] {
			continue
		}
		executedPaths[compactPath] = true

		status, body, callErr := ins.doGET(ctx, cfg, token, compactPath)
		if status == http.StatusUnauthorized {
			if freshToken, tokErr := ins.getOrFetchToken(ctx, cfg, true); tokErr == nil {
				token = freshToken
				status, body, callErr = ins.doGET(ctx, cfg, token, compactPath)
			}
		}
		if callErr != nil {
			ins.logger.Warn("erro na chamada GET da API do tenant",
				slog.String("tenant", in.TenantID),
				slog.String("path", compactPath),
				slog.Any("error", callErr),
			)
			continue
		}

		executed = append(executed, ExecutedCall{
			Path:       compactPath,
			Reason:     reqPlan.Reason,
			StatusCode: status,
			Body:       body,
		})

		// Se retornou 404 ou data:null e há fallback (ex.: verificar em deleted-contracts), encadeia o fallback!
		if (status == http.StatusNotFound || isNullDataPayload(body)) && reqPlan.FallbackPath != "" {
			if IsSafeGetPath(reqPlan.FallbackPath, cfg.Endpoints) && !executedPaths[reqPlan.FallbackPath] {
				planned = append(planned, PlannedRequest{
					Path:   reqPlan.FallbackPath,
					Reason: "Verificar se o registro consta nos excluídos/deletados",
				})
			}
		} else if status >= 200 && status < 300 {
			combinedContext := in.MessageText + " " + strings.Join(in.RecentMessages, " ") + " " + in.Summary
			for _, follow := range PlanFollowUpUserRequests(cfg.Endpoints, combinedContext, body) {
				if IsSafeGetPath(follow.Path, cfg.Endpoints) && !executedPaths[follow.Path] {
					planned = append(planned, follow)
				}
			}
		}
	}

	if len(executed) == 0 {
		return nil, nil
	}

	searchQuery := buildInspectionQuery(in)
	return &InspectResult{
		DocName: LiveAPIDocName,
		Content: FormatInspectionMarkdownWithQuery(executed, searchQuery),
		Calls:   executed,
	}, nil
}

func buildInspectionQuery(in InspectInput) string {
	var parts []string
	if strings.TrimSpace(in.MessageText) != "" {
		parts = append(parts, in.MessageText)
	}
	if strings.TrimSpace(in.Summary) != "" {
		parts = append(parts, in.Summary)
	}
	for k, v := range in.Facts {
		if strings.TrimSpace(v) != "" && v != "true" && v != "false" {
			parts = append(parts, k+" "+v)
		}
	}
	return strings.Join(parts, " ")
}

func isNullDataPayload(body string) bool {
	var env struct {
		Success *bool           `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if json.Unmarshal([]byte(body), &env) != nil {
		return false
	}
	if env.Success != nil && !*env.Success {
		return true
	}
	trimmed := strings.TrimSpace(string(env.Data))
	return trimmed == "null" || trimmed == ""
}

func shouldRunLLMPlanner(msgText, summary string) bool {
	combined := strings.ToLower(msgText + " " + summary)
	triggers := []string{
		"conferir", "confere", "verificar", "verifica", "consultar", "consulta",
		"existe", "pertence", "atribuído", "atribuido", "vinculado", "titular",
		"contrato", "matrícula", "matricula", "plano de carreira", "classificação",
		"classificacao", "equipe", "solicitação", "solicitacao", "importação", "importacao",
		"scraping", "powerbi", "power bi", "deletado", "excluído", "excluido", "status",
	}
	for _, t := range triggers {
		if strings.Contains(combined, t) {
			return true
		}
	}
	return false
}

func (ins *Inspector) planWithLLM(ctx context.Context, cfg *TenantAPIConfig, in InspectInput) []PlannedRequest {
	relevantEndpoints := SelectRelevantEndpoints(cfg.Endpoints, buildInspectionQuery(in), DefaultPlannerTopEndpoints)
	if len(relevantEndpoints) == 0 {
		return nil
	}
	var catalog strings.Builder
	for _, ep := range relevantEndpoints {
		fmt.Fprintf(&catalog, "- GET %s — %s", ep.Path, ep.Summary)
		if len(ep.Parameters) > 0 {
			var pNames []string
			for _, p := range ep.Parameters {
				pNames = append(pNames, fmt.Sprintf("%s(%s)", p.Name, p.In))
			}
			fmt.Fprintf(&catalog, " [params: %s]", strings.Join(pNames, ", "))
		}
		catalog.WriteByte('\n')
	}

	sysPrompt := `Você é um planejador de consultas somente-leitura (GET) em uma API REST administrativa.
Dado o catálogo de endpoints GET abaixo e a dúvida do usuário, decida se alguma consulta GET na API ajuda a responder com fatos reais (ex.: buscar contrato, buscar usuário por nome/e-mail, listar equipes, matrículas, solicitações pendentes, importações ou scraping).
Retorne APENAS um JSON válido no formato:
{"requests": [{"path": "/api/...", "reason": "motivo curto"}]}
Se nenhuma chamada de API for necessária ou se faltar o identificador/termo de busca, retorne {"requests": []}. Máximo de 2 requisições.`

	userPrompt := fmt.Sprintf("CATÁLOGO DE ENDPOINTS GET:\n%s\nRESUMO: %s\nFATOS: %v\nMENSAGEM DO USUÁRIO: %s",
		catalog.String(), in.Summary, in.Facts, in.MessageText)

	resp, err := in.LLM.Generate(ctx, core.LLMRequest{
		Stage:       "api_planner",
		Provider:    in.Provider,
		Model:       in.Model,
		System:      sysPrompt,
		Prompt:      userPrompt,
		Temperature: 0.0,
		MaxTokens:   400,
	})
	if err != nil || resp == nil {
		return nil
	}

	raw := strings.TrimSpace(resp.Content)
	if start := strings.IndexByte(raw, '{'); start >= 0 {
		if end := strings.LastIndexByte(raw, '}'); end > start {
			raw = raw[start : end+1]
		}
	}
	var parsed struct {
		Requests []PlannedRequest `json:"requests"`
	}
	if json.Unmarshal([]byte(raw), &parsed) != nil {
		return nil
	}
	var valid []PlannedRequest
	for _, r := range parsed.Requests {
		if IsSafeGetPath(r.Path, cfg.Endpoints) {
			valid = append(valid, r)
		}
		if len(valid) >= 2 {
			break
		}
	}
	return valid
}

func (ins *Inspector) getOrFetchToken(ctx context.Context, cfg *TenantAPIConfig, forceRefresh bool) (string, error) {
	now := ins.clock.Now()
	if !forceRefresh {
		ins.mu.RLock()
		tok, ok := ins.tokenCache[cfg.TenantID]
		ins.mu.RUnlock()
		if ok && tok.token != "" && now.Before(tok.expiresAt) {
			return tok.token, nil
		}
	}

	form := url.Values{}
	form.Set("grant_type", "password")
	form.Set("username", cfg.Username)
	form.Set("password", cfg.Password)

	tokenURL := cfg.BaseURL + cfg.TokenPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "WhatshelperBrain/1.0")

	resp, err := ins.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("status %d ao obter token em %s", resp.StatusCode, cfg.TokenPath)
	}

	var tokPayload struct {
		AccessToken string `json:"access_token"`
		Token       string `json:"token"`
		ExpiresIn   int    `json:"expires_in"`
		Data        struct {
			AccessToken string `json:"accessToken"`
		} `json:"data"`
	}
	if err := json.Unmarshal(bodyBytes, &tokPayload); err != nil {
		return "", err
	}
	token := firstNonEmpty(tokPayload.AccessToken, tokPayload.Token, tokPayload.Data.AccessToken)
	if token == "" {
		return "", fmt.Errorf("token JWT ausente na resposta de %s", cfg.TokenPath)
	}
	ttl := time.Duration(tokPayload.ExpiresIn) * time.Second
	if ttl <= 2*time.Minute {
		ttl = 30 * time.Minute
	} else {
		ttl -= time.Minute
	}

	ins.mu.Lock()
	ins.tokenCache[cfg.TenantID] = cachedToken{
		token:     token,
		expiresAt: now.Add(ttl),
	}
	ins.mu.Unlock()
	return token, nil
}

func (ins *Inspector) doGET(ctx context.Context, cfg *TenantAPIConfig, token, relPath string) (int, string, error) {
	if !IsSafeGetPath(relPath, cfg.Endpoints) {
		return 0, "", fmt.Errorf("path GET não autorizado pelo OpenAPI: %s", relPath)
	}
	fullURL := cfg.BaseURL + relPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "WhatshelperBrain/1.0")

	resp, err := ins.httpClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return resp.StatusCode, "", err
	}
	return resp.StatusCode, strings.TrimSpace(string(raw)), nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
