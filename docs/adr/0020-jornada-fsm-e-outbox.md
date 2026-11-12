# ADR 0020: Camada de Jornada (FSM Declarativa), Eventos de Sistema, Outbox Transacional e Origem de Mensagens

- **Status**: Aceito
- **Data**: 2026-11-12
- **Contexto**: Milestones M4e (backend) e M4f (UI e funil) — Temas `EVT`, `DIM`, `JRN`, `OUT`, `QA-06`, `UIX-09` a `UIX-13`, `OBS-06`.
- **Relacionado a**: ADR 0004 (DynamoDB single-table), ADR 0012 (atomic commit + optimistic locking), ADR 0017 (connector separado protocolo v1), ADR 0019 (LLM como sensor determinístico).

## 1. Contexto e Motivação

Embora o ADR 0019 tenha introduzido o sensor estruturado e uma FSM inicial em `pack.yaml`, a evolução para um pipeline robusto de CRM/atendimento exige:
1. Separação estrita entre **controle de canal**, **ciclo de vida conversacional** e **jornada de negócio**.
2. Declaração de jornada em arquivo dedicado (`journey.yaml`) com expressões de guarda seguras, prioridade explícita, estagnação, digressão e timeouts por estado.
3. Execução confiável e idempotente de efeitos colaterais via **Outbox Transacional** (`OUTBOX#`), gravada na mesma transação DynamoDB da transição de estado.
4. Detecção determinística da **origem das mensagens** (`origin`) no WhatsApp multi-device (`whatsmeow`) por meio de **pré-registro de IDs de envio**, eliminando falsos positivos de pausa por eco sem jamais comparar texto.

## 2. Os Três Eixos de Estado Independentes

O core mantém três eixos ortogonais que **nunca devem ser misturados**:
1. **Status de controle (`META.status`)**: `active`, `paused`, `human`, `opted_out`, `archived`. Governa se o bot pode responder automaticamente (inclui expiração opcional `paused_until` / `human_until` para retomada automática após takeover).
2. **Ciclo de vida da conversa (`lifecycle`)**: Estado conversacional operacional (ex.: abertura, atendimento ativo, encerrado/inativo), caso habilitado por módulos futuros.
3. **Jornada de negócio (`JOURNEY`)**: Estágio no funil definido declarativamente pelo pack em `journey.yaml` (ex.: `lead_received`, `qualifying`, `visit_scheduled` no pack imobiliário; `triage`, `diagnosing`, `resolved` no pack de suporte).

## 3. Formato do `journey.yaml`

Cada pack pode declarar `packs/<pack>/journey.yaml`:
- `version`: versão do esquema da jornada.
- `initial_state`: estado inicial obrigatório.
- `fallback_state`: estado seguro usado quando o `journey.yaml` é atualizado e o estado salvo anteriormente deixa de existir.
- `states`: mapa ou lista de estados, cada um com:
  - `type`: `initial` | `intermediate` | `terminal`.
  - `timeout`: duração opcional (ex.: `24h`, `30m`) + `timeout_action` ou emissão de `system_timeout`.
  - `stagnation`: `max_turns` sem progresso no estado + `action` (ex.: `notify_admin` ou `handoff`).
  - `digression_policy`: se perguntas fora do roteiro devem ser respondidas via conhecimento mantendo o estado atual, com diretiva opcional de retomada.
- `global_rules`: regras avaliadas antes das regras específicas de estado (ex.: `opt_out`, `human_takeover`, `request_human`), com `priority` explícita.
- `transitions`: lista de regras (`id`, `from`, `to`, `on_event`, `intent`, `guard`, `priority`, `actions[]`).
  - `guard`: expressão booleana segura (compilada com `github.com/expr-lang/expr` no carregamento do pack) sobre `facts`, `dimensions`, `signals`, `event_type`, `intent`, `turns_in_state`, `seconds_in_state`.
  - Erro na compilação de guard bloqueia `brain pack validate`. Erro em runtime gera evento de erro e ignora a regra (nunca crash/panic).
