# TODOs — Brain agnóstico de atendimento por mensageria

Legenda: `[ ]` pendente · `[~]` em andamento · `[x]` concluído.
Cada item tem um ID estável para usar em commits (ex.: `feat(CORE-01): modelo de mensagem`).
A ordem de execução está nos milestones M0 → M5, no final do arquivo.

## Contexto e decisões já tomadas

- Linguagem: **Go**. Empacotado em **Docker**.
- **Local primeiro:** tudo roda e é testado localmente (docker-compose, sem AWS). O deploy em **AWS Fargate** é a última etapa (M5), só depois de validar o sistema local, inclusive com o WhatsApp real.
- O **brain** (cérebro) é o servidor central. Ele é **agnóstico de canal** (WhatsApp, email, simulador, outros) e **agnóstico de mercado**.
- Um **canal** só traduz mensagens externas para o modelo normalizado e vice-versa. WhatsApp via **whatsmeow** (não oficial). A API oficial da Meta pode virar outro canal no futuro, sem mudar o brain.
- O brain responde clientes com base em arquivos `.md` de conhecimento. Quando não sabe ou erra, o dono corrige, e o sistema grava o aprendizado em `QA.md`.
- **Vertical pack**: pasta que define um mercado (intents, schema de fatos, sinais, prompts). O pack `realestate` nasce dos três prompts existentes (analysis, broker, nudge). O core não pode conter nada específico de imobiliário.
- **Storage**: DynamoDB em **uma tabela única**. Cada conversa é uma "fonte" (canal + identificador). Resumo, fatos, sinais e metadados ficam no DynamoDB. A mensagem crua e a mídia ficam no S3.
- Fontes diferentes do mesmo cliente (ex.: WhatsApp e email) poderão ser ligadas no futuro. Agora só a **estrutura** precisa existir, sem lógica de merge.
- Servidor local com chat simulador e visualizador do "pensamento" do bot (prompt, resposta, fatos extraídos, tempo, tokens).

---

## DEC — Decisões e documentação

- [x] **DEC-01 — ADRs iniciais.** Registrar em `docs/adr/` as decisões: Go, canal agnóstico, vertical packs, DynamoDB single-table, S3 para mensagem crua. _Pronto quando:_ um ADR curto por decisão.
- [~] **DEC-02 — Identidade e PII.** Definir como o `source_key` é derivado por canal (WhatsApp: número E.164; email: hash do endereço normalizado), onde ficam os dados de contato brutos (criptografados) e a política de retenção. _Pronto quando:_ ADR aprovado. _(Nota: ADR 0006 redigido no M0; aguardando aprovação explícita.)_
- [x] **DEC-03 — Connector in-process ou separado.** Decidir se o canal roda no mesmo processo do brain ou em outro, falando por HTTP/WebSocket. Para agora, a decisão vale para rodar local; anotar só de passagem como ficaria no Fargate (um serviço ou dois). _Pronto quando:_ ADR com trade-offs.
- [ ] **DEC-04 — Sessão do whatsmeow em produção.** Localmente a sessão fica em SQLite num volume do docker-compose. No Fargate o disco do container é efêmero, então escolher entre EFS + SQLite, Postgres/RDS ou store customizado. Só precisa ser decidido no M5. _Pronto quando:_ ADR e plano de implementação.

## FND — Fundação do projeto

- [x] **FND-01 — Estrutura do repositório.** `cmd/` (brain, connectors), `internal/` (core, brain, channels, store, llm, server), `packs/`, `docs/`, `deploy/`, `misc/`.
- [x] **FND-02 — Configuração e logging.** Config por env e arquivo, log estruturado, graceful shutdown, health endpoint.
- [x] **FND-03 — Ambiente local com docker-compose.** DynamoDB Local, MinIO (S3 compatível) e o brain. _Pronto quando:_ `make up` sobe tudo.
- [x] **FND-04 — Automação e CI.** Makefile/Taskfile e CI com lint, test, build e docker build.
- [x] **FND-05 — Dockerfile.** Multi-stage, binário estático, imagem mínima, usuário não root. Usado primeiro no docker-compose local; o push para o ECR fica para o M5.

## CORE — Modelo de domínio e interfaces

