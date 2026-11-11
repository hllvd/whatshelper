# ADR 0015: Políticas por tenant no Store, bind loopback e Bearer Auth opcional

- **Status**: Aceito
- **Data**: 2026-11-11
- **Contexto**: M2 (`BRAIN-08`, `SAF-02`, `SAF-03`, `SRV-07`, `STO-07`)

## Decisões

1. **Política do tenant em `CONFIG#policy` (`Store`)**: As políticas operacionais (`mode`, `bot_enabled`, `proactive_enabled`, `timezone`, `business_hours`, `rate_limit`, `nudge`) são semeadas a partir de `tenants/<id>/tenant.yaml` durante `brain store init` para `PK = T#<tenant>, SK = CONFIG#policy`, permitindo alteração dinâmica via API/UI sem reiniciar o serviço. Quando `CONFIG#policy` não existir no Store (ex.: testes isolados), há fallback explícito para `LocalTenantLoader`.
2. **Conhecimento editável em MinIO/S3**: `brain store init` semeia `tenants/<id>/knowledge/*.md` para o bucket apenas quando ainda não existem lá (não sobrescreve edições feitas pela UI `/knowledge`). O `BlobKnowledgeSource` recarrega por TTL curto (5s) e é invalidado imediatamente após `PUT /api/knowledge/{file}`.
3. **Bind seguro por padrão (`SRV-07`)**: `HTTP_HOST` padrão é `127.0.0.1`. O processo recusa subir com `HTTP_HOST` não-loopback (ex.: `0.0.0.0`) sem `API_TOKEN`, salvo flag explícita `ALLOW_INSECURE_BIND=true` usada no `docker-compose.yml` onde o mapeamento de porta no host já é restrito a `127.0.0.1:8080:8080`.
4. **Retenção (`STO-07`)**: Itens `RUN#` recebem atributo `ttl` (padrão 90 dias via `RUN_RETENTION_DAYS`), com TTL ativado na tabela pelo `EnsureTable`. Traces em S3/MinIO recebem regra ILM de expiração em 90 dias via `mc ilm rule add` no `minio-init`.
