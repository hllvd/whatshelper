# FEATURE — Provedor OpenAI Direto, Roteamento Dinâmico (On the Fly), Presets e Multimodalidade (Visão + Áudio)

## Visão Geral

Esta entrega adiciona ao **Whatshelper Brain**:
1. **Provedor OpenAI Direto (`LLM_PROVIDER=openai`)**, somando-se a `openrouter`, `anthropic`, `gemini`, `deepseek` e `fake`.
2. **Roteamento Dinâmico Multi-Provedor (`DynamicRouter`)** com troca de provedor e modelo **em tempo real ("on the fly")** por estágio (`analysis`, `response`, `nudge`, `learning`, `vision`, `audio`) sem reiniciar o serviço.
3. **Presets Recomendados de Modelos** expostos via API e UI, com suporte a seleção rápida ou digitação de qualquer ID de modelo customizado.
4. **Processamento Multimodal**:
   - **Visão (`vision`)**: imagens recebidas (`MessageTypeImage`) são baixadas do BlobStore, enviadas ao provedor/modelo de visão configurado e convertidas em descrição objetiva (`[Imagem descrita]: ...`) antes da análise e resposta.
   - **Áudio (`audio`)**: transcrição dinâmica (`DynamicAudioTranscriber`) suportando tanto `TRANSCRIPTION_URL` customizada quanto OpenAI `/audio/transcriptions` direto (`whisper-1`, `gpt-4o-transcribe`, etc.) com override por tenant em tempo real.

---

## Arquitetura Implementada

### 1. Provedor OpenAI Direto & Multimodalidade OpenAI-Compatible
- **Arquivo**: [`internal/llm/openai/client.go`](internal/llm/openai/client.go)
  - Cliente direto usando `DefaultBaseURL = "https://api.openai.com/v1"` sobre [`internal/llm/openaicompat/client.go`](internal/llm/openaicompat/client.go).
- **Arquivo**: [`internal/llm/openaicompat/client.go`](internal/llm/openaicompat/client.go)
  - Suporte a `MediaParts` (`openai.ImageContentPart` com data URI base64 + `openai.TextContentPart`).
  - Suporte a `response_format: json_schema` para `openai` e `openrouter` quando `JSONSchema` é fornecido (com fallback para `json_object`).
  - Retorno de `Provider` e `Model` em `core.LLMResponse`.
- **Arquivo**: [`internal/config/config.go`](internal/config/config.go)
  - Novas variáveis: `OPENAI_API_KEY`, `OPENAI_BASE_URL`, `MODEL_VISION`, `MODEL_AUDIO`.
  - Inferência automática de `LLM_PROVIDER=openai` quando `OPENAI_API_KEY` está presente e nenhum outro provedor foi explicitado.
  - Mascaramento automático de `OPENAI_API_KEY` em logs via `Secrets()`.

### 2. Roteamento Dinâmico e Presets
- **Arquivo**: [`internal/llm/router.go`](internal/llm/router.go)
  - `NewRouterFromConfig`: inicializa o provedor padrão e registra automaticamente todos os provedores cujas chaves estejam no `.env` (`openai`, `openrouter`, `anthropic`, `gemini`, `deepseek`, além de `fake`).
  - `Resolve(stage, overrides)`: resolve `{provider, model}` na ordem: `TenantPolicy.LLMOverrides[stage]` → default do estágio no `.env` → `defaultProvider`.
  - `Generate(ctx, req)`: despacha para o cliente correspondente a `req.Provider`.
  - `Presets()`: catálogo de modelos recomendados por provedor com tags de capacidade (`text`, `thinking`, `vision`, `audio`).
- **Arquivo**: [`internal/core/policy.go`](internal/core/policy.go)
  - `StageLLMConfig{Provider, Model}` e `TenantPolicy.LLMOverrides map[string]StageLLMConfig` persistidos no DynamoDB (`CONFIG#policy`) e suportados via `tenant.yaml` (`llm_overrides`).

### 3. Pipeline Multimodal (Visão + Áudio) e Resolução por Estágio
- **Arquivo**: [`internal/brain/pipeline.go`](internal/brain/pipeline.go)
  - `resolveStageLLM` aplicado em `analysis`, `response`, `vision`, `audio` e [`internal/brain/nudge.go`](internal/brain/nudge.go) (`nudge`).
  - `describeImageMessage`: quando uma imagem chega sem texto, carrega os bytes do BlobStore, chama o estágio `vision` com `MediaParts` e injeta `[Imagem descrita]: <descrição>` no texto da mensagem (fallback seguro para `[Imagem recebida]`).