- [x] **CORE-01 — Mensagem normalizada.** Campos: tenant, channel, account, source_key, message_id, timestamp, direction, type (texto/áudio/imagem/doc), text, media_ref, raw_ref.
- [x] **CORE-02 — Entidades.** Tenant, Source (conversa em um canal), Contact (opcional/futuro), Fact, Signal, Summary, Task, Nudge, Run (trace de processamento).
- [x] **CORE-03 — Interfaces (ports).** `Channel`, `Store`, `BlobStore`, `LLM`, `Clock`, `AdminNotifier`. Cada uma com implementação in-memory para testes.
- [x] **CORE-04 — Barramento de eventos.** Eventos do pipeline (mensagem recebida, análise iniciada/concluída, prompt enviado, resposta gerada/enviada, escalonamento, aprendizado). Alimenta o visualizador.
- [x] **CORE-05 — Idempotência e concorrência.** Dedupe por `message_id`, fila/lock por conversa, debounce de mensagens em rajada.

## PACK — Vertical packs (agnosticismo de mercado)

- [x] **PACK-01 — Formato de pack.** Pasta com `pack.yaml` (nome, idioma, intents, schema de fatos, sinais, contexto), `prompts/*.tmpl` e valores padrão.
- [x] **PACK-02 — Schema de fatos configurável.** Chaves, tipos e normalização definidos no pack, no lugar da lista fixa do prompt imobiliário (Intent, Budget, Bedrooms etc.).
- [x] **PACK-03 — Intents configuráveis.** Cada intent com nome, descrição, exemplos e ação associada (responder, escalar, criar task, passar para humano).
- [x] **PACK-04 — Sinais e contexto configuráveis.** Gerar o JSON schema de saída da análise a partir do pack e validar a resposta do LLM contra ele.
- [x] **PACK-05 — Templates de prompt.** Montar os prompts de análise, resposta e nudge a partir do pack e do estado (resumo, fatos, mensagens recentes, conhecimento).
- [x] **PACK-06 — Pack `realestate`.** Migrar os três prompts existentes preservando as regras: não assumir fatos, uma pergunta de qualificação por vez, nunca inventar imóveis ou preços, respostas em PT-BR.
- [x] **PACK-07 — Segundo pack de exemplo.** Ex.: clínica ou suporte genérico, só para provar que o core não depende de imobiliário.
- [x] **PACK-08 — Validação de pack.** Comando `brain pack validate` que checa schema, templates e referências.

## BRAIN — Pipeline do cérebro

- [x] **BRAIN-01 — Estágio de análise.** Extrair fatos, sinais, contexto e resumo; fazer merge, refinamento e tratar contradições; persistir o estado.
- [x] **BRAIN-02 — Roteamento de intent.** Decidir a ação com base nos intents do pack, nos sinais e no estado da conversa.
- [x] **BRAIN-03 — Estágio de resposta.** Gerar a resposta usando `knowledge/*.md`, `QA.md`, resumo e fatos. Respeitar o formato do canal (ex.: texto puro, sem Markdown).
- [x] **BRAIN-04 — Conhecimento.** Carregar `.md` por tenant (local ou S3), cachear e recarregar quando mudar.
- [x] **BRAIN-05 — Detecção de "não sei".** Critérios (sem fonte no conhecimento, regras do pack, baixa confiança). Responder de forma neutra ao cliente e escalar ao dono.
- [x] **BRAIN-06 — Nudge.** Agendador de follow-up para clientes que pararam de responder, com máximo de tentativas e janela de horário.
- [x] **BRAIN-07 — Tasks e pendências.** Criar itens a partir do contexto (visita, ligação, documentos) e expô-los via API.
- [x] **BRAIN-08 — Políticas por tenant.** Horário de atendimento e modo de operação: automático, com aprovação humana ou só rascunho.

## LRN — Aprendizado (QA.md)

- [x] **LRN-01 — Escalonamento ao dono.** Enviar a pergunta sem resposta ao canal de admin, com o contexto da conversa.
- [x] **LRN-02 — Captura da correção.** A resposta do dono vira uma entrada pergunta/resposta proposta pelo LLM.
- [x] **LRN-03 — Aprovação.** O dono confirma ou edita antes de gravar. Se rejeitar, descarta.
- [x] **LRN-04 — Gravação em `QA.md`.** Formato estável, dedupe e merge de entradas parecidas, versionamento (S3 versioning ou git).
- [x] **LRN-05 — Hot reload e retomada.** Recarregar o conhecimento após gravar e, opcionalmente, responder ao cliente que ficou esperando.
- [x] **LRN-06 — Correção de resposta já enviada.** Comando do admin (ex.: `/corrigir`) para apontar uma resposta errada e gerar o aprendizado.

## CH — Canais (mensageria)

