# ADR 0010: Nudges proativos reutilizam itens TASK# com GSI2

- **Status**: Aceito
- **Data**: 2026-11-11
- **Contexto**: M2 (`BRAIN-06`)

## Decisão

Em vez de criar um novo prefixo `NUDGE#` e duplicar o índice GSI2 no DynamoDB, os agendamentos de follow-up proativo (nudges) reutilizam exatamente o mesmo item `TASK#<id>` na partição da fonte (`PK = T#<tenant>#S#<canal>#<source_key>`), diferenciados por `kind: "nudge"`, `attempt` e `max_attempts`, e indexados no GSI2 (`GSI2PK = T#<tenant>#DUE`, `GSI2SK = <due_at>#<canal>#<source_key>`) somente enquanto `status == "pending"`.

## Consequências

- **Zero alteração de tabela/índices** no DynamoDB (`ensureTable` e `access-patterns.md` permanecem intactos).
- Ao cancelar (quando o cliente responde antes do prazo ou dá opt-out) ou esgotar tentativas (`status = completed`), o `SaveTask` grava o item sem `GSI2PK/GSI2SK`, removendo-o automaticamente da fila de vencimento.
- Requer que `proactive_enabled == true` na política do tenant (`CONFIG#policy`) para que qualquer nudge seja agendado ou enviado.
