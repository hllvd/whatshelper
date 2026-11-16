package apiclient_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"whatshelper/internal/apiclient"
	"whatshelper/internal/core/fakes"
)

const sampleSwaggerYAML = `
openapi: 3.0.3
info:
  title: SalesApp Read-Only Spec
  version: 1.0.0
paths:
  /api/contracts/number/{contractNumber}:
    get:
      summary: Consultar contrato pelo número
      description: Localiza um contrato ativo pelo seu número comercial.
      parameters:
        - name: contractNumber
          in: path
          required: true
  /api/admintools/deleted-contracts:
    get:
      summary: Verificar se contratos foram excluídos
      parameters:
        - name: contractNumber
          in: query
          required: false
  /api/matriculas:
    get:
      summary: Listar matrículas
      parameters:
        - name: search
          in: query
          required: false
  /api/careerplan/users/{userId}:
    get:
      summary: Consultar plano de carreira do usuário
      parameters:
        - name: userId
          in: path
          required: true
  /api/users/{id}:
    get:
      summary: Consultar perfil do usuário e gestor
      parameters:
        - name: id
          in: path
          required: true
  /api/users:
    get:
      summary: Buscar usuários
      parameters:
        - name: search
          in: query
          required: false
`

func TestParseDotEnv_And_ParseOpenAPIGetEndpoints(t *testing.T) {
	env := apiclient.ParseDotEnv("# comment\nSALESAPP_API_URL=https://ademicon.hagadev.com\nSUPERADMIN_USERNAME=\"superadmin@salesapp.com\"\nSUPERADMIN_PASSWORD=secret\n")
	if env["SALESAPP_API_URL"] != "https://ademicon.hagadev.com" {
		t.Fatalf("unexpected SALESAPP_API_URL: %q", env["SALESAPP_API_URL"])
	}
	if env["SUPERADMIN_USERNAME"] != "superadmin@salesapp.com" {
		t.Fatalf("unexpected SUPERADMIN_USERNAME: %q", env["SUPERADMIN_USERNAME"])
	}

	eps, err := apiclient.ParseOpenAPIGetEndpoints([]byte(sampleSwaggerYAML))
	if err != nil {
		t.Fatalf("ParseOpenAPIGetEndpoints failed: %v", err)
	}
	if len(eps) != 6 {
		t.Fatalf("expected 6 GET endpoints, got %d", len(eps))
	}
	if !apiclient.IsSafeGetPath("/api/contracts/number/1101053001", eps) {
		t.Fatalf("expected /api/contracts/number/1101053001 to be safe")
	}
	if !apiclient.IsSafeGetPath("/api/admintools/deleted-contracts?contractNumber=1101053001", eps) {
		t.Fatalf("expected deleted-contracts query to be safe")
	}
	if apiclient.IsSafeGetPath("/api/users/delete/123", eps) {
		t.Fatalf("did not expect undeclared path to be allowed")
	}
	if apiclient.IsSafeGetPath("https://evil.example.com/api/contracts/number/1", eps) {
		t.Fatalf("did not expect absolute URL to be allowed")
	}
}

func TestPlanDeterministicRequests(t *testing.T) {
	eps, err := apiclient.ParseOpenAPIGetEndpoints([]byte(sampleSwaggerYAML))
	if err != nil {
		t.Fatal(err)
	}

	plans := apiclient.PlanDeterministicRequests(
		eps,
		"vc poderia conferir se o contrato 1101053001 existe?",
		[]string{"Nao consigo saber se o contrato 1101053001 pertence a emanually"},
		map[string]string{"ReferenceId": "1101053001"},
	)
	if len(plans) != 1 {
		t.Fatalf("expected 1 deterministic plan, got %+v", plans)
	}
	if plans[0].Path != "/api/contracts/number/1101053001" {
		t.Fatalf("unexpected path: %s", plans[0].Path)
	}
	if plans[0].FallbackPath != "/api/admintools/deleted-contracts?contractNumber=1101053001" {
		t.Fatalf("unexpected fallback path: %s", plans[0].FallbackPath)
	}
}

