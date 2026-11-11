package server_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"whatshelper/internal/brain"
	"whatshelper/internal/channels/simulator"
	"whatshelper/internal/config"
	"whatshelper/internal/core"
	"whatshelper/internal/core/fakes"
	"whatshelper/internal/pack"
	"whatshelper/internal/server"
)

const e2ePackYAML = `
name: sample
language: pt-BR
actors:
  business: agent
  customer: customer
facts:
  - key: Budget
    type: number
    description: Orçamento máximo
signals:
  - key: has_new_question
    description: Nova pergunta
  - key: customer_engaged
    description: Cliente engajado
context:
  - key: last_action
    fields:
      - key: type
        type: enum
        values: [question, other]
      - key: actor
        type: enum
        values: [agent, customer]
      - key: description
        type: string
prompts:
  analysis: prompts/analysis.tmpl
  reply: prompts/reply.tmpl
`

func TestE2E_SimulatorToBrainToStoreToVisualizer(t *testing.T) {
	tmp := t.TempDir()
	packDir := filepath.Join(tmp, "packs", "sample")
	_ = os.MkdirAll(filepath.Join(packDir, "prompts"), 0o755)
	_ = os.WriteFile(filepath.Join(packDir, "pack.yaml"), []byte(e2ePackYAML), 0o644)
	_ = os.WriteFile(filepath.Join(packDir, "prompts", "analysis.tmpl"), []byte("Analyze:\n{{.OutputSchema}}\n<!-- USER_PROMPT -->\n{{.NewMessage.Text}}"), 0o644)
	_ = os.WriteFile(filepath.Join(packDir, "prompts", "reply.tmpl"), []byte("Reply:\n{{range .Knowledge}}{{.Content}}\n{{end}}\n<!-- USER_PROMPT -->\n{{.NewMessage.Text}}"), 0o644)

	kDir := filepath.Join(tmp, "tenants", "demo", "knowledge")
	_ = os.MkdirAll(kDir, 0o755)
	_ = os.WriteFile(filepath.Join(tmp, "tenants", "demo", "tenant.yaml"), []byte("id: demo\nname: Demo\npack: sample\n"), 0o644)
	_ = os.WriteFile(filepath.Join(kDir, "faq.md"), []byte("Plano padrão disponível."), 0o644)

	appCtx, appCancel := context.WithCancel(context.Background())
	defer appCancel()

	clock := fakes.NewFakeClock(time.Date(2026, 11, 11, 16, 0, 0, 0, time.UTC))
	storeRepo := fakes.NewMemoryStoreWithClock(clock)
	blobRepo := fakes.NewMemoryBlobStore()
	bus := core.NewInMemoryEventBus(64)
	llm := fakes.NewOfflineDemoLLM()

	simCh := simulator.New(clock, 16)
	if err := simCh.Start(appCtx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = simCh.Stop(context.Background()) }()

	pipeline := brain.NewPipeline(appCtx, brain.PipelineConfig{
		Store:         storeRepo,
		Blobs:         blobRepo,
		LLM:           llm,
		Bus:           bus,
		Clock:         clock,
		Tenants:       brain.NewLocalTenantLoader(filepath.Join(tmp, "tenants")),
		Knowledge:     brain.NewLocalKnowledgeSource(filepath.Join(tmp, "tenants")),
		PackLoader:    func(name string) (*pack.Pack, error) { return pack.LoadFromDir(filepath.Join(tmp, "packs", name)) },
		AnalysisModel: "fake-a",
		ResponseModel: "fake-r",
		Debounce:      0,
	})
	pipeline.RegisterChannel(simCh)
	defer pipeline.Stop()

	go func() {
		for {
			select {
			case <-appCtx.Done():
				return
			case msg, ok := <-simCh.Messages():
				if !ok {
					return
				}
				_ = pipeline.Ingest(appCtx, msg)
			}
		}
	}()

	srv := server.NewWithDeps(config.Config{LogLevel: "error", LogFormat: "text"}, nil, server.Dependencies{
		Store:     storeRepo,
		Blobs:     blobRepo,
		Bus:       bus,
		Simulator: simCh,
	})
	defer func() { _ = srv.Shutdown(context.Background()) }()

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// 1. Verifica UIs estáticas
	for _, path := range []string{"/", "/thinking", "/static/styles.css", "/static/sim.js", "/static/thinking.js"} {
		resp, err := ts.Client().Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s failed: %v", path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s expected 200, got %d", path, resp.StatusCode)
		}
	}

	// 2. Abre stream SSE antes de enviar a mensagem
	sseCtx, sseCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer sseCancel()
	sseReq, _ := http.NewRequestWithContext(sseCtx, http.MethodGet, ts.URL+"/api/events?tenant=demo&channel=simulator&source=e2e-user", nil)
	sseResp, err := ts.Client().Do(sseReq)
	if err != nil {
		t.Fatalf("SSE connect failed: %v", err)
	}
	defer sseResp.Body.Close()

	// Aguarda linha ": connected"
	reader := bufio.NewReader(sseResp.Body)
	line, err := reader.ReadString('\n')
	if err != nil || !strings.HasPrefix(line, ": connected") {
		t.Fatalf("expected SSE connected comment, got %q (err=%v)", line, err)
	}

	// 3. Envia mensagem como cliente via API HTTP
	postBody, _ := json.Marshal(map[string]string{
		"tenant_id":  "demo",
		"source_key": "e2e-user",
		"text":       "Meu orçamento é 5000, qual plano?",
	})
	postResp, err := ts.Client().Post(ts.URL+"/api/sim/messages", "application/json", bytes.NewReader(postBody))
	if err != nil {
		t.Fatalf("POST /api/sim/messages failed: %v", err)
	}
	_ = postResp.Body.Close()
	if postResp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted, got %d", postResp.StatusCode)
	}

	// 4. Coleta eventos SSE até reply.sent
	var seenEvents []string
	for {
		l, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("SSE read error before reply.sent (seen=%v): %v", seenEvents, err)
		}
		if strings.HasPrefix(l, "event: ") {
			evName := strings.TrimSpace(strings.TrimPrefix(l, "event: "))
			seenEvents = append(seenEvents, evName)
			if evName == "reply.sent" {
				break
			}
		}
	}

	expectedSequence := []string{"message.received", "analysis.started", "prompt.sent", "llm.responded", "facts.diff", "analysis.completed", "prompt.sent", "llm.responded", "reply.generated", "reply.sent"}
	for _, exp := range expectedSequence {
		found := false
		for _, got := range seenEvents {
			if got == exp {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected SSE event %q in %v", exp, seenEvents)
		}
	}

	// 5. Verifica estado estruturado na API
	stateResp, err := ts.Client().Get(ts.URL + "/api/sources/simulator/e2e-user/state?tenant=demo")
	if err != nil {
		t.Fatal(err)
	}
	rawState, _ := io.ReadAll(stateResp.Body)
	_ = stateResp.Body.Close()
	var state core.SourceState
	if err := json.Unmarshal(rawState, &state); err != nil {
		t.Fatal(err)
	}
	if state.Facts["Budget"] != "5000" {
		t.Fatalf("expected extracted Budget 5000, got %v", state.Facts)
	}

	// 6. Verifica mensagens (inbound + outbound)
	msgsResp, err := ts.Client().Get(ts.URL + "/api/sources/simulator/e2e-user/messages?tenant=demo")
	if err != nil {
		t.Fatal(err)
	}
	var msgsBody struct {
		Messages []struct {
			Direction string `json:"direction"`
			Text      string `json:"text"`
		} `json:"messages"`
	}
	_ = json.NewDecoder(msgsResp.Body).Decode(&msgsBody)
	_ = msgsResp.Body.Close()
	if len(msgsBody.Messages) != 2 {
		t.Fatalf("expected 2 messages (inbound + outbound), got %d", len(msgsBody.Messages))
	}

	// 7. Verifica runs e trace completo no BlobStore
	runsResp, err := ts.Client().Get(ts.URL + "/api/sources/simulator/e2e-user/runs?tenant=demo")
	if err != nil {
		t.Fatal(err)
	}
	var runsBody struct {
		Runs []core.Run `json:"runs"`
	}
	_ = json.NewDecoder(runsResp.Body).Decode(&runsBody)
	_ = runsResp.Body.Close()
	if len(runsBody.Runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runsBody.Runs))
	}

	traceResp, err := ts.Client().Get(ts.URL + "/api/runs/" + runsBody.Runs[0].ID + "/trace?channel=simulator&source=e2e-user&tenant=demo")
	if err != nil {
		t.Fatal(err)
	}
	var trace brain.RunTrace
	_ = json.NewDecoder(traceResp.Body).Decode(&trace)
	_ = traceResp.Body.Close()
	if traceResp.StatusCode != http.StatusOK || trace.Analysis == nil || trace.Reply == nil || len(trace.FactsDiff) == 0 {
		t.Fatalf("unexpected trace response (status=%d): %+v", traceResp.StatusCode, trace)
	}
}

