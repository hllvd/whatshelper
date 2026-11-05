# Whatshelper Brain

Brain agnóstico de atendimento por mensageria construído em Go. Responde clientes com base em arquivos `.md` de conhecimento, aprende com correções do proprietário e funciona com qualquer canal (WhatsApp, email, simulador) e qualquer mercado através de vertical packs.

---

## Arquitetura e Decisões Técnicas

- **Linguagem & Padrão**: Go moderno com arquitetura hexagonal (Ports & Adapters). Domínio puro sem dependências externas.
- **Armazenamento**:
  - **DynamoDB (Tabela Única)**: Estado da conversa (`META`, `SUMMARY`, `FACTS`, `SIGNALS`, `CONTEXT`), metadados de mensagens (`MSG#`), tarefas operacionais (`TASK#`), nudges (`GSI2`) e link de contato (`GSI1`).
  - **S3 / MinIO**: Payloads brutos de mensagens (`raw/`), anexos de mídia (`media/`), traces de execução (`traces/`) e base de conhecimento versionada (`knowledge/`).
- **Agnóstico de Canal**: Todos os canais comunicam através da interface `core.Channel` e do envelope `core.NormalizedMessage`.
- **Agnóstico de Mercado**: Os prompts, esquemas de fatos, sinais e intents vivem em pacotes verticais (`packs/<nome>/`). O core nunca referencia termos específicos de mercado.

---

## Como rodar localmente

### Pré-requisitos
- Docker & Docker Compose
- Go 1.24+ (para desenvolvimento local)

### Subir infraestrutura e Brain
```bash
make up
```
Isso iniciará:
- DynamoDB Local (`localhost:8000`)
- MinIO S3 Local (`localhost:9000` / Console em `localhost:9001`)
- Bucket `whatshelper-blobs` provisionado automaticamente com S3 Versioning
- Whatshelper Brain (`localhost:8080`)

### Verificar saúde da aplicação
```bash
curl http://localhost:8080/healthz
curl http://localhost:8080/readyz
```

### Encerrar ambiente
```bash
make down
```

---

## Testes e Validação de Código

```bash
# Rodar todos os testes unitários
make test

# Rodar com detecção de condições de corrida (race detector)
make test-race

# Formatar e executar linter (go vet)
make fmt
make lint

# Compilar binários locais em bin/
make build
```

---

## Documentação e Padrões de Acesso

- [Padrões de Acesso ao Armazenamento (DynamoDB + S3)](docs/access-patterns.md)
- [Registros de Decisão Arquitetural (ADRs)](docs/adr/)
  - [ADR 0001: Go e Arquitetura Hexagonal](docs/adr/0001-go-language-and-architecture.md)
  - [ADR 0002: Núcleo Agnóstico de Canal](docs/adr/0002-channel-agnostic-core.md)
  - [ADR 0003: Vertical Packs](docs/adr/0003-vertical-packs.md)
  - [ADR 0004: DynamoDB Single-Table](docs/adr/0004-single-table-dynamodb.md)
  - [ADR 0005: S3 para Mensagens Cruas e Blobs](docs/adr/0005-s3-raw-messages-and-blobs.md)
  - [ADR 0006: Identidade, Source Key e PII](docs/adr/0006-identity-and-pii.md)
  - [ADR 0007: Infraestrutura Local em Containers](docs/adr/0007-local-infrastructure-containers.md)