- **Arquivo**: [`internal/media/transcriber.go`](internal/media/transcriber.go)
  - `DynamicAudioTranscriber` com `TranscribeWithConfig(ctx, audio, mimeType, provider, model)` escolhendo entre OpenAI `/audio/transcriptions` direto e `TRANSCRIPTION_URL` customizada conforme o override do estágio `audio`.

### 4. API HTTP & Interface Web ("On the Fly")
- **Arquivo**: [`internal/server/llm_config.go`](internal/server/llm_config.go)
  - `GET /api/llm/presets`: lista provedores ativos (sem expor chaves) e presets de modelos.
  - `GET /api/llm/config?tenant=<id>`: retorna defaults do `.env`, overrides do tenant e configuração efetiva por estágio.
  - `PUT /api/llm/config?tenant=<id>`: valida estágios e provedores ativos, atualiza `TenantPolicy.LLMOverrides` no `Store` e aplica imediatamente nas próximas mensagens.
- **UI**: [`internal/server/static/thinking.html`](internal/server/static/thinking.html) e [`internal/server/static/thinking.js`](internal/server/static/thinking.js)
  - Painel **Modelos LLM (On the Fly)** na tela `/thinking` para selecionar provedor e modelo (via preset ou texto livre) para `Análise / Thinking`, `Resposta`, `Visão (Imagens)` e `Áudio (Transcrição)`.

---

## Como Usar

1. Configure uma ou mais chaves no `.env` (ex.: `OPENAI_API_KEY`, `OPENROUTER_API_KEY`, `GEMINI_API_KEY`, `ANTHROPIC_API_KEY`) e os modelos padrão (`MODEL_ANALYSIS`, `MODEL_RESPONSE`, `MODEL_NUDGE`, `MODEL_VISION`, `MODEL_AUDIO`).
2. Inicie o brain (`make run` ou `docker compose up`).
3. Abra `/thinking` na UI web ou chame `PUT /api/llm/config?tenant=demo` para trocar o modelo de qualquer estágio em tempo real sem reiniciar o servidor:

```bash
curl -X PUT "http://127.0.0.1:8080/api/llm/config?tenant=demo" \
  -H "Content-Type: application/json" \
  -d '{
    "overrides": {
      "analysis": {"provider": "openai", "model": "o4-mini"},
      "vision": {"provider": "gemini", "model": "gemini-2.5-flash"},
      "audio": {"provider": "openai", "model": "gpt-4o-transcribe"}
    }
  }'
```

---

## Verificação
- Testes unitários adicionados em:
  - [`internal/llm/openai/client_test.go`](internal/llm/openai/client_test.go)
  - [`internal/llm/router_test.go`](internal/llm/router_test.go)
  - [`internal/media/transcriber_test.go`](internal/media/transcriber_test.go)
  - [`internal/brain/llm_routing_test.go`](internal/brain/llm_routing_test.go)
  - [`internal/server/llm_config_test.go`](internal/server/llm_config_test.go)
- Suite completa validada com `make test` e `./scripts/tests.sh all`.

---

## 2026-11-13 — Workflows FSM Parametrizáveis por Pack + Seletor na UI (`/journey`)