func TestAPI_SourceStatusAndTenantPolicy(t *testing.T) {
	storeRepo := fakes.NewMemoryStore()
	bus := core.NewInMemoryEventBus(16)
	srv := server.NewWithDeps(config.LoadFromEnv(), nil, server.Dependencies{
		Store: storeRepo,
		Blobs: fakes.NewMemoryBlobStore(),
		Bus:   bus,
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// 1. Atualiza status para paused e depois para active
	resp, err := ts.Client().Post(ts.URL+"/api/sources/simulator/u-api/status", "application/json",
		strings.NewReader(`{"tenant_id":"demo","status":"paused","reason":"teste manual"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 updating status, got %d", resp.StatusCode)
	}
	state, _ := storeRepo.GetSourceState(context.Background(), "demo", "simulator", "u-api")
	if state.Meta.Status != core.SourceStatusPaused {
		t.Fatalf("expected paused status, got %s", state.Meta.Status)
	}

	// Status inválido retorna 400
	badResp, err := ts.Client().Post(ts.URL+"/api/sources/simulator/u-api/status", "application/json",
		strings.NewReader(`{"tenant_id":"demo","status":"invalid_xyz"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = badResp.Body.Close()
	if badResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid status, got %d", badResp.StatusCode)
	}

	// 2. PUT & GET /api/tenants/demo/policy
	putReq, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/tenants/demo/policy",
		strings.NewReader(`{"name":"Demo","pack":"sample","mode":"approval","bot_enabled":false}`))
	putReq.Header.Set("Content-Type", "application/json")
	putResp, err := ts.Client().Do(putReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = putResp.Body.Close()
	if putResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on PUT policy, got %d", putResp.StatusCode)
	}

	getResp, err := ts.Client().Get(ts.URL + "/api/tenants/demo/policy")
	if err != nil {
		t.Fatal(err)
	}
	var gotPol core.TenantPolicy
	_ = json.NewDecoder(getResp.Body).Decode(&gotPol)
	_ = getResp.Body.Close()
	if gotPol.Mode != core.ModeApproval || gotPol.BotEnabled {
		t.Fatalf("unexpected policy returned: %+v", gotPol)
	}
}
