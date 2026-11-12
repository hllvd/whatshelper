# Whatshelper Brain

Brain agnóstico de atendimento por mensageria construído em Go. Responde clientes com base em arquivos `.md` de conhecimento, aprende com correções do proprietário e funciona com qualquer canal (WhatsApp, email, simulador) e qualquer mercado através de vertical packs.

---

## Arquitetura e Decisões Técnicas

- **Linguagem & Padrão**: Go moderno com arquitetura hexagonal (Ports & Adapters). Domínio puro sem dependências externas ou termos específicos de mercado (`TestCoreAgnosticism_NoVerticalTermsInInternal`).
- **Armazenamento**:
  - **DynamoDB (Tabela Única)**: Estado da conversa (`META`, `SUMMARY`, `FACTS`, `SIGNALS`, `CONTEXT`), metadados de mensagens (`MSG#`), tarefas/aprovações/nudges (`TASK#`), execuções com TTL de 90 dias (`RUN#`), políticas do tenant (`CONFIG#policy`), catálogo de fontes (`SRC#`), agenda (`GSI2`) e link de contato (`GSI1`).
  - **S3 / MinIO**: Payloads brutos de mensagens (`<tenant>/<canal>/<source_key>/...`), mídia (`media/`), rascunhos (`drafts/`), traces com lifecycle ILM de 90 dias (`traces/`) e conhecimento versionado (`knowledge/`).
- **Agnóstico de Canal**: Todos os canais comunicam através da interface `core.Channel` e do envelope `core.NormalizedMessage`.
- **Agnóstico de Mercado**: Intents, schema de fatos, sinais, regras de tarefas e templates vivem exclusivamente em `packs/<nome>/` (exemplos inclusos: `packs/realestate` e `packs/support`).
- **Segurança & Políticas**: Gate pré-LLM (bot_enabled, pausa/humano/opt-out, rate limit em token bucket), horário comercial, grounding determinístico com escalonamento neutro e modos `auto`, `approval` e `draft`.

---

## Como rodar localmente

### Pré-requisitos
- Docker & Docker Compose
- Go 1.26+ (para desenvolvimento local)

### Subir infraestrutura e Brain
```bash
make up
```
Isso iniciará:
- DynamoDB Local (`localhost:8000`)
- MinIO S3 Local (`localhost:9000` / Console em `localhost:9001`)
- Bucket `whatshelper-blobs` com S3 Versioning ativo e ILM (90 dias para `traces/`)
- Serviço `store-init` (`brain store init`) criando a tabela única e semeando políticas (`CONFIG#policy`) e conhecimento (`knowledge/*.md`) para `demo` (`realestate`) e `demo-support` (`support`)
- Whatshelper Brain exposto em loopback (`127.0.0.1:8080`; suporte opcional a `API_TOKEN` Bearer Auth e `PII_ENCRYPTION_KEY` AES-256-GCM)
- Whatshelper Connector WhatsApp em processo separado (`127.0.0.1:8081`, protocolo `v1` via HTTP/WebSocket com `CONNECTOR_TOKEN`, sessão SQLite sem CGO persistida no volume `whatsapp_data`)

### Pareamento WhatsApp Local (`whatsmeow`)
- Com os containers ativos, acompanhe os logs do `connector` ou consulte `GET http://127.0.0.1:8081/qr` para obter o QR Code atual.
- Defina `ADMIN_PHONE_E164` no `.env` para rotear automaticamente mensagens desse número para o canal `admin` (`/aprovar`, `/rejeitar`, `/corrigir`).
- Opcional: configure `TRANSCRIPTION_URL` / `TRANSCRIPTION_API_KEY` para transcrição automática de áudio (sem ela, áudios escalonam com resposta neutra).

### Modo Offline (`fake`) vs LLM Real (`anthropic`, `gemini`, `deepseek`) e `.env`
- Tanto `cmd/brain` / `cmd/connectors` (ao rodar localmente com Go) quanto o `docker compose` carregam automaticamente o arquivo `.env` na raiz do projeto (ou `ENV_FILE`), sem sobrescrever variáveis já exportadas no shell.
- Por padrão (sem chaves configuradas), o sistema sobe com `LLM_PROVIDER=fake` para rodar 100% offline.
- Para testar com LLM real, edite o `.env` definindo `LLM_PROVIDER` (`anthropic`, `gemini` ou `deepseek`), a respectiva chave (`ANTHROPIC_API_KEY`, `GEMINI_API_KEY` ou `DEEPSEEK_API_KEY`) e os modelos por estágio (`MODEL_ANALYSIS`, `MODEL_RESPONSE`, `MODEL_NUDGE` e opcionalmente `MODEL_LEARNING`), consultando a documentação oficial de cada provedor.
- Em seguida suba com `make up` ou execute `./scripts/tests.sh live`.

