# Padrões de Acesso ao Armazenamento (DynamoDB + S3)

Este documento define todos os padrões de acesso (Access Patterns - AP) do **brain**, servindo como base para o design da tabela única no DynamoDB e para o layout de objetos no S3/MinIO.

---

## 1. Visão Geral da Arquitetura de Armazenamento

A persistência do sistema é estritamente dividida em duas camadas com responsabilidades distintas:

1. **DynamoDB (Tabela Única)**: Guarda o estado estruturado da conversa, metadados de mensagens, tarefas operacionais, agendamentos de nudges e configurações de tenant.
2. **S3 / MinIO (Blob Storage)**: Guarda o payload bruto (raw) das mensagens, mídias recebidas ou enviadas (áudio, imagens, documentos), traces detalhados de execução do LLM e arquivos de conhecimento (`.md`, `QA.md`).

---

## 2. Entidades e Chaves no DynamoDB

### 2.1 Convenções de Chave Primária (PK e SK)

- **Tenant**: Identificador do inquilino (ex: `tenant-123`).
- **Canal (`canal`)**: Nome do canal normalizado (`whatsapp`, `simulator`, `email`).
- **Identificador da Fonte (`source_key`)**:
  - WhatsApp: Número de telefone normalizado em formato E.164 (ex: `5511999998888`).
  - Email: Hash SHA-256 do endereço de e-mail normalizado (minúsculas, sem espaços).
  - Simulador: Identificador de sessão de teste (ex: `sim-user-01`).

### 2.2 Estrutura da Partição da Fonte (`Source Partition`)
Toda conversa vive em uma partição isolada no DynamoDB:
- **PK**: `T#<tenant>#S#<canal>#<source_key>`

Dentro dessa partição, os registros são diferenciados pela **Sort Key (SK)**:
- `META`: Metadados da conversa (canal, status, data de início, `contact_id` opcional, dados de contato criptografados).
- `SUMMARY`: Resumo textual cumulativo (1 a 3 frases) e contagem de turnos.
- `FACTS`: Mapa estruturado de fatos extraídos da conversa.
- `SIGNALS`: Sinais conversacionais objetivos (flags booleanas para automação).
- `CONTEXT`: Contexto estruturado da última ação, visitas sugeridas, ligações agendadas, etc.
- `MSG#<timestamp_iso8601>#<message_id>`: Metadados da mensagem (direção, tipo, timestamp, referência para o S3). **Não armazena o texto longo ou bruto**.
- `TASK#<task_id>`: Tarefas operacionais derivadas da conversa (ex: agendar visita, enviar proposta).
- `RUN#<timestamp_iso8601>#<run_id>`: Metadados resumidos de execução do pipeline (tokens, latência, estágio).

### 2.3 Estrutura de Configuração e Índice de Fontes do Tenant
- **PK**: `T#<tenant>`
- **SK**: `CONFIG#<nome>` (ex: `CONFIG#general`, `CONFIG#operating_hours`, `CONFIG#pack`)
- **SK**: `SRC#<canal>#<source_key>` (índice esparso para listar conversas ativas do tenant sem `Scan`; ver ADR 0008)

### 2.4 Índices Secundários Globais (GSIs)

#### GSI1 — Identidade e Vínculo Multicanal do Contato
Permite consultar todas as fontes/conversas pertencentes a um mesmo contato unificado (`contact_id`), sem realizar merge físico de partições.
- **GSI1PK**: `T#<tenant>#CONTACT#<contact_id>` *(índice esparso: atributo omitido quando `contact_id` está vazio; ver ADR 0008)*
- **GSI1SK**: `SRC#<canal>#<source_key>`
- Itens projetados: `META` das fontes (quando possuem `contact_id`) e itens de ligação dedicados.

#### GSI2 — Fila de Trabalho Vencido (Tasks e Nudges)
Permite ao agendador do brain consultar rapidamente tarefas ou nudges pendentes que atingiram seu momento de execução (`due_at <= now`).
- **GSI2PK**: `T#<tenant>#DUE`
- **GSI2SK**: `<due_at_iso8601>#<canal>#<source_key>`
- Itens presentes no GSI2 possuem atributos `GSI2PK` e `GSI2SK` definidos apenas enquanto estão com status `pending`.

---

## 3. Matriz de Padrões de Acesso (Access Patterns)

