# ADR 0010: Streaming de Eventos em Tempo Real via Server-Sent Events (SSE)

- **Status:** Aceito
- **Data:** 2026-11-11
- **Decisores:** Engenharia Whatshelper

## Contexto
O servidor local precisa transmitir em tempo real para o navegador os eventos do pipeline cognitivo (`message.received`, `prompt.sent`, `llm.responded`, `facts.diff`, `reply.generated`, `reply.sent`, `pipeline.failed`) para alimentar o chat simulador e a tela de visualização "pensando" (SRV-02).

## Decisão
Utilizar **Server-Sent Events (SSE)** sobre HTTP/1.1 puro no endpoint `GET /api/events` (com filtros opcionais `channel` e `source`), utilizando exclusivamente a biblioteca padrão `net/http` (`http.Flusher`) e `EventSource` nativo no navegador:
- Eventos são emitidos no formato `id: <event_id>\nevent: <type>\ndata: <json>\n\n`.
- Um *heartbeat* periódico (`: ping\n\n` a cada 15s) mantém conexões ociosas vivas através de proxies.
- Um buffer circular em memória mantém os últimos eventos recentes para replay *best-effort* quando o cliente reconecta enviando o cabeçalho `Last-Event-ID`.

## Consequências
- **Positivas:**
  - Zero dependências externas de WebSocket no servidor ou no cliente.
  - Reconexão automática nativa do navegador via `EventSource` com `Last-Event-ID`.
  - Comunicação perfeitamente unidirecional (servidor → visualizador), enquanto ações do cliente usam HTTP `POST` padrão.
- **Negativas:**
  - Limite de conexões HTTP/1.1 simultâneas por domínio no navegador (mitigável no futuro com HTTP/2 se necessário).