- [x] **CH-01 — Contrato `Channel`.** Start/Stop, stream de mensagens recebidas, Send, indicador de digitando, health e capacidades declaradas (texto, mídia, reações).
- [x] **CH-02 — Protocolo connector ↔ brain.** HTTP/WebSocket com autenticação, versionamento e retries. Documentar em `docs/`.
- [x] **CH-03 — Canal simulador.** Chat web para desenvolvimento. É o primeiro canal implementado.
- [x] **CH-04 — Connector WhatsApp (whatsmeow).** Pareamento por QR/código, reconexão, receber e enviar texto, ids e dedupe. Localmente a sessão fica em SQLite num volume do docker-compose.
- [x] **CH-05 — Envio humanizado.** Atraso variável, indicador de digitando, fila por conversa e rate limit.
- [x] **CH-06 — Mídia.** Áudio com transcrição via provider; imagem e documento salvos no S3 e referenciados. Pode ficar para depois do texto.
- [ ] **CH-07 — Canal email (esqueleto).** Só o suficiente para validar o modelo de múltiplas fontes com chaves diferentes.
- [x] **CH-08 — Canal admin.** Conversa separada usada para escalonamento e comandos do dono.
- [ ] **CH-09 — Canal API oficial (futuro).** Deixar documentado como a Cloud API da Meta entraria como outro `Channel`. Não implementar agora.

## SRV — Servidor local e visualizador

- [x] **SRV-01 — API HTTP.** Enviar mensagem como cliente, listar conversas, ver estado (resumo, fatos, sinais, tasks).
- [x] **SRV-02 — Stream de eventos.** SSE ou WebSocket com os eventos do pipeline em tempo real.
- [x] **SRV-03 — UI de chat simulador.** Conversar com o bot como se fosse um cliente.
- [x] **SRV-04 — UI "pensando".** Linha do tempo por mensagem: prompt enviado, resposta crua, JSON parseado, diff de fatos, tempo e tokens.
- [x] **SRV-05 — UI de conhecimento.** Ver e editar `knowledge/*.md` e `QA.md`, e ver propostas de aprendizado pendentes.
- [x] **SRV-06 — Replay.** Reexecutar uma conversa gravada contra outro prompt ou pack e comparar as saídas.
- [x] **SRV-07 — Acesso.** Bind local por padrão; token simples para uso remoto.

## STO — Armazenamento (DynamoDB + S3)

- [x] **STO-01 — Padrões de acesso.** Documento em `docs/` listando as consultas necessárias (conversa por fonte, mensagens recentes, pendências vencidas, fontes de um contato). O design da tabela parte dele.
- [x] **STO-02 — Tabela única.** `PK`/`SK`, GSI1 (fontes de um contato), GSI2 (trabalho vencido: nudges e tasks), TTL. Local: script que cria a tabela no DynamoDB Local. IaC fica para o M5 (OPS-01).
- [x] **STO-03 — Itens por fonte.** Sob a mesma partição: `META`, `SUMMARY`, `FACTS`, `SIGNALS`, `CONTEXT`, `MSG#<ts>#<id>` (metadados e referência ao S3), `TASK#…`, `RUN#…`.
- [x] **STO-04 — Repositório Go.** Implementar `Store` sobre DynamoDB, com condition expressions para concorrência, e a versão in-memory.
- [x] **STO-05 — Layout no S3.** Mensagem crua, mídia e traces em caminhos previsíveis por tenant, canal e fonte. O item `MSG#` aponta para o objeto.
- [x] **STO-06 — Linkagem entre fontes (só estrutura).** Atributo `contact_id` e GSI1 reservados, item de link `CONTACT#…` → `SRC#…`. Sem lógica de merge.
- [x] **STO-07 — Retenção e custo.** TTL para traces, lifecycle no S3 e atenção ao limite de tamanho de item do DynamoDB.
- [x] **STO-08 — Testes de integração.** Rodar contra DynamoDB Local e MinIO.

## LLM — Camada de modelo

- [x] **LLM-01 — Interface e provider.** Interface `LLM` com implementação para a API da Anthropic. Modelo configurável por estágio (análise, resposta, nudge).
- [x] **LLM-02 — Saída JSON validada.** Validar contra o schema do pack, com retry/repair e fallback seguro.
- [ ] **LLM-03 — Prompt caching e contexto.** Cachear o conhecimento estático, limitar o histórico enviado e truncar com critério. _(Nota: após CTX-05, o truncamento por orçamento passa a viver no módulo de contexto; LLM-03 fica restrito ao prompt caching do provider.)_
- [ ] **LLM-04 — Métricas e resiliência.** Tokens, custo e latência por estágio; timeouts e circuit breaker.
- [x] **LLM-05 — LLM fake.** Implementação determinística para testes.

