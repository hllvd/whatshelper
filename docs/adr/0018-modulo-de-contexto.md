# ADR 0018: Módulo de Contexto Independente (`ContextService` v1)

- **Status**: Aceito
- **Data**: 2026-11-12
- **Contexto**: M4a / M4b (`CTX-01` a `CTX-16`)

## 1. Contexto e Motivação

Atualmente a leitura e escrita da memória conversacional (`META`, `SUMMARY`, `FACTS`, `SIGNALS`, `CONTEXT`, `MSG#`, `RUN#`, `TASK#`, `knowledge/*.md`, `QA.md`, propostas de aprendizado e traces) é orquestrada diretamente por `internal/brain` sobre `core.Store`, `core.BlobStore` e `KnowledgeSource`. Para permitir que agentes externos, ferramentas e integrações consumam (e, sob controle estrito, atualizem) contexto via API REST e servidor MCP sem duplicar regras nem acoplar ao pipeline cognitivo, extraímos essa camada como um módulo de fachada independente.

## 2. Fronteiras do Módulo (`internal/contextmod`)

- **Dentro do Módulo de Contexto**:
  - Fachada única sobre o armazenamento existente (`DynamoDB` + `S3`/`MinIO` — **nenhum banco novo**).
  - Montagem de contexto por **view** e **orçamento de tokens** com truncamento determinístico e relatório do que foi cortado (`TruncationReport`).
  - Providers plugáveis (`summary`, `facts`, `signals`, `recent_messages`, `knowledge`) com isolamento de falha.
  - Histórico paginado de mensagens por cursor (`ListMessages`).
  - Leitura/escrita de fatos com concorrência otimista (`ExpectedVersion` / `If-Match` / `ETag`).
  - Busca em memória (`BM25`/palavra-chave) e leitura de `knowledge/*.md` e `QA.md`.
  - Consulta/decisão controlada de propostas de aprendizado (`ListLearningProposals`, `Approve/RejectProposal`) e consulta de `Run`/`RunTrace`.
- **Fora do Módulo (permanece no `brain`)**:
  - Ingestão de canais (`Channel`), debounce, gates pré-LLM (opt-out, kill switch, horário comercial, rate limit), chamadas a `core.LLM`, roteamento determinístico (`Route`), validação de grounding (`CheckGrounding`), envio de mensagens (`Channel.Send`) e worker de nudges.

## 3. Escopo, Views e Versionamento de Contrato (`v1`)

- **Chave de escopo (`Scope`)**: `TenantID` + `Channel` + `SourceKey` + `ContactID` (opcional, reservado para leitura agregada `CTX-14`).
- **Aderência ao ADR 0006**: A derivação de `source_key` permanece inalterada (WhatsApp E.164, e-mail SHA-256, simulador ID). `encrypted_contact_info` **nunca** é exposto pelo módulo de contexto.
- **Views declaradas no `pack.yaml` (`CTX-16`)**: `analysis`, `response`, `admin`, `external_agent` e `summary_only`, definindo blocos habilitados, ordem, formato, orçamento padrão de tokens e `exposed_fields` (allowlist externa).
- **DTOs versionados (`v1`)**: Tipos públicos próprios desacoplados das structs de domínio (`core`) e dos atributos DynamoDB. `ExpectedVersion` vira `ETag` / `If-Match` na API REST e `expected_version` nas ferramentas de escrita MCP.

## 4. Modelo de Segurança

- **Tenant derivado da credencial**: Nas exposições externas (REST e MCP), o `tenant_id` vem exclusivamente da credencial autenticada, nunca de parâmetro do cliente.
- **Escopos explícitos**: `read:context` (padrão), `write:facts`, `learn:propose`, `learn:decide`. Escrita desabilitada por padrão no MCP; aprovação/rejeição de aprendizado permanece exclusiva do dono (`admin` ou API com escopo `learn:decide`).
- **Proteção contra prompt injection e vazamento de PII**: Conteúdo de mensagens é devolvido delimitado como dado não confiável nas views externas, com redação opcional de PII nas mensagens e allowlist estrita de campos em `external_agent`.

## 5. Preparo para Leitura Agregada por `contact_id` (`STO-06` / `CTX-14`)

Quando `Scope.ContactID` for informado no futuro (somente leitura), o módulo resolverá as fontes vinculadas via `Store.ListSourcesByContactGSI1` / `ListContactSources`, agregando resumos, fatos permitidos e mensagens recentes por fonte **sem lógica de merge nem escrita cross-source**.

## 6. Consequências

- **Positivas**: Um único caminho de acesso a contexto para o `brain`, API REST v1 e MCP; zero duplicação de lógica de truncamento, busca e allowlist; transição limpa para futuros providers externos (`CTX-17`).
- **Negativas**: O `Pipeline` do `brain` passa a depender da interface `contextmod.Service` e o `pack.yaml` ganha validação adicional de configuração de contexto (`brain pack validate`).
