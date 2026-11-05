# ADR 0004: Persistência de Estado Estruturado em DynamoDB Tabela Única

- **Status:** Aceito
- **Data:** 2026-11-05
- **Decisores:** Engenharia Whatshelper

## Contexto
O sistema precisa de armazenamento de estado para conversas com latência de milissegundos de um dígito, escalabilidade horizontal sem necessidade de sharding manual, e custo proporcional ao uso em produção. As operações frequentes envolvem recuperar e atualizar o estado recente de uma conversa (resumo, fatos, sinais, contexto), registrar metadados de novas mensagens e consultar tarefas ou nudges pendentes.

## Decisão
1. **Padrão Single-Table Design no DynamoDB**:
   - Uma única tabela conterá todas as entidades estruturadas do sistema.
   - Partição da conversa/fonte:
     - `PK = T#<tenant>#S#<canal>#<source_key>`
     - `SK = META | SUMMARY | FACTS | SIGNALS | CONTEXT | MSG#<ts>#<id> | TASK#<id> | RUN#<ts>#<id>`
   - Configuração do tenant:
     - `PK = T#<tenant>`
     - `SK = CONFIG#<nome>`
2. **Índices Secundários Globais**:
   - `GSI1` (Estrutura de vínculo com contato unificado):
     - `GSI1PK = T#<tenant>#CONTACT#<contact_id>`
     - `GSI1SK = SRC#<canal>#<source_key>`
   - `GSI2` (Fila de trabalho vencido para nudges e tarefas):
     - `GSI2PK = T#<tenant>#DUE`
     - `GSI2SK = <due_at_iso8601>#<source_key>`
3. **Ambiente Local**:
   - Execução local via imagem oficial `amazon/dynamodb-local` no docker-compose.
   - Repositório Go implementará a interface `Store`, com versão DynamoDB e versão in-memory para testes unitários.

## Consequências
- **Positivas:**
  - Todo o estado de uma conversa pode ser lido em uma única requisição com baixo overhead.
  - Elimina a necessidade de gerenciar múltiplos esquemas e migrações relacionais complexas.
- **Negativas:**
  - Não armazena payloads grandes nem mídias, exigindo separação com armazenamento em objeto (S3).
