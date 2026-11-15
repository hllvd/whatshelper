# ADR 0021: Janela de Contexto Estruturada, Debounce Adaptativo, Guarda contra Resposta Defasada, Coleções de Fatos e Edição/Exclusão

- **Status**: Aceito
- **Data**: 2026-11-15
- **Contexto**: Milestones M4g (núcleo puro e debounce) e M4h (coleções, multi-intent, edição e UI) — Tema `WIN` (`WIN-01` a `WIN-09`).
- **Relacionado a**: ADR 0017 (connector separado protocolo v1), ADR 0018 (módulo de contexto), ADR 0019 (FSM e sensor LLM), ADR 0020 (jornada FSM, eventos, outbox e origem de mensagens).

## 1. Contexto e Motivação

No atendimento conversacional real por WhatsApp, clientes frequentemente fragmentam um único pensamento em várias mensagens curtas enviadas em sequência:
> `"Eu quero comprar um apartamento"` / `"E vender o meu"` / `"Bom dia primeiramente"` / `"E TB tenho outro para alugar em março"`

Para tratar essas rajadas como **um único pedido multi-parte** sem respostas duplicadas nem perda de contexto, precisamos formalizar em funções puras:
1. A distinção explícita entre **Lote Atual (NOVO, ainda sem resposta)** e **Histórico Recente (5 mensagens do cliente / 10 no total)**, com papel (`customer`, `bot`, `human`) e delta de tempo (`+4s`, `+2m`) entre mensagens.
2. O **Debounce Adaptativo** (`NextDebounceDecision`), combinando a janela base por pack (`8s`), extensão por mensagem com indício de continuação (heurística léxica do pack) e standby por `typing` (`16s`), sempre limitado por um teto máximo intransponível.
3. A **Guarda contra Resposta Defasada** (`ShouldDiscardReply`), descartando respostas geradas caso novas mensagens tenham chegado durante a chamada ao LLM, cancelando itens `pending` no outbox e reprocessando o lote unificado (com limite de reprocessamentos).
4. **Coleções de Fatos** (`MergeAndDiffCollections`) e **Múltiplos Intents por Lote** (`AggregateBatchIntents`), permitindo representar múltiplos objetivos na mesma conversa (ex.: comprar, vender e alugar) sem sobrescrever um mapa plano.
5. **Edição e Exclusão de Mensagens** (`ApplyMessageEdit`), suportando o que o `whatsmeow` (`waE2E.ProtocolMessage`) expõe para `MESSAGE_EDIT` e `REVOKE`.

---

## 2. Definição de Lote vs. Histórico, Rotulagem e Orçamento de Tokens (`WIN-02`)

### 2.1. Estrutura da Janela (`BuildWindow`)
A janela enviada aos estágios `analysis` e `response` do LLM é dividida em duas seções distintas:
- **Histórico (`History`)**: até `N` mensagens anteriores já processadas (padrão `HistoryMaxMessages = 10` no total entre robô e cliente, garantindo a preservação das **últimas `HistoryMinCustomer = 5` mensagens do cliente** em ordem cronológica, configurável por pack e limitado por `RECENT_MESSAGES_LIMIT`). Mensagens marcadas como `deleted` ou pertencentes ao lote atual (`Batch`) são sempre excluídas do histórico.
- **Lote Atual (`Batch`)**: todas as mensagens recebidas na janela de debounce atual (e ainda sem resposta), em ordem cronológica. **Invariante fundamental: o lote atual nunca é cortado, mesmo se exceder `N` ou o orçamento de tokens.**

### 2.2. Rotulagem de Papel e Delta de Tempo
Cada item em `History` e `Batch` recebe:
- `Role`: derivado deterministicamente de `Origin` e `Direction`:
  - `customer`: `Direction == inbound` (ou `Origin == customer`).
  - `human`: `Origin == human_phone` ou `Origin == human_admin`.
  - `bot`: `Direction == outbound` com `Origin == bot` (ou vazio/default outbound).
- `DeltaFromPrev`: intervalo formatado desde a mensagem imediatamente anterior na sequência cronológica (`"0s"` na primeira mensagem, `"+4s"`, `"+2m15s"`, `"+1h"`, etc.).