## SAF — Segurança operacional e anti-ban

- [x] **SAF-01 — Só responder a quem escreveu primeiro.** Flag por tenant para permitir mensagens proativas, desligada por padrão.
- [x] **SAF-02 — Rate limit e horários.** Limites globais e por conversa, com janelas de horário.
- [x] **SAF-03 — Kill switch e handoff.** Pausar o bot globalmente ou por conversa, e comando do admin para assumir a conversa.
- [x] **SAF-04 — Opt-out.** Respeitar e persistir pedidos de parar de receber mensagens.
- [x] **SAF-05 — Guardrails de conteúdo.** Nunca inventar dados fora do conhecimento; aplicar as regras do pack na validação da resposta.

## SEC — Segurança e privacidade

- [x] **SEC-01 — PII.** Criptografar dados de contato (KMS ou criptografia de atributos) e remover dados sensíveis dos logs.
- [x] **SEC-02 — Segredos.** Local: `.env` fora do git. Produção: Secrets Manager. Nunca no repositório.
- [ ] **SEC-03 — LGPD.** Exportar e excluir todos os dados de um contato (DynamoDB e S3).

## OPS — Deploy e operação (produção, só no final)

> Não começar antes de M0–M4 estarem validados localmente.

- [ ] **OPS-01 — IaC.** CDK ou Terraform para DynamoDB, S3, ECR, ECS/Fargate, IAM e logs.
- [ ] **OPS-02 — Serviço Fargate.** Uma task por sessão/número (desired count 1), health check e deploy sem duas instâncias na mesma sessão.
- [ ] **OPS-03 — Sessão do WhatsApp.** Implementar a persistência escolhida em DEC-04.
- [ ] **OPS-04 — Observabilidade.** Logs no CloudWatch, métricas e alarmes (desconexão do WhatsApp, erros do LLM, fila travada).
- [ ] **OPS-05 — Backup.** PITR no DynamoDB e versioning no S3.

## QA — Testes

- [x] **QA-01 — Unitários do core.** Usando os fakes de CORE-03 e LLM-05.
- [x] **QA-02 — Golden tests de prompts.** Entrada → JSON esperado, com opção de rodar contra LLM real.
- [x] **QA-03 — Fixtures de conversas.** Conversas realistas (imobiliário e pack de exemplo) usadas em testes e replay.
- [x] **QA-04 — End-to-end local.** Simulador → brain → store → visualizador.
- [x] **QA-05 — Connector.** Testes de reconexão e mensagens duplicadas.

## DOC — Documentação

- [x] **DOC-01 — README.** Arquitetura, como subir local e como rodar os testes.
- [x] **DOC-02 — Guia de novo pack.** Passo a passo para adaptar o brain a outro mercado.
- [ ] **DOC-03 — Guia de operação.** Parear número, rotina de operação e como trocar para a API oficial no futuro.

## CTX — Módulo de Contexto

