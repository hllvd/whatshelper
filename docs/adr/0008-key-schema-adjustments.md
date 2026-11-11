# ADR 0008: Ajustes no Schema de Chaves (DynamoDB + S3)

- **Status:** Aceito
- **Data:** 2026-11-11
- **Decisores:** Engenharia Whatshelper

## Contexto
Ao revisar o desenho inicial do armazenamento (`docs/access-patterns.md` e especificação original) antes da implementação concreta em DynamoDB e S3, identificamos inconsistências e requisitos operacionais do DynamoDB que precisam ser explicitados:

1. **Colisão de `RUN#<timestamp>`**: Se dois estágios ou retries executarem no mesmo milissegundo/segundo para a mesma fonte, a sort key `RUN#<timestamp>` sobrescreveria o registro anterior.
2. **Ambiguidade em `GSI2SK`**: O desenho especificava `GSI2SK = <due_at>#<source>`. Usar apenas `<source_key>` perde o canal da fonte em cenários multi-canal.
3. **Atributo `GSI1PK` vazio no `META`**: O DynamoDB rejeita (ou indexa indevidamente) valores de string vazia em chaves de índice secundário global, além de impedir o comportamento de índice esparso quando a fonte ainda não tem `contact_id`.
4. **Layout S3 de mensagens cruas**: A tabela de padrões de acesso usava `<tenant>/<canal>/<source_key>/<yyyy>/<mm>/<dd>/<ts>_<message_id>.json` (conforme especificação), mas a árvore ilustrativa inseria um prefixo extra `raw/`.
5. **Listagem de conversas do tenant (`GET /api/sources`)**: Sem um índice de fontes na partição do tenant, listar conversas exigiria um `Scan` completo na tabela.

## Decisão
1. **Sort Key de Runs**: Utilizar `RUN#<timestamp_iso8601>#<run_id>` para garantir unicidade mesmo sob concorrência ou retries no mesmo instante.
2. **Sort Key do GSI2**: Utilizar `GSI2SK = <due_at_iso8601>#<canal>#<source_key>` para identificar univocamente canal e fonte.
3. **GSI1 esparso**: Os atributos `GSI1PK` e `GSI1SK` no item `META` são **omitidos** enquanto `contact_id` estiver vazio, sendo preenchidos apenas quando o vínculo for criado.
4. **Layout S3 unificado**: Alinhar integralmente com o formato especificado: `<tenant>/<canal>/<source_key>/<yyyy>/<mm>/<dd>/<ts>_<message_id>.json` para mensagens cruas, mantendo `media/`, `traces/` e `knowledge/` em prefixos dedicados sob `<tenant>/`.
5. **Índice de fontes na partição do tenant (AP-15)**: Ao criar ou atualizar o `META` de uma fonte, manter o item leve `PK = T#<tenant>`, `SK = SRC#<canal>#<source_key>` (com `status`, `updated_at`, `last_message_at`), permitindo listar conversas do tenant via `Query` sem `Scan`.

## Consequências
- **Positivas:**
  - Eliminação total de operações `Scan` no DynamoDB.
  - Compatibilidade estrita com restrições de tipos/chaves do DynamoDB.
  - Alinhamento exato entre `docs/access-patterns.md` e `internal/store/keys.go`.
- **Negativas:**
  - O `PutMessageMeta` / `SaveSourceMeta` realiza uma escrita adicional leve na partição `T#<tenant>` para manter o catálogo de fontes atualizado.