### 2.3. Orçamento de Tokens e Ordem de Corte
Quando `TokenBudget > 0` e o total estimado da janela (`History + Batch + Overhead`) excede `TokenBudget`:
1. Primeiro são removidas do `History` as mensagens mais antigas que **não** comprometam o mínimo de `HistoryMinCustomer` (5) mensagens do cliente (isto é, prioriza-se descartar respostas antigas de `bot`/`human` ou mensagens de cliente além das 5 mais recentes).
2. Se o orçamento continuar estourado, removem-se as mensagens mais antigas restantes do `History` (`reason: "budget_exceeded"`).
3. O `Batch` (lote atual) **jamais é removido ou truncado** (`BatchNeverCut`).

---

## 3. Debounce Adaptativo (`WIN-03`)

O `SourceDispatcher` delega toda decisão temporal à função pura `NextDebounceDecision(state, event, cfg, now)`:
- **Configuração por Pack (`TimingDef`)**:
  - `debounce_ms` (padrão `8000` — 8s de inatividade base).
  - `incomplete_grace_ms` (padrão `4000` — extensão adicional quando a última mensagem parece incompleta).
  - `typing_max_wait_ms` / `max_wait_ms` (padrão `16000` — teto máximo de espera desde o início do ciclo/última extensão, configurável até 20–25s por pack).
  - `min_wait_ms` (padrão `0` ou `debounce_ms`).
  - `incomplete_suffixes` / `continuation_prefixes`: lista de sufixos/conectivos por idioma no pack (defaults PT-BR: termina em `,`, `:`, `;`, `...`, `"e"`, `"ou"`, `"mas"`, `"que"`, `"para"`, `"pra"`, `"com"`, `"de"`, `"em"`; ou nova mensagem começa com `"e "`, `"tb "`, `"também "`, `"inclusive "`, `"além disso"`).
- **Regras de transição**:
  1. Ao receber mensagem (`DebounceEventMessage`): inicia ciclo (`CycleStart` na 1ª mensagem do lote) e agenda espera base (`debounce_ms`), ou `debounce_ms + incomplete_grace_ms` se `IsIncompleteFragment(text, lexicon)` for verdadeiro — sempre limitado por `CycleStart + MaxWait`.
  2. Ao vencer o timer (`DebounceEventTimerExpired`):
     - Se `state.Typing == true` e `now < CycleStart + MaxWait`, retorna `Flush: false, WaitUntil: CycleStart + MaxWait, Reason: "typing_standby"`.
     - Caso contrário, retorna `Flush: true, Reason: "debounce_expired"` (ou `"max_wait_reached"` se atingiu o teto).
  3. Ao receber `typing=false` (`DebounceEventTypingStopped`): se o debounce base já havia vencido (`state.WaitingTyping == true`), retorna `Flush: true, Reason: "typing_stopped"`.
  4. **Invariante de Teto**: `WaitUntil` nunca ultrapassa `state.CycleStart.Add(cfg.MaxWait)` quando `cfg.MaxWait > 0`. Se uma nova mensagem chegar depois que o lote anterior já deu flush, ela inicia um novo ciclo (` Turno N+1`) com o turno anterior já persistido no histórico.

---

## 4. Guarda contra Resposta Defasada (`WIN-04`)

Se o cliente enviar uma nova mensagem **depois** que o lote começou a ser processado pelo `ProcessBatch` (durante a chamada ao LLM de análise ou resposta, antes do envio efetivo ao canal):
1. Cada fonte mantém um contador monotônico de sequência de entrada (`InboundSeq`). No início do `ProcessBatch`, captura-se `seqInicial`.
2. Antes de despachar a resposta/outbox (e após chamadas ao LLM), consulta-se `seqAtual` e avalia-se a função pura `ShouldDiscardReply(seqInicial, seqAtual, reprocessCount, policy)`.
3. Se `seqAtual > seqInicial` e `reprocessCount < policy.MaxReprocesses` (padrão `2`):
   - A resposta gerada é **descartada**;
   - Quaisquer itens `OUTBOX#` gerados na tentativa são cancelados antes do envio (o que já foi enviado ao canal externo em turnos anteriores nunca é revertido);
   - O evento `reply_discarded_stale` é publicado no `EventBus`;
   - As novas mensagens que chegaram são incorporadas ao lote atual e o `ProcessBatch` reexecuta com o lote completo (`reprocessCount + 1`).