- `actions`: cada ação emitida possui `id`, `type` (`send_directive`, `send_template`, `create_task`, `notify_admin`, `handoff`, `schedule_nudge`, `set_fact`, `webhook`) e `params` (ex.: `{ goal, must_ask, constraints }` ou `template_text`).

## 4. Precedência, Estagnação e Digressão

A função pura `Next(state, event, analysis, facts, dimensions, now)` segue precedência estrita:
1. **Regras globais** (`global_rules`), ordenadas por `priority` decrescente (desempate pela ordem declarativa; conflito de prioridade igual no mesmo gatilho é rejeitado na validação estática).
2. **Regras do estado atual** (`transitions` cujo `from == state`), ordenadas por `priority` decrescente.
3. **Estagnação**: se nenhuma transição avançar de estado e `turns_in_state >= state.stagnation.max_turns`, dispara a ação de estagnação configurada.
4. **Digressão / Padrão**: se o evento é `user_message` e nenhuma transição mudou o estado, responde mantendo o estado atual via conhecimento + diretiva opcional do estado para retomar o roteiro (se `digression_policy` ativa).

## 5. Catálogo de Eventos de Sistema (`EVT`)

Todos os estímulos entram no FSM como um `SystemEvent` unificado com `event_id` (idempotência), `type`, `origin`, `occurred_at` e `payload`:
- `user_message`: mensagem recebida do cliente (`origin = customer`).
- `session_start`: abertura de nova sessão/conversa (`origin = system`).
- `system_timeout`: expiração de prazo do estado atual ou pendência (`origin = system`).
- `human_message_sent`: dono respondeu fora do bot pelo celular (`origin = human_phone`).
- `human_takeover` / `human_release`: assunção ou devolução explícita de atendimento (`origin = human_admin` ou `system`).
- `integration_result`: reservado para callbacks de integrações externas futuras.

## 6. Outbox Transacional (`OUTBOX#`)

Para garantir entrega confiável e idempotência de efeitos colaterais:
- **Chave**: `PK = TENANT#<t>#CH#<c>#SRC#<s_key>`, `SK = OUTBOX#<run_id>#<index>` (e índice secundário ou consulta por prefixo `OUTBOX#` / GSI para itens `pending`/`failed`).
- **Transação atômica**: No `CommitJourneyTurn`, a atualização de `JOURNEY`, o histórico `JSTATE#<ts>`, o registro de `OUTBOX#<run_id>#<idx>` e eventuais pré-registros de `OUTMSG#` são gravados na **mesma transação DynamoDB** com `ConditionExpression` em `ExpectedVersion`.
- **Estados do Outbox**: `pending` → `done` | `failed` (agendado com backoff exponencial até `max_attempts`) → `dead` (após esgotar tentativas; reprocessável manualmente via API/UI).
- **Worker local**: goroutine iniciada com o `brain` que varre itens `pending` e `failed` com `next_attempt_at <= now` e executa os handlers registrados (envio de mensagem, criação de task, notificação admin, etc.).
- **Diretivas vs Templates**:
  - `send_directive`: carrega `{ goal, must_ask, constraints }`; o worker/estágio de resposta invoca o LLM de resposta restrito à diretiva + conhecimento + `CheckGrounding`.
  - `send_template`: envia texto fixo parametrizado sem chamar LLM (mensagens críticas/compliance).

## 7. Persistência da Jornada (`JOURNEY` e `JSTATE#`) e Evolução do YAML

- **Estado atual (`SK = JOURNEY`)**: `state`, `entered_at`, `turns_in_state`, `version`, `journey_version`, `updated_at`.
- **Histórico (`SK = JSTATE#<RFC3339Nano>#<run_id>`)**: `from_state`, `to_state`, `event_type`, `rule_id`, `reason`, `run_id`, `created_at`.
- **Mudança de `journey.yaml`**: ao hidratar `JOURNEY`, se `state` não existir mais no `journey.yaml` carregado:
  1. O brain faz fallback determinístico para `journey.fallback_state` (ou `journey.initial_state` se não definido).
  2. Registra uma entrada `JSTATE#` de migração automática (`reason = "state_removed_from_journey_yaml"`).

