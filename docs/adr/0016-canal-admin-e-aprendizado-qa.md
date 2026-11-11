# ADR 0016: Canal Admin dedicado e fluxo de aprendizado contínuo em QA.md

- **Status**: Aceito
- **Data**: 2026-11-11
- **Contexto**: M3 (`CH-08`, `LRN-01` a `LRN-06`)

## Decisões

1. **Canal `admin` como `core.Channel` dedicado**:
   - Implementado em `internal/channels/admin` com `ChannelID = "admin"` e conversa isolada por tenant em `PK = T#<tenant>#S#admin#owner` (`SUMMARY`/`MSG#` seguem o mesmo contrato de Storage das demais conversas, sem misturar no histórico de clientes).
   - Localmente (M3), a interação acontece via UI web e endpoints `/api/admin/messages` + `/api/learning/*`. No M4 (WhatsApp real), mensagens recebidas de um número admin autorizado poderão ser encaminhadas para o mesmo pipeline/parser de comandos admin sem alterar a lógica de aprendizado.

2. **Separação explícita entre escalonamento, proposta (`learning`) e gravação em `QA.md`**:
   - Escalonamento (`TASK#` `kind=escalation`) notifica o canal `admin` com resumo, pergunta do cliente e origem.
   - A correção do dono nunca altera `QA.md` diretamente: passa pelo estágio cognitivo `learning` (modelo configurável via `MODEL_LEARNING`, com fallback para `MODEL_RESPONSE` se omitido), vira uma `LearningProposal` persistida em `<tenant>/learning/<id>.json` + `TASK#` `kind=learning`, e só grava em `QA.md` após aprovação explícita (`/aprovar` ou UI/API).

3. **Dedupe/merge e versionamento**:
   - `QA.md` utiliza formato determinístico `P: ...` / `R: ...` parseado em memória e mesclado por similaridade léxica normalizada antes de gravar no MinIO/S3, aproveitando o S3 Versioning já ativo no bucket `whatshelper-blobs`.
