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
	"whatshelper/internal/channels/admin"
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
	if len(runsBody.Runs) != 1 || runsBody.Runs[0].TTL <= clock.Now().Unix() {
		t.Fatalf("expected 1 run with future TTL, got %+v", runsBody.Runs)
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

	// 3. Lista tarefas pendentes + por fonte e conclui via API
	dueAt := time.Now().UTC()
	_ = storeRepo.SaveTask(context.Background(), core.Task{
		ID: "task-api-1", TenantID: "demo", Channel: "simulator", SourceKey: "u-api",
		Kind: "task", Title: "Ligar para cliente", Type: "callback", Status: core.TaskStatusPending,
		DueAt: &dueAt, CreatedAt: dueAt,
	})

	pendingResp, err := ts.Client().Get(ts.URL + "/api/tasks/pending?tenant=demo")
	if err != nil {
		t.Fatal(err)
	}
	var pendingBody struct {
		Tasks []core.Task `json:"tasks"`
	}
	_ = json.NewDecoder(pendingResp.Body).Decode(&pendingBody)
	_ = pendingResp.Body.Close()
	if len(pendingBody.Tasks) != 1 || pendingBody.Tasks[0].ID != "task-api-1" {
		t.Fatalf("expected 1 pending task, got %+v", pendingBody.Tasks)
	}

	compResp, err := ts.Client().Post(ts.URL+"/api/tasks/task-api-1/complete?tenant=demo", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = compResp.Body.Close()
	if compResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 completing task, got %d", compResp.StatusCode)
	}

	srcTasksResp, err := ts.Client().Get(ts.URL + "/api/sources/simulator/u-api/tasks?tenant=demo")
	if err != nil {
		t.Fatal(err)
	}
	var srcTasksBody struct {
		Tasks []core.Task `json:"tasks"`
	}
	_ = json.NewDecoder(srcTasksResp.Body).Decode(&srcTasksBody)
	_ = srcTasksResp.Body.Close()
	if len(srcTasksBody.Tasks) != 1 || srcTasksBody.Tasks[0].Status != core.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %+v", srcTasksBody.Tasks)
	}

	// 4. Testa POST /api/tasks/{id}/approve com Pipeline conectado
	blobs := fakes.NewMemoryBlobStore()
	simCh := simulator.New(core.SystemClock{}, 8)
	_ = simCh.Start(context.Background())
	defer func() { _ = simCh.Stop(context.Background()) }()
	pipe := brain.NewPipeline(context.Background(), brain.PipelineConfig{
		Store: storeRepo, Blobs: blobs, LLM: fakes.NewOfflineDemoLLM(), Bus: bus,
		Tenants: brain.NewStoreTenantPolicyLoader(storeRepo, nil),
		Knowledge: brain.NewLocalKnowledgeSource(t.TempDir()),
		PackLoader: func(name string) (*pack.Pack, error) { return nil, nil },
	})
	pipe.RegisterChannel(simCh)
	defer pipe.Stop()

	srvAppr := server.NewWithDeps(config.LoadFromEnv(), nil, server.Dependencies{
		Store: storeRepo, Blobs: blobs, Bus: bus, Simulator: simCh, Pipeline: pipe,
	})
	tsAppr := httptest.NewServer(srvAppr.Handler())
	defer tsAppr.Close()

	_ = blobs.Put(context.Background(), "demo/drafts/simulator/u-api/r1.json", []byte(`{"run_id":"r1","tenant_id":"demo","channel":"simulator","source_key":"u-api","business_actor":"agent","text":"Rascunho original"}`), "application/json")
	_ = storeRepo.SaveTask(context.Background(), core.Task{
		ID: "appr-api-1", TenantID: "demo", Channel: "simulator", SourceKey: "u-api",
		Kind: "approval", Title: "Aprovar", Type: "reply_approval", Status: core.TaskStatusPending,
		DraftBlobRef: "demo/drafts/simulator/u-api/r1.json", DueAt: &dueAt, CreatedAt: dueAt,
	})

	apprResp, err := tsAppr.Client().Post(tsAppr.URL+"/api/tasks/appr-api-1/approve?tenant=demo", "application/json", strings.NewReader(`{"text":"Texto aprovado!"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = apprResp.Body.Close()
	if apprResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 approving task, got %d", apprResp.StatusCode)
	}

	// 5. Testa leitura GSI1 de fontes vinculadas a um contato (STO-06)
	_ = storeRepo.LinkSourceToContact(context.Background(), "demo", "simulator", "u-api", "contact-e2e")
	contactResp, err := ts.Client().Get(ts.URL + "/api/contacts/contact-e2e/sources?tenant=demo")
	if err != nil {
		t.Fatal(err)
	}
	var contactBody struct {
		Sources []core.SourceMeta `json:"sources"`
	}
	_ = json.NewDecoder(contactResp.Body).Decode(&contactBody)
	_ = contactResp.Body.Close()
	if contactResp.StatusCode != http.StatusOK || len(contactBody.Sources) != 1 || contactBody.Sources[0].SourceKey != "u-api" {
		t.Fatalf("expected 1 linked source via GSI1 endpoint, got status=%d body=%+v", contactResp.StatusCode, contactBody)
	}

	// 6. Testa API de conhecimento (PUT + GET + versions + proteção path traversal) e página /knowledge
	putKReq1, _ := http.NewRequest(http.MethodPut, tsAppr.URL+"/api/knowledge/guia.md",
		strings.NewReader(`{"tenant_id":"demo","content":"# Guia v1"}`))
	putKResp1, err := tsAppr.Client().Do(putKReq1)
	if err != nil || putKResp1.StatusCode != http.StatusOK {
		t.Fatalf("PUT knowledge v1 failed: status=%v err=%v", putKResp1, err)
	}
	_ = putKResp1.Body.Close()

	putKReq2, _ := http.NewRequest(http.MethodPut, tsAppr.URL+"/api/knowledge/guia.md",
		strings.NewReader(`{"tenant_id":"demo","content":"# Guia v2"}`))
	putKResp2, _ := tsAppr.Client().Do(putKReq2)
	_ = putKResp2.Body.Close()

	verResp, err := tsAppr.Client().Get(tsAppr.URL + "/api/knowledge/guia.md/versions?tenant=demo")
	if err != nil {
		t.Fatal(err)
	}
	var verBody struct {
		Versions []core.BlobVersion `json:"versions"`
	}
	_ = json.NewDecoder(verResp.Body).Decode(&verBody)
	_ = verResp.Body.Close()
	if verResp.StatusCode != http.StatusOK || len(verBody.Versions) != 2 {
		t.Fatalf("expected 2 knowledge versions, got status=%d body=%+v", verResp.StatusCode, verBody)
	}

	// Busca versão antiga v1
	oldResp, err := tsAppr.Client().Get(tsAppr.URL + "/api/knowledge/guia.md?tenant=demo&version_id=v1")
	if err != nil {
		t.Fatal(err)
	}
	var oldDoc struct {
		Content string `json:"content"`
	}
	_ = json.NewDecoder(oldResp.Body).Decode(&oldDoc)
	_ = oldResp.Body.Close()
	if oldDoc.Content != "# Guia v1" {
		t.Fatalf("expected v1 content '# Guia v1', got %q", oldDoc.Content)
	}

	uiResp, err := tsAppr.Client().Get(tsAppr.URL + "/knowledge")
	if err != nil || uiResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /knowledge failed: err=%v", err)
	}
	_ = uiResp.Body.Close()
}

func TestE2E_MultiPack_RealestateAndSupport(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 11, 14, 0, 0, 0, time.UTC)
	clock := fakes.NewFakeClock(now)
	storeRepo := fakes.NewMemoryStoreWithClock(clock)
	blobs := fakes.NewMemoryBlobStore()
	bus := core.NewInMemoryEventBus(64)

	tenantsDir := filepath.Join("..", "..", "tenants")
	packsDir := filepath.Join("..", "..", "packs")
	if _, err := brain.SeedLocalTenants(ctx, tenantsDir, storeRepo); err != nil {
		t.Fatalf("SeedLocalTenants failed: %v", err)
	}
	if _, err := brain.SeedLocalKnowledge(ctx, tenantsDir, blobs); err != nil {
		t.Fatalf("SeedLocalKnowledge failed: %v", err)
	}

	ch := fakes.NewMemoryChannel("simulator", 16)
	_ = ch.Start(ctx)

	p := brain.NewPipeline(ctx, brain.PipelineConfig{
		Store:         storeRepo,
		Blobs:         blobs,
		LLM:           fakes.NewOfflineDemoLLM(),
		Bus:           bus,
		Clock:         clock,
		Tenants:       brain.NewStoreTenantPolicyLoader(storeRepo, nil),
		Knowledge:     brain.NewBlobKnowledgeSource(blobs, clock, 5*time.Second),
		PackLoader:    func(name string) (*pack.Pack, error) { return pack.LoadFromDir(filepath.Join(packsDir, name)) },
		AnalysisModel: "fake-a",
		ResponseModel: "fake-r",
	})
	p.RegisterChannel(ch)
	defer p.Stop()

	// Processa mensagem no tenant "demo" (pack realestate) e em "demo-support" (pack support)
	msgs := []core.NormalizedMessage{
		{
			TenantID: "demo", Channel: "simulator", SourceKey: "u-re",
			MessageID: "m-re-1", Timestamp: now, Direction: core.DirectionInbound, Type: core.MessageTypeText,
			Text: "Qual o orçamento 500000 disponível?",
		},
		{
			TenantID: "demo-support", Channel: "simulator", SourceKey: "u-sup",
			MessageID: "m-sup-1", Timestamp: now, Direction: core.DirectionInbound, Type: core.MessageTypeText,
			Text: "Exportação de 50000 linhas deu erro no plano Starter!",
		},
	}
	for _, m := range msgs {
		if err := p.ProcessBatch(ctx, []core.NormalizedMessage{m}); err != nil {
			t.Fatalf("ProcessBatch(%s) failed: %v", m.TenantID, err)
		}
	}

	if len(ch.SentMessages()) != 2 {
		t.Fatalf("expected 2 outbound replies across both packs, got %d", len(ch.SentMessages()))
	}
	stRe, _ := storeRepo.GetSourceState(ctx, "demo", "simulator", "u-re")
	stSup, _ := storeRepo.GetSourceState(ctx, "demo-support", "simulator", "u-sup")
	if len(stRe.Facts) == 0 {
		t.Fatalf("expected at least 1 extracted fact for demo, got %+v", stRe.Facts)
	}
	if stSup.Facts["SLAHours"] != "50000" {
		t.Fatalf("expected demo-support fact SLAHours=50000, got %+v", stSup.Facts)
	}

	// Testa POST /api/replay (SRV-06): deve rodar em MemoryStore isolado sem mutar u-sup no storeRepo real
	srvReplay := server.NewWithDeps(config.LoadFromEnv(), nil, server.Dependencies{
		Store: storeRepo, Blobs: blobs, Bus: bus, Pipeline: p,
	})
	tsReplay := httptest.NewServer(srvReplay.Handler())
	defer tsReplay.Close()

	repResp, err := tsReplay.Client().Post(tsReplay.URL+"/api/replay", "application/json",
		strings.NewReader(`{"tenant_id":"demo-support","channel":"simulator","source_key":"u-sup","messages":[{"text":"Qual o limite de 99999 linhas?"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var repResult brain.ReplayResult
	_ = json.NewDecoder(repResp.Body).Decode(&repResult)
	_ = repResp.Body.Close()
	if repResp.StatusCode != http.StatusOK || len(repResult.Turns) != 1 || repResult.Turns[0].ReplyText == "" {
		t.Fatalf("unexpected replay result: status=%d res=%+v", repResp.StatusCode, repResult)
	}

	// Store real deve permanecer com apenas 1 run (sem poluição pelo replay)
	realRuns, _ := storeRepo.ListRuns(ctx, "demo-support", "simulator", "u-sup", 10)
	if len(realRuns) != 1 {
		t.Fatalf("expected real store untouched by replay (1 run), got %d", len(realRuns))
	}

	// Testa /api/admin/messages (CH-08)
	admCh := admin.New(clock, 8)
	_ = admCh.Start(ctx)
	srvAdm := server.NewWithDeps(config.LoadFromEnv(), nil, server.Dependencies{
		Store: storeRepo, Blobs: blobs, Bus: bus, AdminChannel: admCh, Pipeline: p,
	})
	tsAdm := httptest.NewServer(srvAdm.Handler())
	defer tsAdm.Close()

	admPost, err := tsAdm.Client().Post(tsAdm.URL+"/api/admin/messages", "application/json",
		strings.NewReader(`{"tenant_id":"demo-support","text":"/aprovar prop-1"}`))
	if err != nil || admPost.StatusCode != http.StatusAccepted {
		t.Fatalf("POST /api/admin/messages failed: resp=%v err=%v", admPost, err)
	}
	_ = admPost.Body.Close()

	select {
	case admMsg := <-admCh.Messages():
		if err := p.Ingest(ctx, admMsg); err != nil {
			t.Fatalf("Ingest admin message failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting admin channel inbound message")
	}

	admGet, err := tsAdm.Client().Get(tsAdm.URL + "/api/admin/messages?tenant=demo-support")
	if err != nil {
		t.Fatal(err)
	}
	var admBody struct {
		Messages []struct {
			Direction string `json:"direction"`
			Text      string `json:"text"`
		} `json:"messages"`
	}
	_ = json.NewDecoder(admGet.Body).Decode(&admBody)
	_ = admGet.Body.Close()
	if admGet.StatusCode != http.StatusOK || len(admBody.Messages) < 2 {
		t.Fatalf("expected admin inbound+outbound messages, got status=%d body=%+v", admGet.StatusCode, admBody)
	}
}