## 8. Pré-registro de IDs de Envio, Matriz de `origin` e Política de Takeover

### 8.1 Pré-registro de IDs (`OUTMSG#<message_id>`)
- No `whatsmeow`, `client.GenerateMessageID()` gera o ID antes da chamada de rede e `client.SendMessage(ctx, jid, msg, whatsmeow.SendRequestExtra{ID: msgID})` envia exatamente com esse ID (confirmado na API da biblioteca `go.mau.fi/whatsmeow`).
- **Fluxo**:
  1. Antes de o connector enviar ao WhatsApp, o brain gera e persiste `OUTMSG#<message_id>` (associado ao `outbox_id`, `source_key`, `status = reserved`, `ttl = now + 24h`).
  2. O connector envia usando esse `message_id` pré-definido.
  3. Se o eco do WhatsApp chegar **antes** de `SendMessage` retornar, o `OUTMSG#<message_id>` já existe no store, portanto o brain reconhece imediatamente `origin = bot`.
  4. Se o envio no outbox falhar definitivamente (`failed`/`dead`), o `OUTMSG#` é marcado como `failed` e expira pelo TTL (não fica marcado como enviado para sempre).
- **Canais sem `ReportsOwnMessages`** (ex.: simulador, email): a capability `ReportsOwnMessages` é `false`; o recurso de detecção de eco/takeover por `from_me` fica desligado sem erro.
- **Reconexão e histórico**: eventos `from_me = true` com timestamp anterior ao `connected_at` do connector ou já processados (`message_id` dedupado) são ignorados para fins de pausa. Mensagens de grupo, broadcast e status são descartadas na borda.

### 8.2 Matriz de Decisão de `origin` (Executada exclusivamente no Brain)

| Canal / Condição | `from_me` | `message_id` em `OUTMSG#`? | `origin` resultante | Efeito no Bot |
| :--- | :--- | :--- | :--- | :--- |
| Canal normal (cliente) | `false` | N/A | `customer` | Processa normalmente (`user_message`) |
| Canal admin / UI | N/A | N/A | `human_admin` | **Nunca pausa** a conversa (exceto comando explícito `/assumir`) |
| WhatsApp (`ReportsOwnMessages`) | `true` | **Sim** (estado `reserved`/`sent`) | `bot` | Eco do próprio robô: ignora para pipeline/pausa |
| WhatsApp (`ReportsOwnMessages`) | `true` | **Não** (e timestamp >= `connected_at`) | `human_phone` | Emite `human_message_sent` e aplica política de takeover |
| WhatsApp / Ambíguo (sem ID confiável ou falha de consulta) | `true` | Indeterminado | `unknown` | **Nunca pausa automaticamente**; registra e sinaliza na UI |
| Evento interno (timeout, sessão) | N/A | N/A | `system` | Processa evento de sistema no FSM |

*Nota de segurança*: Jamais classificar `origin` comparando texto da mensagem. Se o dono digitar manualmente no celular um texto idêntico ao que o bot já enviou, o ID será diferente e desconhecido, portanto será classificado corretamente como `human_phone`.

### 8.3 Política de Takeover Humano (`EVT-04`)
Configurável por tenant (`TakeoverPolicy`):
- `mode`: `pause` (padrão quando `ReportsOwnMessages` suportado com ID pré-registrado) | `record_only` | `ignore`.
- `pause_duration`: duração da pausa automática (ex.: `30m`, `2h`), gravando `META.status = human` e `paused_until`.
- Retomada: automática quando `now >= paused_until` (no próximo evento ou verificação) ou manual pelo dono no canal admin (`/retomar`).

## 9. Consequências e Riscos

- **Positivas**: Eliminação de loops de eco no WhatsApp, garantia transacional de estado + ações, desacoplamento total entre regras de funil e código Go, e auditabilidade completa (`JSTATE#` + `OUTBOX#`).
- **Riscos mitigados**:
  - *Race condition de eco antes do retorno de `SendMessage`*: mitigado pelo pré-registro antes da chamada de rede.
  - *Guard malformada no `journey.yaml`*: mitigado pela compilação estática no `brain pack validate` e isolamento de erro em runtime.
