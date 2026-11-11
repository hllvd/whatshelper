# ADR 0012: Port Store com CommitAnalysis Atômico e Controle de Versão Otimista

- **Status:** Aceito
- **Data:** 2026-11-11
- **Decisores:** Engenharia Whatshelper

## Contexto
No M0, a interface `core.Store` expunha quatro operações separadas de gravação de estado (`SaveSummary`, `SaveFacts`, `SaveSignals`, `SaveContext`) sem controle de concorrência nem atomicidade entre os itens de uma mesma conversa. Em cenários com retries, debounce ou múltiplos workers, gravações parciais ou intercaladas causariam corrupção silenciosa de estado. Além disso, a deduplicação de mensagens no armazenamento precisa retornar um erro tipado distinto para o orquestrador decidir ignorar mensagens repetidas sem executar o pipeline.

## Decisão
Estender a interface `core.Store` mantendo retrocompatibilidade controlada e adicionando garantias transacionais:
1. **Versão otimista em `SourceState`**: `SourceState` e `SourceMeta` passam a carregar `Version int64` (0 quando a fonte ainda não existe).
2. **`CommitAnalysis(ctx, CommitAnalysisInput)`**: Atualiza `META` (incrementando `Version` via `ConditionExpression` `version = :expected` ou `attribute_not_exists(PK)` na primeira gravação), `SUMMARY`, `FACTS`, `SIGNALS`, `CONTEXT` e insere `RUN#<ts>#<run_id>` em uma única transação atômica (`TransactWriteItems` no DynamoDB; mutex único no `MemoryStore`). Conflito de versão retorna `core.ErrVersionConflict`.
3. **`PutMessageMeta(ctx, MessageMeta)`**: Grava o item `MSG#<ts>#<id>` com condição `attribute_not_exists(PK)`. Se o `message_id` já foi gravado para aquela fonte, retorna `core.ErrDuplicateMessage` (suportando dedupe idempotente em CORE-05). Também garante a existência do índice `SRC#<canal>#<source_key>` na partição `T#<tenant>`.
4. **Consultas para o servidor local**: Adicionar `ListSources(ctx, tenant)` e `ListRuns(ctx, tenant, channel, sourceKey, limit)` para alimentar a API HTTP e a tela `/thinking`.

## Consequências
- **Positivas:**
  - Zero risco de estado parcialmente gravado em caso de falha ou corrida.
  - Conflitos otimistas são detectados explicitamente via `ErrVersionConflict` e reprocessados com estado fresco.
  - A mesma suíte de testes de contrato valida identicamente `MemoryStore` e `DynamoStore`.
- **Negativas:**
  - `TransactWriteItems` no DynamoDB consome 2x WCUs pelos itens envolvidos e limita a transação a 100 itens (usamos no máximo 7 por commit).
