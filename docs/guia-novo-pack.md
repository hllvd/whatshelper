# Guia de Criação de Novo Vertical Pack

Este guia mostra como adaptar o **Whatshelper Brain** para qualquer novo mercado ou operação sem alterar uma única linha de código Go do `core`.

---

## 1. Estrutura da pasta do pack

Crie um diretório em `packs/<nome-do-pack>/`:

```text
packs/<nome>/
  pack.yaml
  prompts/
    analysis.tmpl
    reply.tmpl
    nudge.tmpl        # opcional; recomendado se o tenant puder habilitar proactive_enabled
```

## 2. Configurando `pack.yaml`

Campos principais:

1. **`name` e `language`**: identificador único do pack e idioma padrão (ex.: `pt-BR`).
2. **`actors`**: papéis usados no histórico e schema (`business` e `customer`, devem ser distintos).
3. **`facts`**: fatos extraídos e atualizados no estado (`string`, `number`, `integer`, `boolean` ou `enum` com `values`).
4. **`signals`**: flags booleanas objetivas detectadas na conversa (ex.: `has_new_question`, `customer_engaged`).
5. **`intents`**: intenções classificadas pelo estágio de análise. Cada intent declara:
   - `name`: valor permitido no enum `intent` do JSON schema
   - `description` e `examples`
   - `action`: `reply` | `escalate` | `create_task` | `handoff` | `opt_out`
6. **`opt_out_keywords`**: lista de palavras ou frases que acionam `opted_out` antes mesmo de chamar o LLM.
7. **`task_rules`**: regras determinísticas para criar tarefas (`TASK#`) quando um `trigger_signal` for `true` ou `trigger_intent` coincidir:
   - `type`, `title`, `context_section` (opcional) e `due_in_minutes`.
8. **`context`**: seções e campos estruturados preenchidos pela análise (ex.: `last_action`, agendamento).
9. **`prompts` e `rules`**: caminhos relativos aos templates `.tmpl` e regras injetadas no prompt.

Veja [`packs/realestate/pack.yaml`](../packs/realestate/pack.yaml) e [`packs/support/pack.yaml`](../packs/support/pack.yaml) como referências completas.

## 3. Templates de prompt (`prompts/*.tmpl`)

Os templates usam `text/template` do Go com `missingkey=error` e um marcador opcional separando system prompt de user prompt:

```text
Instruções de sistema...
{{.OutputSchema}}
<!-- USER_PROMPT -->
Resumo: {{.Summary}}
Fatos: {{range $k, $v := .Facts}}{{$k}}: {{$v}}{{end}}
Nova mensagem: {{.NewMessage.Text}}
```

- Em `analysis.tmpl`, inclua `{{.OutputSchema}}` para o JSON Schema gerado dinamicamente a partir do `pack.yaml`.
- Em `reply.tmpl`, solicite saída JSON estruturada `{"answerable": bool, "sources": [...], "reply": "..."}` e respeite `{{if .PlainTextOnly}}...{{end}}`.
- O motor de grounding validará automaticamente `answerable`, existência dos arquivos citados em `sources` e consistência numérica contra `knowledge/*.md`, `QA.md`, mensagens recentes e fatos.

## 4. Criando um Tenant para o pack

Crie `tenants/<tenant-id>/`:

```text
tenants/<tenant-id>/
  tenant.yaml
  knowledge/
    manual.md
    QA.md
```

Exemplo mínimo de `tenant.yaml`:

```yaml
id: meu-tenant
name: Operação Exemplo
pack: <nome-do-pack>
mode: auto              # auto | approval | draft
timezone: America/Sao_Paulo
bot_enabled: true
proactive_enabled: false
business_hours:
  enabled: true
  days: [mon, tue, wed, thu, fri]
  start: "09:00"
  end: "18:00"
out_of_hours_reply: "Recebemos sua mensagem fora do horário comercial e retornaremos em seguida."
rate_limit:
  global_per_minute: 60
  per_source_per_minute: 10
nudge:
  delay_minutes: 60
  max_attempts: 2
```

## 5. Validando e testando

1. Valide a sintaxe, schema e renderização de templates:
   ```bash
   go run ./cmd/brain pack validate packs/<nome-do-pack>
   ```
2. Crie uma fixture de conversa em `testdata/conversations/<nome>.yaml` e execute:
   ```bash
   make test-race
   ```
3. Suba localmente (`make up`) ou teste em memória via `POST /api/replay` sem alterar conversas reais:
   ```bash
   curl -X POST http://localhost:8080/api/replay \
     -H "Content-Type: application/json" \
     -d '{"tenant_id":"meu-tenant","messages":[{"text":"Qual o horário de atendimento?"}]}'
   ```
