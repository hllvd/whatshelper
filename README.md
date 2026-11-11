# Whatshelper Brain

Brain agnóstico de atendimento por mensageria construído em Go. Responde clientes com base em arquivos `.md` de conhecimento, aprende com correções do proprietário e funciona com qualquer canal (WhatsApp, email, simulador) e qualquer mercado através de vertical packs.

---

## Arquitetura e Decisões Técnicas

- **Linguagem & Padrão**: Go moderno com arquitetura hexagonal (Ports & Adapters). Domínio puro sem dependências externas.
- **Armazenamento**:
  - **DynamoDB (Tabela Única)**: Estado da conversa (`META`, `SUMMARY`, `FACTS`, `SIGNALS`, `CONTEXT`), metadados de mensagens (`MSG#`), tarefas operacionais (`TASK#`), execuções (`RUN#`), catálogo de fontes do tenant (`SRC#`), nudges (`GSI2`) e link de contato (`GSI1`).
  - **S3 / MinIO**: Payloads brutos de mensagens (`<tenant>/<canal>/<source_key>/...`), anexos de mídia (`media/`), traces de execução (`traces/`) e base de conhecimento versionada (`knowledge/`).
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
- Serviço `store-init` (`brain store init`) criando a tabela única e índices
- Whatshelper Brain (`localhost:8080`)

### Modo Offline (`fake`) vs Modo Anthropic (`anthropic`)
- Por padrão (sem `ANTHROPIC_API_KEY`), o `make up` sobe com `LLM_PROVIDER=fake`, usando um modelo determinístico offline para testar o simulador e o visualizador sem chamadas externas.
- Para usar a API real da Anthropic, confira os IDs de modelos atuais na documentação da Anthropic e defina no ambiente ou em `.env`:
  ```bash
  export LLM_PROVIDER=anthropic
  export ANTHROPIC_API_KEY=sk-ant-...
  export MODEL_ANALYSIS=<modelo-anthropic-analise>
  export MODEL_RESPONSE=<modelo-anthropic-resposta>
  export MODEL_NUDGE=<modelo-anthropic-nudge>
  make up
  ```

### Simulador Web e Visualizador "Pensando"
Com o ambiente rodando:
- **Chat Simulador**: abra [http://localhost:8080/](http://localhost:8080/) para conversar como cliente (`tenant: demo`, pack `realestate`, conhecimento em `tenants/demo/knowledge/`).
- **Tela "Pensando"**: abra [http://localhost:8080/thinking](http://localhost:8080/thinking) para acompanhar em tempo real (SSE) o prompt enviado, resposta crua, JSON parseado, diff de fatos, latência e tokens por execução.

### Smoke Test Manual (API)
```bash
curl -X POST http://localhost:8080/api/sim/messages \
  -H "Content-Type: application/json" \
  -d '{"tenant_id":"demo","source_key":"smoke-1","text":"Olá, meu orçamento é 750000, aceitam pet?"}'

# Aguarde ~2s (debounce padrão 1500ms) e consulte o estado e mensagens:
curl "http://localhost:8080/api/sources/simulator/smoke-1/state?tenant=demo"
curl "http://localhost:8080/api/sources/simulator/smoke-1/runs?tenant=demo"
```

### Encerrar ambiente
```bash
make down
```

---

## Testes e Validação de Código

```bash
# Rodar todos os testes unitários (offline)
make test

# Rodar com detecção de condições de corrida (race detector)
make test-race

# Rodar testes de integração contra DynamoDB Local e MinIO (requer make up)
make test-integration

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
  - [ADR 0008: Ajustes no Schema de Chaves (DynamoDB + S3)](docs/adr/0008-key-schema-adjustments.md)
  - [ADR 0009: Validador de Saída JSON Guiado pelo Pack](docs/adr/0009-pack-driven-json-validator.md)
  - [ADR 0010: Streaming de Eventos via SSE](docs/adr/0010-sse-realtime-events.md)
  - [ADR 0011: Configuração do Tenant Demo no M1](docs/adr/0011-single-demo-tenant-config.md)
  - [ADR 0012: Port Store com CommitAnalysis e Versão Otimista](docs/adr/0012-store-atomic-commit-and-optimistic-locking.md)