---

## 5. Modelo de Coleções de Fatos (`WIN-05`) e Múltiplos Intents por Lote (`WIN-06`)

### 5.1. Coleções de Fatos (`collections` no `pack.yaml`)
Além de `facts` (`map[string]string`), o `pack.yaml` pode declarar `collections`:
- Cada coleção tem `key` (ex.: `objectives`), `id_field` ou `natural_key` (ex.: `["type", "property_type"]`) e `fields` tipados (`string`, `number`, `integer`, `boolean`, `enum`).
- `MergeAndDiffCollections(existing, patch, schema)` aplica upserts por `id` (ou chave natural derivada determinística quando `id` é omitido) e remoções explícitas (`_delete: true`), preservando ordem estável e idempotência.

### 5.2. Múltiplos Intents por Lote (`AggregateBatchIntents`)
- O envelope de análise passa a aceitar `batch_intents []BatchIntentItem` (intent por segmento/mensagem do lote) além do `intent` primário.
- A função pura `AggregateBatchIntents(primary, items, packIntents)` consolida intents únicos sem duplicatas, determina a ação prioritária (`opt_out` > `handoff` > `escalate` > `create_task` > `reply`) e fornece todos os pontos para o estágio de resposta cobrir em **uma única mensagem com no máximo uma pergunta de qualificação**.

---

## 6. Edição e Exclusão de Mensagens (`WIN-07`)

Conferido diretamente na biblioteca `go.mau.fi/whatsmeow`:
- `events.Message` possui `IsEdit bool` e `Message.GetProtocolMessage()` (`*waE2E.ProtocolMessage`).
- `waE2E.ProtocolMessage` expõe:
  - `GetType() == waE2E.ProtocolMessage_MESSAGE_EDIT` com `GetKey().GetID()` (ID original) e `GetEditedMessage()` (novo conteúdo).
  - `GetType() == waE2E.ProtocolMessage_REVOKE` com `GetKey().GetID()` (ID da mensagem apagada).
- A função pura `ApplyMessageEdit(pendingBatch, history, event, policy)`:
  - Se a mensagem alvo ainda está no `pendingBatch` (antes do flush): edição substitui o `Text` in-place no lote; exclusão remove a mensagem do lote.
  - Se a mensagem já foi processada: marca `EditedText` ou `Deleted = true` nos metadados para que `BuildWindow` omita mensagens `deleted` e use o texto corrigido, abrindo reprocessamento apenas se `policy.ReopenOnEdit` estiver habilitado.

---

## 7. Assinaturas das Funções Puras

```go
// WIN-02 (internal/contextmod)
func BuildWindow(in WindowInput) WindowOutput

// WIN-03 (internal/brain)
func IsIncompleteFragment(text string, lex IncompleteLexicon) bool
func NextDebounceDecision(state DebounceState, evt DebounceEvent, cfg AdaptiveDebounceConfig, now time.Time) DebounceDecision

// WIN-04 (internal/brain)
func ShouldDiscardReply(initialSeq, currentSeq uint64, reprocessCount int, policy StaleReplyPolicy) StaleReplyDecision

// WIN-05 (internal/brain)
func MergeAndDiffCollections(existing map[string][]map[string]any, patch map[string][]map[string]any, defs []pack.CollectionDef) (merged map[string][]map[string]any, diff CollectionDiff)

// WIN-06 (internal/brain)
func AggregateBatchIntents(primary string, items []BatchIntentItem, defs []pack.IntentDef) AggregatedIntents

// WIN-07 (internal/brain)
func ApplyMessageEdit(batch []core.NormalizedMessage, edit MessageMutationEvent, policy EditPolicy) EditApplicationResult
```
