# ADR 0017: Connector Separado (`cmd/connectors`) e Protocolo v1

- **Status**: Aceito
- **Data**: 2026-11-11
- **Contexto**: M4 (`DEC-03`, `CH-02`, `CH-04`)

## Contexto e Problema

O canal WhatsApp (baseado em `whatsmeow` + banco SQLite local da sessão) mantém estado de rede persistente (WebSocket encriptado Noise com servidores WhatsApp) e grava chaves criptográficas/sessão em disco (`whatsapp_data`). Rodar esse cliente dentro do mesmo processo do `brain` acoplaria reinícios do servidor cognitivo/API à reconexão do WhatsApp e misturaria responsabilidades de transporte de canal com orquestração de estado/LLM.

## Opções Consideradas

1. **In-process no `cmd/brain`**: menor latência inicial, mas derruba a sessão WhatsApp a cada reinício/deploy do `brain` e impede escalonamento ou implantação independente.
2. **Processo/serviço separado (`cmd/connectors`)**: isola sessão `whatsmeow` (volume `whatsapp_data`) dos reinícios do `brain`, valida localmente no `docker-compose.yml` a fronteira HTTP/WebSocket autenticada que também será usada no deploy ECS/Fargate (M5), e mantém o core do `brain` 100% agnóstico a transporte específico de canal.

## Decisão

1. Adotamos **serviço/processo separado (`cmd/connectors`)** comunicando-se com o `brain` via **Protocolo de Connector `v1`**:
   - **Inbound + Mídia (Connector -> Brain)**: HTTP `POST /api/connectors/v1/inbound` e `POST /api/connectors/v1/media`, com cabeçalho `X-Protocol-Version: 1`, autenticação `Authorization: Bearer <CONNECTOR_TOKEN>` (fallback para `API_TOKEN`), e retries idempotentes com backoff exponencial baseados em `message_id` (deduplicados no `Store`).
   - **Outbound + Typing + Health (Brain <-> Connector)**: WebSocket autenticado em `GET /api/connectors/v1/ws` encapsulado no `brain` por um `RemoteChannel` que implementa `core.Channel` sem alterar o pipeline.
2. No M4, toda a operação permanece **100% local** via `docker-compose.yml` com volume Docker dedicado para SQLite (`modernc.org/sqlite`, puro Go, preservando `CGO_ENABLED=0`).

## Consequências

- **Positivas**:
  - Reinícios do `brain` não forçam novo pareamento ou queda brusca do cliente WhatsApp.
  - O contrato `RemoteChannel` <-> `cmd/connectors` prova desde o M4 a separação distribuída necessária para M5.
- **Negativas / Trade-offs**:
  - Exige autenticação mútua por token (`CONNECTOR_TOKEN`) e buffer/retry no connector caso o `brain` reinicie temporariamente.