func TestInspector_Inspect_ContractFoundAndFallbackDeleted(t *testing.T) {
	var tokenCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/auth/token":
			atomic.AddInt32(&tokenCalls, 1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"jwt-test-token","token_type":"Bearer","expires_in":3600}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/contracts/number/1101053001":
			if r.Header.Get("Authorization") != "Bearer jwt-test-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":96664,"contractNumber":"1101053001","userId":"9d5d2b12-d32b-4532-8dd2-29d5bc3b6b2b","userName":"Emanuelly Barbosa","status":"Active","isActive":true,"teamName":"netzer","matriculaNumber":"8338"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/contracts/number/9999999999":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"success":false,"message":"Contrato não encontrado"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/admintools/deleted-contracts":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true,"data":[{"contractNumber":"9999999999","isActive":false,"deletedReason":"Cancelado"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/users/9d5d2b12-d32b-4532-8dd2-29d5bc3b6b2b":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":"9d5d2b12-d32b-4532-8dd2-29d5bc3b6b2b","name":"Emanuelly Barbosa","email":"manubarbosa102030@gmail.com","role":"user","parentUserName":"João Augusto e Deborah Bozza","parentEmail":"deborah.bozza@autorizadoademicon.com.br","currentTeamName":"netzer","isActive":true}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/users":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true,"data":{"items":[{"id":"9d5d2b12-d32b-4532-8dd2-29d5bc3b6b2b","name":"Emanuelly Barbosa","email":"manubarbosa102030@gmail.com","parentUserName":"João Augusto e Deborah Bozza","parentEmail":"deborah.bozza@autorizadoademicon.com.br","currentTeamName":"netzer"}]}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	tmp := t.TempDir()
	apisDir := filepath.Join(tmp, "supporter", "apis")
	if err := os.MkdirAll(apisDir, 0o755); err != nil {
		t.Fatal(err)
	}
	envContent := "SALESAPP_API_URL=" + srv.URL + "\nSUPERADMIN_USERNAME=superadmin@salesapp.com\nSUPERADMIN_PASSWORD=secret\n"
	if err := os.WriteFile(filepath.Join(apisDir, ".env"), []byte(envContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(apisDir, "swagger-superadmin.yml"), []byte(sampleSwaggerYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	clock := fakes.NewFakeClock(time.Date(2026, 11, 15, 19, 0, 0, 0, time.UTC))
	ins := apiclient.NewInspector(tmp, clock, nil).WithHTTPClient(srv.Client())

	// 1. Contrato ativo 1101053001 -> retorna Emanuelly Barbosa e encadeia dados do usuário/gestor
	res, err := ins.Inspect(context.Background(), apiclient.InspectInput{
		TenantID:    "supporter",
		MessageText: "vc poderia conferir se o contrato 1101053001 existe?",
		Facts:       map[string]string{"ReferenceId": "1101053001"},
	})
	if err != nil || res == nil {
		t.Fatalf("expected Inspect result, got res=%v err=%v", res, err)
	}
	if res.DocName != apiclient.LiveAPIDocName {
		t.Fatalf("expected doc name %s, got %s", apiclient.LiveAPIDocName, res.DocName)
	}
	if !strings.Contains(res.Content, "Emanuelly Barbosa") || !strings.Contains(res.Content, "1101053001") {
		t.Fatalf("expected API response to contain Emanuelly Barbosa and 1101053001, got:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "João Augusto e Deborah Bozza") {
		t.Fatalf("expected chained user response to contain manager João Augusto e Deborah Bozza, got:\n%s", res.Content)
	}

	// 2. Contrato 9999999999 não ativo -> aciona fallback automático em deleted-contracts e reutiliza token em cache
	res2, err := ins.Inspect(context.Background(), apiclient.InspectInput{
		TenantID:    "supporter",
		MessageText: "o contrato 9999999999 foi excluído?",
	})
	if err != nil || res2 == nil {
		t.Fatalf("expected Inspect result for deleted contract, got res=%v err=%v", res2, err)
	}
	if len(res2.Calls) != 2 {
		t.Fatalf("expected 2 calls (lookup + deleted fallback), got %d", len(res2.Calls))
	}
	if atomic.LoadInt32(&tokenCalls) != 1 {
		t.Fatalf("expected cached JWT token to be reused (1 auth call), got %d", atomic.LoadInt32(&tokenCalls))
	}

	// 3. Pergunta How-To com needs_api_lookup = false -> NÃO chama API nem LLM planner
	res3, err := ins.Inspect(context.Background(), apiclient.InspectInput{
		TenantID:    "supporter",
		MessageText: "como faço para adicionar um consultor?",
		Signals:     map[string]bool{"needs_api_lookup": false},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res3 != nil {
		t.Fatalf("expected nil Inspect result for how-to query with needs_api_lookup=false, got: %+v", res3)
	}

	// 4. Pergunta com TargetUser nos fatos -> dispara busca determinística por usuário
	res4, err := ins.Inspect(context.Background(), apiclient.InspectInput{
		TenantID:    "supporter",
		MessageText: "a consultora Emanuelly pertence a qual equipe?",
		Facts:       map[string]string{"TargetUser": "Emanuelly"},
		Signals:     map[string]bool{"needs_api_lookup": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	plansUser := apiclient.PlanDeterministicRequests([]apiclient.EndpointDef{
		{Path: "/api/users"},
	}, "a consultora Emanuelly pertence a qual equipe?", nil, map[string]string{"TargetUser": "Emanuelly"})
	if len(plansUser) != 1 || !strings.Contains(plansUser[0].Path, "/api/users?search=Emanuelly") {
		t.Fatalf("expected user search plan via /api/users?search, got %+v", plansUser)
	}
	_ = res4

	// 5. Pergunta subsequente de gestor da equipe sem citar contrato diretamente na mensagem atual
	res5, err := ins.Inspect(context.Background(), apiclient.InspectInput{
		TenantID:       "supporter",
		MessageText:    "quem é o gestor da equipe?",
		RecentMessages: []string{"o contrato 1101053001 está ativo", "esse contrato nao pertence a emily, como faço?"},
	})
	if err != nil || res5 == nil {
		t.Fatalf("expected Inspect result for follow-up manager question, got res=%v err=%v", res5, err)
	}
	if !strings.Contains(res5.Content, "João Augusto e Deborah Bozza") {
		t.Fatalf("expected manager information in api-live-data, got:\n%s", res5.Content)
	}
}

func TestCompactJSONBody_PrunesNoiseCapsArraysAndPreservesNumbers(t *testing.T) {
	var itemsBuilder strings.Builder
	itemsBuilder.WriteString(`{"success":true,"data":[`)
	for i := 1; i <= 20; i++ {
		if i > 1 {
			itemsBuilder.WriteByte(',')
		}
		if i == 17 {
			itemsBuilder.WriteString(`{"id":96664,"contractNumber":"1101053001","userName":"Emanuelly Barbosa","status":"Active","totalValue":185000,"notes":"","deletedAt":null,"emptyArr":[],"passwordHash":"secret-hash","securityStamp":"stamp-123"}`)
		} else {
			itemsBuilder.WriteString(`{"id":1000,"contractNumber":"2200000000","userName":"Outro Consultor","status":"Pending","totalValue":50000,"notes":null,"emptyObj":{},"avatarBase64":"iVBORw0KGgoAAAANSUhEUgAAAAUA"}`)
		}
	}
	itemsBuilder.WriteString(`]}`)
	rawJSON := itemsBuilder.String()

	compacted := apiclient.CompactJSONBody(rawJSON, "contrato 1101053001 Emanuelly", 5)

	if strings.Contains(compacted, "passwordHash") || strings.Contains(compacted, "securityStamp") || strings.Contains(compacted, "avatarBase64") {
		t.Fatalf("expected noisy/sensitive internal fields pruned, got:\n%s", compacted)
	}
	if strings.Contains(compacted, "null") || strings.Contains(compacted, "emptyArr") {
		t.Fatalf("expected null and empty fields pruned, got:\n%s", compacted)
	}
	if !strings.Contains(compacted, "1101053001") || !strings.Contains(compacted, "Emanuelly Barbosa") || !strings.Contains(compacted, "185000") {
		t.Fatalf("expected item 17 ranked into Top-5 with numbers preserved, got:\n%s", compacted)
	}
	if !strings.Contains(compacted, "[total_items: 20 | showing_top: 5]") {
		t.Fatalf("expected array cap summary marker, got:\n%s", compacted)
	}
	if len(compacted) >= len(rawJSON)/3 {
		t.Fatalf("expected >66%% reduction in payload characters: compacted=%d raw=%d", len(compacted), len(rawJSON))
	}
}

func TestEnsureCompactQueryParams_And_SelectRelevantEndpoints(t *testing.T) {
	rawSpec, err := os.ReadFile(filepath.Join("..", "..", "tenants", "supporter", "apis", "swagger-superadmin.yml"))
	if err != nil {
		t.Fatalf("failed to read swagger-superadmin.yml: %v", err)
	}
	allEndpoints, err := apiclient.ParseOpenAPIGetEndpoints(rawSpec)
	if err != nil || len(allEndpoints) < 15 {
		t.Fatalf("expected at least 15 GET endpoints in swagger-superadmin.yml, got %d (err=%v)", len(allEndpoints), err)
	}

	// 1. Injeção automática de pageSize=5 em endpoints paginados
	withPage := apiclient.EnsureCompactQueryParams("/api/contracts?search=Emanuelly", allEndpoints, 5)
	if !strings.Contains(withPage, "pageSize=5") {
		t.Fatalf("expected automatic pageSize=5 injection, got %s", withPage)
	}
	cappedPage := apiclient.EnsureCompactQueryParams("/api/contracts?pageSize=100&search=Emanuelly", allEndpoints, 5)
	if !strings.Contains(cappedPage, "pageSize=5") {
		t.Fatalf("expected large pageSize=100 capped to 5, got %s", cappedPage)
	}

	// 2. Filtragem do catálogo OpenAPI para o LLM planner (sem UUID na query -> descarta rotas {userId}/{teamId} e retorna Top 8)
	selected := apiclient.SelectRelevantEndpoints(allEndpoints, "verificar solicitações pendentes de aprovação e matrículas", 8)
	if len(selected) == 0 || len(selected) > 8 {
		t.Fatalf("expected 1..8 selected endpoints, got %d", len(selected))
	}
	for _, ep := range selected {
		if strings.Contains(ep.Path, "{userId}") || strings.Contains(ep.Path, "{teamId}") {
			t.Fatalf("expected endpoint requiring missing UUID %s to be excluded when query has no UUID", ep.Path)
		}
	}
}
