# Protocolo Connector ↔ Brain (`v1`)

Define o contrato de comunicação entre processos de conectores externos (ex.: `cmd/connectors` com `whatsmeow`) e o serviço principal `cmd/brain`.

## 1. Versionamento e Autenticação

- **Versão do protocolo**: `1`
- **Cabeçalho obrigatório nas chamadas HTTP/WS**: `X-Protocol-Version: 1`
- **Autenticação**: `Authorization: Bearer <CONNECTOR_TOKEN>` (faz fallback para `API_TOKEN` se `CONNECTOR_TOKEN` não estiver definido). Em desenvolvimento loopback local sem tokens configurados, o middleware libera acesso apenas para testes locais.

## 2. Endpoints HTTP (Connector → Brain)

### `POST /api/connectors/v1/inbound`
Entrega uma mensagem normalizada ao pipeline do `brain`. O `brain` executa `Pipeline.Ingest` de forma síncrona antes de responder, garantindo persistência e deduplicação por `message_id` antes do ACK.

**Payload JSON:**
```json
{
  "tenant_id": "demo",
  "channel": "whatsapp",
  "account": "main",
  "source_key": "5511999998888",
  "message_id": "wamid-123",
  "timestamp": "2026-11-11T22:00:00Z",
  "type": "text",
  "text": "Olá, gostaria de saber o horário.",
  "media_ref": "",
  "raw_contact": "+55 11 99999-8888",
  "display_name": "Maria",
  "route_to_admin": false
}
```
> Campos `raw_contact` e `display_name` são transientes: o `brain` cifra-os apenas em `SourceMeta.EncryptedContactInfo` (AES-256-GCM) e **não** os grava em texto claro no S3 (`raw/`).

**Respostas:**
- `202 Accepted` `{ "status": "accepted", "message_id": "..." }` — mensagem nova ingerida.
- `200 OK` `{ "status": "duplicate", "message_id": "..." }` — já processada anteriormente (idempotente).
- `400 Bad Request` — erro permanente de contrato (connector **não** deve fazer retry).
- `401 Unauthorized` — token inválido ou ausente.
- `5xx` — falha transitória (connector faz retry com backoff exponencial).

### `POST /api/connectors/v1/media`
Faz upload de mídia recebida (`multipart/form-data`) para persistência no `BlobStore` (`MinIO`/`S3`) antes do envio de `/inbound`.

**Campos `multipart/form-data`:**
- `tenant_id`, `channel`, `source_key`, `media_id`, `ext`, `mime_type` (opcional), `timestamp` (RFC3339, opcional) e arquivo no campo `file`.

**Resposta (`201 Created`):**
```json
{
  "media_ref": "demo/media/whatsapp/5511999998888/2026/11/wamid-123.ogg"
}
```

## 3. Canal Bidirecional WebSocket (Brain ↔ Connector)

### `GET /api/connectors/v1/ws?channel=<channel>&account=<account>`
Mantido conectado pelo connector com reconexão automática e backoff exponencial (`200ms` base até `30s`).

**Frames Brain → Connector:**
- `send`:
  ```json
  { "type": "send", "request_id": "req-1", "message": { ...NormalizedMessage... } }
  ```
- `typing`:
  ```json
  { "type": "typing", "source_key": "5511999998888", "typing": true }
  ```
- `ping`: `{ "type": "ping" }`

**Frames Connector → Brain:**
- `ack`:
  ```json
  { "type": "ack", "request_id": "req-1", "ok": true, "error": "" }
  ```
- `health`:
  ```json
  { "type": "health", "connected": true, "detail": "whatsapp online" }
  ```
- `pong`: `{ "type": "pong" }`