| ID | Descrição | Componente | Operação | Chave / Condição |
|---|---|---|---|---|
| **AP-01** | Obter estado completo da conversa (Meta, Summary, Facts, Signals, Context) | DynamoDB | `BatchGetItem` ou `Query` com `SK in (META, SUMMARY, FACTS, SIGNALS, CONTEXT)` | `PK = T#<tenant>#S#<canal>#<source_key>` |
| **AP-02** | Salvar/Atualizar estado estruturado da conversa após análise | DynamoDB | `TransactWriteItems` ou `PutItem` por item | `PK = T#<tenant>#S#<canal>#<source_key>`, `SK = SUMMARY / FACTS / SIGNALS / CONTEXT` |
| **AP-03** | Registrar metadados de nova mensagem | DynamoDB | `PutItem` com condição `attribute_not_exists(PK)` para deduplicação | `PK = T#<tenant>#S#<canal>#<source_key>`, `SK = MSG#<ts>#<id>` |
| **AP-04** | Listar histórico recente de mensagens de uma conversa | DynamoDB | `Query` com `SK begins_with MSG#`, ordenado decrescente por SK, com limite | `PK = T#<tenant>#S#<canal>#<source_key>` |
| **AP-05** | Gravar payload bruto e mídia de mensagem | S3 | `PutObject` | `<tenant>/<canal>/<source_key>/<yyyy>/<mm>/<dd>/<ts>_<message_id>.json` |
| **AP-06** | Ler payload bruto de mensagem específica | S3 | `GetObject` | Chave referenciada em `MSG#<ts>#<id>.raw_s3_key` |
| **AP-07** | Listar tarefas e nudges vencidos | DynamoDB | `Query` em `GSI2` | `GSI2PK = T#<tenant>#DUE`, `GSI2SK <= <now_iso8601>` |
| **AP-08** | Criar/atualizar tarefa ou agendar nudge | DynamoDB | `PutItem` | `PK = T#<tenant>#S#<canal>#<source_key>`, `SK = TASK#<id>` com `GSI2PK` e `GSI2SK` |
| **AP-09** | Concluir/cancelar tarefa ou nudge | DynamoDB | `UpdateItem` removendo `GSI2PK` e `GSI2SK` | `PK = T#<tenant>#S#<canal>#<source_key>`, `SK = TASK#<id>` |
| **AP-10** | Obter todas as fontes vinculadas a um contato (`contact_id`) | DynamoDB | `Query` em `GSI1` | `GSI1PK = T#<tenant>#CONTACT#<contact_id>` |
| **AP-11** | Associar fonte existente a um `contact_id` | DynamoDB | `TransactWriteItems` (atualiza META e cria item de link) | `PK = T#<tenant>#S#<canal>#<source_key>`, `SK = META` + Link `PK = T#<tenant>#CONTACT#<contact_id>`, `SK = SRC#<canal>#<source_key>` |
| **AP-12** | Ler configurações do tenant | DynamoDB | `GetItem` ou `Query` | `PK = T#<tenant>`, `SK = CONFIG#<nome>` |
| **AP-13** | Gravar e listar traces de execução do LLM | S3 + DynamoDB | `PutObject` no S3 + `PutItem`/`Query` no DynamoDB (`RUN#<ts>#<id>`) | S3: `<tenant>/traces/<canal>/<source_key>/<run_id>.json` |
| **AP-14** | Ler e gerenciar arquivos de conhecimento | S3 | `GetObject` / `PutObject` / `ListObjectsV2` | `<tenant>/knowledge/*.md` e `<tenant>/knowledge/QA.md` com S3 Versioning |
| **AP-15** | Listar conversas/fontes ativas de um tenant | DynamoDB | `Query` com `SK begins_with SRC#` | `PK = T#<tenant>` |

---

## 4. Layout de Chaves no S3 / MinIO

O bucket S3 único (ou configurável por tenant) adota o seguinte particionamento hierárquico:

```
s3://<bucket-name>/
├── <tenant>/
│   ├── <canal>/
│   │   └── <source_key>/
│   │       └── <yyyy>/
│   │           └── <mm>/
│   │               └── <dd>/
│   │                   └── <ts>_<message_id>.json
│   ├── media/
│   │   └── <canal>/
│   │       └── <source_key>/
│   │           └── <yyyy>/
│   │               └── <mm>/
│   │                   └── <media_id>.<ext>
│   ├── traces/
│   │   └── <canal>/
│   │       └── <source_key>/
│   │           └── <run_id>.json
│   └── knowledge/
│       ├── FAQ.md
│       ├── servicos.md
│       └── QA.md
```

- **S3 Versioning**: Habilitado no bucket, garantindo versionamento automático do prefixo `<tenant>/knowledge/`, em especial para o arquivo gerado de aprendizado `QA.md`.
- **Lifecycle Rules**: Traces do LLM podem expirar em 30 a 90 dias; mídias brutas podem transicionar para Glacier ou expirar conforme política de retenção definida no ADR 0006.