### Itens Implementados
- **Campo `workflow` em [`TenantPolicy`](file:///Users/hudson/development/whatshelper/internal/core/policy.go#L74-L88)**: padrão `"support"`, configurável em `tenant.yaml` e persistido no DynamoDB via `PUT /api/tenants/{id}/policy`.
- **Múltiplas jornadas por pack**:
  - [`LoadJourneysFromDir`](file:///Users/hudson/development/whatshelper/internal/pack/journey.go) e [`Pack.ResolveJourney`](file:///Users/hudson/development/whatshelper/internal/pack/pack.go) carregam e validam `packs/<pack>/journeys/*.yaml` (com fallback para `"support"` e para o `journey.yaml` legado).
  - Jornadas criadas em `packs/realestate/journeys/` (`support.yaml`, `leads.yaml`, `hybrid.yaml`) e `packs/support/journeys/support.yaml`.
- **Reset seguro de estado em [`NextJourney`](file:///Users/hudson/development/whatshelper/internal/brain/journey.go)**: quando uma conversa em andamento possui estado que não existe no novo workflow ativo, o FSM reinicia automaticamente para `initial_state` da nova jornada.
- **API & UI (`/journey`)**:
  - `GET /api/workflows?tenant=<id>` em [`internal/server/journey_api.go`](file:///Users/hudson/development/whatshelper/internal/server/journey_api.go) lista os workflows disponíveis no pack do tenant.
  - `GET /api/sources/{channel}/{key}/journey`, `POST /api/journey/what-if` e `GET /api/journey/funnel` passam a usar `pk.ResolveJourney(policy.Workflow)`.
  - Seletor **Workflow Ativo (FSM)** adicionado em [`journey.html`](file:///Users/hudson/development/whatshelper/internal/server/static/journey.html) e [`journey.js`](file:///Users/hudson/development/whatshelper/internal/server/static/journey.js), salvando via `PUT /api/tenants/{id}/policy` e recarregando o diagrama Mermaid em tempo real.
- **Testes**:
  - [`internal/pack/journey_multi_test.go`](file:///Users/hudson/development/whatshelper/internal/pack/journey_multi_test.go)
  - [`internal/brain/journey_workflow_test.go`](file:///Users/hudson/development/whatshelper/internal/brain/journey_workflow_test.go)
  - [`internal/server/workflows_api_test.go`](file:///Users/hudson/development/whatshelper/internal/server/workflows_api_test.go)

---

## 2026-11-13 — Debounce Configurável por Pack, Espera por Digitação (`typing`) e Janela de Contexto (5 Cliente / 10 Total)

### Itens Implementados
- **Configuração por Pack (`timing`)**:
  - [`TimingDef`](file:///Users/hudson/development/whatshelper/internal/pack/pack.go) e [`Pack.EffectiveTiming`](file:///Users/hudson/development/whatshelper/internal/pack/pack.go) adicionados em `pack.yaml` com padrões `debounce_ms: 8000` (8s) e `typing_max_wait_ms: 16000` (16s).
  - Fallback global em [`config.go`](file:///Users/hudson/development/whatshelper/internal/config/config.go) atualizado para `DEBOUNCE_MS=8000` e `RECENT_MESSAGES_LIMIT=10`.
- **Debounce + Standby por Digitação no [`SourceDispatcher`](file:///Users/hudson/development/whatshelper/internal/brain/scheduler.go)**:
  - Aguarda 8s de inatividade por padrão; se o cliente estiver digitando (`SetTyping(..., true)`), segura o processamento até parar de digitar ou atingir o teto máximo de 16s.
  - Liberação imediata quando `typing=false` chega após o vencimento do debounce; novas mensagens reiniciam a janela de 8s (e mensagens após resposta disparam um novo ciclo completo).
  - [`Pipeline.NotifyTyping`](file:///Users/hudson/development/whatshelper/internal/brain/pipeline.go) resolve os tempos a partir do pack do tenant.
- **Detecção de `typing` no WhatsApp e Simulador**:
  - WhatsApp ([`adapter.go`](file:///Users/hudson/development/whatshelper/internal/channels/whatsapp/adapter.go)): escuta `*events.ChatPresence` (`Composing` / `Paused`) e encaminha ao Brain via `POST /api/connectors/v1/typing` ([`protocol.go`](file:///Users/hudson/development/whatshelper/internal/channels/bridge/protocol.go), [`client.go`](file:///Users/hudson/development/whatshelper/internal/channels/bridge/client.go), [`connector.go`](file:///Users/hudson/development/whatshelper/internal/server/connector.go)).
  - Simulador ([`api.go`](file:///Users/hudson/development/whatshelper/internal/server/api.go), [`sim.js`](file:///Users/hudson/development/whatshelper/internal/server/static/sim.js)): novo endpoint `POST /api/sim/typing` integrado ao campo de texto da UI com envio de `typing=true/false`.
- **Janela de Contexto para o LLM (5 cliente / 10 total)**:
  - [`selectRecentMessages`](file:///Users/hudson/development/whatshelper/internal/contextmod/builtin_providers.go) e padrão `RecentMessagesLimit=10` garantem até 10 mensagens recentes (robô + cliente) preservando sempre até as últimas 5 mensagens do cliente em ordem cronológica.
- **Testes**:
  - [`internal/brain/scheduler_test.go`](file:///Users/hudson/development/whatshelper/internal/brain/scheduler_test.go)
  - [`internal/pack/pack_test.go`](file:///Users/hudson/development/whatshelper/internal/pack/pack_test.go)
  - [`internal/contextmod/builtin_providers_test.go`](file:///Users/hudson/development/whatshelper/internal/contextmod/builtin_providers_test.go)
  - [`internal/server/connector_test.go`](file:///Users/hudson/development/whatshelper/internal/server/connector_test.go)

---

## 2026-11-15 — M4g: Núcleo Puro de Janela/Lote (`WIN-01..WIN-04`)

### Itens Implementados
- **`WIN-01` — ADR 0021**: [`docs/adr/0021-janela-de-contexto-e-lote.md`](file:///Users/hudson/development/whatshelper/docs/adr/0021-janela-de-contexto-e-lote.md) especificando janela lote vs. histórico, rotulagem e delta de tempo, debounce adaptativo, guarda contra resposta defasada, coleções de fatos, múltiplos intents, edição/exclusão (`whatsmeow`) e assinaturas das funções puras.
- **`WIN-02` — `BuildWindow` puro**: [`internal/contextmod/window.go`](file:///Users/hudson/development/whatshelper/internal/contextmod/window.go) e integração em [`internal/brain/context_adapter.go`](file:///Users/hudson/development/whatshelper/internal/brain/context_adapter.go) (separa `History` [10 totais / 5 cliente] e `Batch` [nunca cortado], rotula `Role` e `DeltaFromPrev`, aplica orçamento de tokens e persiste `Window` no `RunTrace`).
- **`WIN-03` — Debounce adaptativo puro (`NextDebounceDecision` & `IsIncompleteFragment`)**: [`internal/brain/debounce_adaptive.go`](file:///Users/hudson/development/whatshelper/internal/brain/debounce_adaptive.go) acoplado ao [`SourceDispatcher`](file:///Users/hudson/development/whatshelper/internal/brain/scheduler.go).
- **`WIN-04` — Guarda contra resposta defasada (`ShouldDiscardReply`)**: [`internal/brain/stale_guard.go`](file:///Users/hudson/development/whatshelper/internal/brain/stale_guard.go) integrado ao `ProcessBatch` ([`internal/brain/pipeline.go`](file:///Users/hudson/development/whatshelper/internal/brain/pipeline.go)) com drenagem atômica (`TakePending`), cancelamento de outbox pendente e evento `reply_discarded_stale`.
- **Testes (`WIN-09` M4g)**: [`internal/contextmod/window_test.go`](file:///Users/hudson/development/whatshelper/internal/contextmod/window_test.go), [`internal/brain/debounce_adaptive_test.go`](file:///Users/hudson/development/whatshelper/internal/brain/debounce_adaptive_test.go), [`internal/brain/stale_guard_test.go`](file:///Users/hudson/development/whatshelper/internal/brain/stale_guard_test.go) e [`internal/brain/m4g_window_debounce_test.go`](file:///Users/hudson/development/whatshelper/internal/brain/m4g_window_debounce_test.go).

---

## 2026-11-15 — M4h: Coleções de Fatos, Múltiplos Intents, Edição/Exclusão, Cartão UI `/thinking` e Integração (`WIN-05..WIN-09`)

### Itens Implementados
- **`WIN-05` — Coleções de Fatos (`MergeAndDiffCollections`)**: [`CollectionDef`](file:///Users/hudson/development/whatshelper/internal/pack/pack.go) em `pack.yaml` e função pura [`MergeAndDiffCollections`](file:///Users/hudson/development/whatshelper/internal/brain/collections.go) com merge por `id` ou chave natural, remoções explícitas (`_delete: true`) e idempotência.
- **`WIN-06` — Múltiplos Intents por Lote (`AggregateBatchIntents`)**: [`internal/brain/batch_intents.go`](file:///Users/hudson/development/whatshelper/internal/brain/batch_intents.go) consolidando intents únicos, ação dominante (`opt_out > handoff > escalate > create_task > reply`) e limite de 1 pergunta de qualificação por resposta.
- **`WIN-07` — Edição e Exclusão de Mensagens (`ApplyMessageEdit`)**: [`internal/brain/message_edit.go`](file:///Users/hudson/development/whatshelper/internal/brain/message_edit.go) e [`SourceDispatcher.ApplyMutation`](file:///Users/hudson/development/whatshelper/internal/brain/scheduler.go) aplicando edições/exclusões no lote pendente antes do flush ou sinalizando reabertura conforme política.
- **`WIN-08` — Observabilidade e Cartão "Janela e Lote" no `/thinking`**: [`internal/server/static/thinking.js`](file:///Users/hudson/development/whatshelper/internal/server/static/thinking.js) exibindo lote agrupado, histórico (5 cliente / 10 total), papéis, deltas de tempo, itens cortados, reprocessamentos e respostas descartadas.
- **`WIN-09` — Testes Unitários e de Integração (`DynamoDB Local + MinIO`)**: [`internal/brain/collections_test.go`](file:///Users/hudson/development/whatshelper/internal/brain/collections_test.go), [`internal/brain/batch_intents_test.go`](file:///Users/hudson/development/whatshelper/internal/brain/batch_intents_test.go), [`internal/brain/message_edit_test.go`](file:///Users/hudson/development/whatshelper/internal/brain/message_edit_test.go) e [`internal/brain/integration_window_test.go`](file:///Users/hudson/development/whatshelper/internal/brain/integration_window_test.go).
