# ADR 0009: Grounding determinístico e notificação de escalonamento ao operador

- **Status**: Aceito
- **Data**: 2026-11-11
- **Contexto**: M2 (`BRAIN-05`, `SAF-05`)

## Decisão

1. **Envelope estruturado na resposta**: O estágio de resposta solicita do LLM um JSON `{"answerable": bool, "sources": [...], "reply": "..."}`. Quando `answerable == false` (ou em caso de pack simples sem envelope JSON), o backend aplica validação determinística (`CheckGrounding`) antes de enviar qualquer texto ao cliente:
   - Se `answerable == false`: não envia a resposta especulativa do LLM.
   - Se `sources` citar arquivo ausente no `KnowledgeBundle`: bloqueia o envio.
   - Se o texto de `reply` contiver números (preços, prazos, quantidades com dígitos) ausentes no conhecimento, `QA.md`, mensagens da conversa e fatos extraídos: bloqueia o envio.
2. **Fallback neutro e `AdminNotifier`**: Sempre que o grounding falha (ou a intenção tem ação `escalate`), o cliente recebe apenas uma mensagem neutra padronizada (`NeutralFallbackReply` / `NeutralEscalationReply`) e o `DefaultAdminNotifier` atua em três camadas desacopladas por porta (`core.AdminNotifier`):
   - **Log estruturado** (`slog.Warn`)
   - **Evento SSE** (`escalation.triggered`)
   - **Tarefa persistida no Store** (`TASK#<id>` com `kind=escalation`)

## Consequências

- Canal de notificação externo (webhook/Slack/email para o operador) poderá implementar `core.AdminNotifier` no M3/M4 sem alterar o `Pipeline`.
- Packs legados ou testes sem envelope JSON no template continuam suportados via fallback de `ParseReplyOutput` (`isStructured=false`), preservando verificação numérica e sanitização de texto puro.
