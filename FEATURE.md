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

---

## 2026-11-15 — Prevenção de Respostas Duplicadas em Rajadas + Fluxo "Consultor Saiu da Equipe" (Ademicon vs. Migração de Equipe)

### Itens Implementados
- **Prevenção de Respostas Duplicadas (`SourceDispatcher.flush` + `TakePending`)**:
  - **Correção de corrida de concorrência em [`SourceDispatcher.flush`](file:///Users/hudson/development/whatshelper/internal/brain/scheduler.go#L380-L421)**: `q.execMu.Lock()` passa a ser adquirido **antes** de drenar `q.pending`. Quando uma nova mensagem chega (e seu timer vence) enquanto uma chamada ao LLM já está em andamento, as mensagens permanecem em `q.pending` até que `TakePending()` e `ShouldDiscardReply()` no [`ProcessBatch`](file:///Users/hudson/development/whatshelper/internal/brain/pipeline.go) descartem a resposta defasada e consolidem tudo em **uma única resposta**.
  - **Teste de regressão**: [`TestM4g_Regression_TimerExpiresDuringLLMCall_StillSingleReply`](file:///Users/hudson/development/whatshelper/internal/brain/m4g_window_debounce_test.go#L319-L390).
  - **Alinhamento de Debounce (8s / 16s) e Janela (10 msgs)**: configurado em `.env`, [`.env.example`](file:///Users/hudson/development/whatshelper/.env.example), [`docker-compose.yml`](file:///Users/hudson/development/whatshelper/docker-compose.yml) e no bloco `timing` de [`packs/supporter/pack.yaml`](file:///Users/hudson/development/whatshelper/packs/supporter/pack.yaml), [`packs/support/pack.yaml`](file:///Users/hudson/development/whatshelper/packs/support/pack.yaml) e [`packs/realestate/pack.yaml`](file:///Users/hudson/development/whatshelper/packs/realestate/pack.yaml).
- **Desativação do Gate de Palavra-Chave Pré-LLM (`MatchesOptOutKeyword`)**:
  - [`EvaluatePreLLMGates`](file:///Users/hudson/development/whatshelper/internal/brain/gates.go#L33-L71) e [`MatchesOptOutKeyword`](file:///Users/hudson/development/whatshelper/internal/brain/gates.go#L75-L77) desativados para evitar falsos positivos de bloqueio (`status=opted_out`) em frases comuns do negócio que contêm verbos como `"sair"` ou `"parar"` (ex.: *"vai sair da empresa"*). O opt-out permanece contextual via intent `opt_out` do LLM.
  - Testes atualizados em [`internal/brain/gates_test.go`](file:///Users/hudson/development/whatshelper/internal/brain/gates_test.go) e [`testdata/conversations/realestate_optout_and_escalation.yaml`](file:///Users/hudson/development/whatshelper/testdata/conversations/realestate_optout_and_escalation.yaml).
- **Horário de Atendimento Todos os Dias (`05:00` às `22:00`, Seg a Dom)**:
  - Atualizado em [`tenants/supporter/tenant.yaml`](file:///Users/hudson/development/whatshelper/tenants/supporter/tenant.yaml), [`tenants/demo/tenant.yaml`](file:///Users/hudson/development/whatshelper/tenants/demo/tenant.yaml) e [`tenants/demo-support/tenant.yaml`](file:///Users/hudson/development/whatshelper/tenants/demo-support/tenant.yaml).
- **Regra e Base de Conhecimento — Consultor "Saiu da Equipe" (`supporter`)**:
  - **Regra explícita em [`packs/supporter/pack.yaml`](file:///Users/hudson/development/whatshelper/packs/supporter/pack.yaml#L144-L150)**: quando o usuário informa que um consultor "saiu da equipe" sem detalhar o motivo, o bot pergunta primeiro se o consultor **saiu da Ademicon (não trabalha mais na empresa)** ou se **migrou para outra equipe**:
    1. **Saiu da Ademicon (desligamento da empresa)**: orienta **somente** a desativar o usuário em **Usuários → Lista (`#/users`)** (ícone de lixeira → **Desativar**), sem alterar o Calendário de Equipes.
    2. **Saiu e migrou para outra equipe (ou virou gestor)**: orienta a ajustar a data de corte (**Data Final** na equipe antiga e **Data de Início** na nova) em **Equipes → Calendário (`#/teams/calendar`)**.
  - **How-Tos e QA atualizados**:
    - [`tenants/supporter/knowledge/como-ajustar-membros-equipe-calendario.md`](file:///Users/hudson/development/whatshelper/tenants/supporter/knowledge/como-ajustar-membros-equipe-calendario.md)
    - [`tenants/supporter/knowledge/como-gerenciar-usuarios.md`](file:///Users/hudson/development/whatshelper/tenants/supporter/knowledge/como-gerenciar-usuarios.md)
    - [`tenants/supporter/knowledge/como-gerenciar-equipes.md`](file:///Users/hudson/development/whatshelper/tenants/supporter/knowledge/como-gerenciar-equipes.md)
    - [`tenants/supporter/knowledge/QA.md`](file:///Users/hudson/development/whatshelper/tenants/supporter/knowledge/QA.md)

---

## 2026-11-15 — Especificação Técnica de APIs por Tenant (`tenants/supporter/apis/`) + Refinamento de QA de Contratos (Avapro / Filtros)

### Itens Implementados
- **Estrutura de Especificação de APIs por Tenant (`tenants/supporter/apis/`)**:
  - Criada pasta dedicada `tenants/supporter/apis/` (separada de `knowledge/` para evitar injeção de credenciais/rotas técnicas no prompt de FAQ dos usuários finais).
  - [`tenants/supporter/apis/README.md`](file:///Users/hudson/development/whatshelper/tenants/supporter/apis/README.md): instruções de uso e organização de arquivos `.md` e `.yml`/`.yaml` por serviço externo, sem campos obrigatórios.
  - [`tenants/supporter/apis/swagger-exemplo.yml`](file:///Users/hudson/development/whatshelper/tenants/supporter/apis/swagger-exemplo.yml): exemplo de especificação direta em arquivo `.yml` (Swagger / OpenAPI 3.0 flexível, sem campos obrigatórios, com bloco opcional `x-auth` para login/senha e descrição de endpoints).
  - [`tenants/supporter/apis/servico-exemplo.md`](file:///Users/hudson/development/whatshelper/tenants/supporter/apis/servico-exemplo.md): template descritivo livre em `.md` para informar autenticação (`Sim`/`Não`), `Login`, `Senha` e descrição de cada endpoint.
  - [`tenants/supporter/apis/swagger-exemplo.md`](file:///Users/hudson/development/whatshelper/tenants/supporter/apis/swagger-exemplo.md): template para documentação **Swagger / OpenAPI em `.md`**.
- **Melhoria de Respostas sobre Contratos e Sincronização com o Avapro (`supporter`)**:
  - **Contrato não encontrado no sistema**: orienta a verificar filtros ativos/mal formatados (`Limpar Filtros`) e explica a sincronização diária com o **Avapro (Ava Pro)** (prazo de até 24h para novos contratos).
  - **Contrato consta como cancelado mas já está pago/ativo**: orienta sobre as duas causas possíveis — ainda constar como cancelado no Avapro (acionar a **Ademicon**) ou mudança no Avapro há menos de 24h (aguardar a sincronização matinal).
  - Arquivos atualizados: [`tenants/supporter/knowledge/QA.md`](file:///Users/hudson/development/whatshelper/tenants/supporter/knowledge/QA.md), [`tenants/supporter/knowledge/como-acompanhar-meus-contratos.md`](file:///Users/hudson/development/whatshelper/tenants/supporter/knowledge/como-acompanhar-meus-contratos.md), [`tenants/supporter/knowledge/como-gerenciar-contratos-admin.md`](file:///Users/hudson/development/whatshelper/tenants/supporter/knowledge/como-gerenciar-contratos-admin.md), [`tenants/supporter/knowledge/como-acompanhar-meu-plano-de-carreira.md`](file:///Users/hudson/development/whatshelper/tenants/supporter/knowledge/como-acompanhar-meu-plano-de-carreira.md) e [`packs/supporter/pack.yaml`](file:///Users/hudson/development/whatshelper/packs/supporter/pack.yaml).

---

## 2026-11-15 — Redução de Consumo de Tokens com Busca Híbrida In-Process (`Grep + BM25` por Seções Markdown)

### Itens Implementados
- **Chunking Determinístico por Seções Markdown (`SplitMarkdownSections`)**:
  - Implementado em [`internal/contextmod/search.go`](file:///Users/hudson/development/whatshelper/internal/contextmod/search.go): particiona documentos `.md` por cabeçalhos (`##` e `###`), preservando o título principal (`#`) e o `DocName` original do arquivo para compatibilidade total com [`CheckGrounding`](file:///Users/hudson/development/whatshelper/internal/brain/grounding.go).
  - Ignora automaticamente arquivos `README.md` de placeholder sem conteúdo de domínio.
- **Motor de Busca Híbrida In-Process (`RetrieveRelevantKnowledge` & `BM25Searcher`)**:
  - Combina pontuação léxica **BM25**, filtro de stopwords PT-BR/EN, **stemming leve determinístico** de sufixos em português, expansão de sinônimos de horário/funcionamento e **boost estilo `grep`** para termos técnicos exatos (rotas `#/...`, códigos, números) e bigramas no título/corpo da seção.
  - Seleciona apenas as **Top-K seções mais relevantes** (`DefaultKnowledgeTopK = 6`) e reagrupa seções do mesmo arquivo sob um único `KnowledgeDocView` ordenado por relevância decrescente (garantindo que [`enforceBudget`](file:///Users/hudson/development/whatshelper/internal/contextmod/builder.go) descarte primeiro os menos relevantes caso o orçamento de tokens seja atingido).
  - Retorna lista vazia (`[]KnowledgeDocView{}`) quando nenhuma seção possui match com a query (ex.: saudações ou assuntos fora da base), evitando envio desnecessário de conhecimento ao LLM.
- **Filtragem Inteligente de `QA.md` (`FilterRelevantQA`)**:
  - Mantém o `QA.md` integralmente quando pequeno (`<= 250` tokens) e filtra apenas os Top-K pares `Pergunta/Resposta` relevantes quando o arquivo cresce.
- **Integração no `ContextService` e `Pipeline` do Brain**:
  - Adicionados `KnowledgeQuery` e `KnowledgeTopK` em [`GetContextRequest`](file:///Users/hudson/development/whatshelper/internal/contextmod/v1.go) e aplicados em [`InProcessService.GetContext`](file:///Users/hudson/development/whatshelper/internal/contextmod/inprocess.go).
  - Criada função pura [`BuildKnowledgeSearchQuery`](file:///Users/hudson/development/whatshelper/internal/brain/pure.go) e conectada ao estágio `ViewResponse` em [`Pipeline.ProcessBatch`](file:///Users/hudson/development/whatshelper/internal/brain/pipeline.go), combinando mensagem atual do cliente (com maior peso), intenção, fatos extraídos e resumo da conversa.
  - Testes unitários adicionados em [`internal/contextmod/builder_search_test.go`](file:///Users/hudson/development/whatshelper/internal/contextmod/builder_search_test.go) e [`internal/brain/knowledge_test.go`](file:///Users/hudson/development/whatshelper/internal/brain/knowledge_test.go).