### Interfaces Web Locais
Com o ambiente rodando:
- **Chat Simulador**: [http://localhost:8080/](http://localhost:8080/) — envie mensagens como cliente (escolha tenant `demo` ou `demo-support`), pause/assuma/reative conversas e aprove ou conclua tarefas pendentes.
- **Tela "Pensando"**: [http://localhost:8080/thinking](http://localhost:8080/thinking) — acompanhe em tempo real (SSE) prompts, resposta crua, JSON parseado, diff de fatos, decisão do roteador, latência e tokens.
- **Tela "Conhecimento & Política"**: [http://localhost:8080/knowledge](http://localhost:8080/knowledge) — edite arquivos `knowledge/*.md` e `QA.md`, navegue/restaure versões históricas S3, ajuste políticas (`bot_enabled`, `mode`, `proactive_enabled`), aprove/rejeite propostas de aprendizado e interaja com o Canal Admin (`/aprovar`, `/rejeitar`, `/corrigir`).

### Smoke Test Manual (API)
```bash
# Enviar mensagem no simulador (tenant demo ou demo-support)
curl -X POST http://localhost:8080/api/sim/messages \
  -H "Content-Type: application/json" \
  -d '{"tenant_id":"demo","source_key":"smoke-1","text":"Olá, meu orçamento é 750000, aceitam pet?"}'

# Consultar estado, tarefas e runs (aguarde ~2s pelo debounce padrão)
curl "http://localhost:8080/api/sources/simulator/smoke-1/state?tenant=demo"
curl "http://localhost:8080/api/sources/simulator/smoke-1/tasks?tenant=demo"
curl "http://localhost:8080/api/sources/simulator/smoke-1/runs?tenant=demo"

# Executar replay isolado em memória (não altera o Store real)
curl -X POST http://localhost:8080/api/replay \
  -H "Content-Type: application/json" \
  -d '{"tenant_id":"demo-support","messages":[{"text":"Qual o SLA do plano Pro?"}]}'
```

### Encerrar ambiente
```bash
make down
```

---

## Testes e Validação de Código

```bash
# Rodar todos os testes unitários e golden tests (offline)
make test

# Rodar com detecção de condições de corrida (race detector)
make test-race

# Rodar testes de integração contra DynamoDB Local e MinIO (requer make up)
make test-integration

# Validar código (go vet) e estrutura/templates de todos os packs
make lint

# Validar um pack específico via CLI
go run ./cmd/brain pack validate packs/realestate

# (Opcional) Validar smoke & golden tests contra API real da Anthropic
make llm-check
make test-live

# Compilar binários locais em bin/
make build

# Ou executar a suíte completa automatizada por categoria (logs em artifacts/)
./scripts/tests.sh           # tudo: lint, unit, brain, api, build, integration
./scripts/tests.sh unit      # unitários e contratos in-memory (-race)
./scripts/tests.sh brain     # pipeline cognitivo e golden fixtures (-race)
./scripts/tests.sh api       # HTTP API, SSE e replay (-race)
./scripts/tests.sh build     # sobe/valida containers e logs de startup
./scripts/tests.sh integration
```

---

## Documentação e Padrões de Acesso

- [Protocolo Connector ↔ Brain v1](docs/connector-protocol.md)
- [Guia para criar um novo Vertical Pack](docs/guia-novo-pack.md)
- [Padrões de Acesso ao Armazenamento (DynamoDB + S3)](docs/access-patterns.md)
- [Registros de Decisão Arquitetural (ADRs)](docs/adr/)
  - [ADR 0001: Go e Arquitetura Hexagonal](docs/adr/0001-go-language-and-architecture.md)
  - [ADR 0002: Núcleo Agnóstico de Canal](docs/adr/0002-channel-agnostic-core.md)
  - [ADR 0003: Vertical Packs](docs/adr/0003-vertical-packs.md)
  - [ADR 0004: DynamoDB Single-Table](docs/adr/0004-single-table-dynamodb.md)
  - [ADR 0005: S3 para Mensagens Cruas e Blobs](docs/adr/0005-s3-raw-messages-and-blobs.md)
  - [ADR 0006: Identidade e Criptografia de Contato](docs/adr/0006-identity-and-pii.md)
  - [ADR 0007: Infraestrutura Local em Containers](docs/adr/0007-local-infrastructure-containers.md)
  - [ADR 0008: Ajustes de Schema de Chaves](docs/adr/0008-key-schema-adjustments.md)
  - [ADR 0009: Validador JSON Orientado pelo Pack](docs/adr/0009-pack-driven-json-validator.md)
  - [ADR 0010: Eventos em Tempo Real via SSE](docs/adr/0010-sse-realtime-events.md)
  - [ADR 0011: Configuração do Tenant Demo](docs/adr/0011-single-demo-tenant-config.md)
  - [ADR 0012: Commit Atômico e Concorrência Otimista](docs/adr/0012-store-atomic-commit-and-optimistic-locking.md)
  - [ADR 0013: Grounding Determinístico e AdminNotifier](docs/adr/0013-grounding-e-notificacao-admin.md)
  - [ADR 0014: Nudges Reutilizam TASK# e GSI2](docs/adr/0014-nudge-como-task-gsi2.md)
  - [ADR 0015: Políticas, Conhecimento em MinIO e Bind Seguro](docs/adr/0015-politicas-seguranca-e-bind-local.md)
  - [ADR 0016: Canal Admin e Aprendizado Contínuo em QA.md](docs/adr/0016-canal-admin-e-aprendizado-qa.md)
  - [ADR 0017: Connector Separado (`cmd/connectors`) e Protocolo v1](docs/adr/0017-connector-separado-protocolo-v1.md)