- [x] **CTX-01 — ADR do módulo de contexto.** Fronteiras, chave de escopo, views, versionamento v1, segurança e preparo para `contact_id` (ADR 0018).
- [x] **CTX-02 — Contrato ContextService v1.** Operações: `GetContext(scope, view, budget)`, `ListMessages(scope, cursor)`, `GetFacts`, `ApplyFacts(expected_version)`, `GetSummary`, `GetSignals`/`GetContext` estruturado, `ListKnowledge`, `GetKnowledgeDoc`, `SearchKnowledge`, `ListLearningProposals`, `Approve`/`RejectProposal`, `GetRun`, `ListRuns`. DTOs próprios, versionados.
- [ ] **CTX-03 — Adapter in-process e brain consumindo o módulo.** `ProcessBatch` lê e escreve memória só via `ContextService`, sem acessar o `Store` diretamente para isso.
- [x] **CTX-04 — Views de contexto.** `analysis`, `response`, `admin`, `external_agent`, `summary_only`. Cada view define blocos, ordem e formato.
- [x] **CTX-05 — Montagem com orçamento.** `ContextBuilder` com orçamento de tokens por view e truncamento determinístico por prioridade (ex.: fatos e resumo antes de mensagens antigas). Retorna o que foi cortado, para aparecer no `/thinking`.
- [x] **CTX-06 — Providers de contexto plugáveis.** Interface `ContextProvider` (nome, schema, `Fetch(scope, budget)`). Embutidos: `summary`, `facts`, `signals`, `recent_messages`, `knowledge`. Registro por pack/tenant. A falha de um provider não derruba o turno.
- [ ] **CTX-07 — API REST v1 com OpenAPI.** Contexto por view/orçamento, mensagens com cursor, fatos (`PUT` com `If-Match`), resumo, conhecimento (listar, ler, buscar), propostas de aprendizado, runs e traces. Auth por API key com escopos por tenant, `ETag`, erros padronizados.
- [x] **CTX-08 — Busca no conhecimento.** Interface `KnowledgeSearcher` com implementação simples (palavra-chave/BM25 em memória sobre os `.md`), usada pela API e pelo MCP. Ponto de extensão para embeddings no futuro; sem banco vetorial agora.
- [ ] **CTX-09 — MCP server.** Comando próprio usando o SDK oficial em Go (`github.com/modelcontextprotocol/go-sdk`), transportes stdio e Streamable HTTP, tools de leitura e escrita opt-in (`update_facts`, `propose_qa_entry`), resources com URIs estáveis.
- [ ] **CTX-10 — Segurança da API e do MCP.** Tenant vindo da credencial; escopos (`read:context`, `write:facts`, `learn:propose`, `learn:decide`); allowlist de campos no pack; `encrypted_contact_info` nunca exposto; redação opcional de PII; conteúdo delimitado; audit log; rate limit.
- [ ] **CTX-11 — Eventos de contexto.** Fato alterado, resumo atualizado, proposta criada por SSE e opcionalmente webhooks assinados, reaproveitando o barramento do `CORE-04`.
- [ ] **CTX-12 — Cliente e documentação.** Cliente Go gerado a partir do OpenAPI, `docs/context-module.md`, guia para conectar um cliente MCP local e exemplos de chamadas.
- [ ] **CTX-13 — Testes de contrato.** Suíte única rodando contra os adapters (in-process, REST e MCP) com o mesmo resultado esperado; isolamento entre tenants; conflito de versão; paginação; golden tests e replay verdes.
- [ ] **CTX-14 — Leitura agregada por contato (preparação).** `Scope` aceita `contact_id` opcional e retorna contexto de várias fontes só em leitura, sem merge nem escrita (liga ao `STO-06`).
- [ ] **CTX-15 — Execução local.** Serviço da API/MCP no `docker-compose`, porta própria, config por env e README com comandos. Sem AWS.
- [x] **CTX-16 — Configuração no pack.** `pack.yaml` declara views, providers habilitados e campos expostos externamente. O `brain pack validate` passa a validar isso.

> **Ideia futura (fora dos milestones):** `CTX-17` — providers externos, para o brain consumir contexto de outras fontes (CRM, agenda, pedidos) via HTTP ou cliente MCP, com timeout, cache e falha isolada.

---

## Milestones

Do M0 ao M4 tudo roda 100% local. AWS e Fargate só aparecem no M5.

- **M0 — Fundação:** DEC-01, DEC-02, FND-01 a FND-05, CORE-01 a CORE-03.
- **M1 — Fatia vertical local** (simulador → brain → store → visualizador): CORE-04, CORE-05, LLM-01, LLM-02, LLM-05, STO-01 a STO-05, STO-08, PACK-01, PACK-02, PACK-04, PACK-05, PACK-06, BRAIN-01, BRAIN-03, BRAIN-04, CH-01, CH-03, SRV-01 a SRV-04, QA-01, QA-04.
- **M2 — Agnosticismo e políticas:** PACK-03, PACK-07, PACK-08, BRAIN-02, BRAIN-05 a BRAIN-08, SAF-01 a SAF-05, STO-06, STO-07, SRV-05 a SRV-07, QA-02, QA-03, DOC-01, DOC-02.
- **M3 — Aprendizado (QA.md):** LRN-01 a LRN-06, CH-08.
- **M4 — WhatsApp real, ainda local:** DEC-03, CH-02, CH-04, CH-05, CH-06, QA-05, SEC-01, SEC-02.
- **M4a — Núcleo modular (Contexto):** CTX-01 a CTX-06, CTX-08, CTX-13 (parte in-process), CTX-16.
- **M4b — Exposição por API e MCP:** CTX-07, CTX-09 a CTX-12, CTX-14, CTX-15, CTX-13 (restante).
- **M5 — Produção (Fargate):** DEC-04, OPS-01 a OPS-05, SEC-03, LLM-03, LLM-04, CH-07, CH-09, DOC-03.
