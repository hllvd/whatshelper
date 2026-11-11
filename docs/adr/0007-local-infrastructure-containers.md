# ADR 0007: Imagens e Verificações de Saúde para Infraestrutura Local (Docker Compose)

- **Status:** Aceito
- **Data:** 2026-11-11
- **Decisores:** Engenharia Whatshelper

## Contexto
O desenvolvimento e testes locais exigem instâncias de DynamoDB Local e MinIO (compatível com S3) rodando em contêineres Docker via `docker-compose`. Durante a configuração inicial:
1. Os repositórios públicos originais `minio/minio` e `minio/mc` no Docker Hub e `quay.io/minio/minio` recusaram pull anônimo (`pull access denied` / `401 UNAUTHORIZED`).
2. O projeto requer que `make up` funcione em qualquer máquina limpa ou ambiente de CI sem exigir `docker login` nem tags mutáveis ou aliases locais manuais.
3. O contêiner de DynamoDB Local não possui `pidof`, mas inclui `/usr/bin/curl`.
4. O contêiner MinIO necessita de permissão de escrita em `/data` e possui `/usr/bin/wget` para healthcheck.

## Decisão
1. **Imagens de MinIO e Client fixadas por digest**:
   - Utilizar `cleanstart/minio@sha256:c79d95411873282e192209a7af1a7d7524aeae2371bf53737b1e6ae6baa0c06d` (build Chainguard-forked de MinIO, rodando com `user: root` localmente para escrita no volume `/data`) e healthcheck via `wget -q --spider http://localhost:9000/minio/health/live`.
   - Utilizar `drpsychick/minio-client@sha256:8fb24fd225727f183710eaed1fd6ed378ee87a152a47002e65c22f9383a3d807` para o contêiner efêmero `minio-init` que cria o bucket `whatshelper-blobs` e ativa o S3 Versioning.
2. **DynamoDB Local**:
   - Utilizar a imagem oficial `amazon/dynamodb-local:latest` com healthcheck HTTP `curl -s http://localhost:8000/ > /dev/null || exit 1`.

## Riscos e Mitigação (Supply Chain)
- O uso de imagens de terceiros (`cleanstart/minio`, `drpsychick/minio-client`) traz risco de supply chain em comparação ao mantenedor oficial.
- **Mitigação**: As imagens estão fixadas por **digest SHA-256 imutável** (não usam `:latest` mutável) e restringem-se exclusivamente ao ambiente local de desenvolvimento e testes. Em produção (M5), será utilizado o AWS S3 gerenciado. Caso uma conta oficial ou mirror corporativo esteja disponível no futuro, basta trocar a referência no `docker-compose.yml`.

## Consequências
- **Positivas:**
  - `make up` é 100% reproduzível em qualquer máquina sem configuração manual de Docker nem `docker login`.
- **Negativas:**
  - Atualizações de versão do MinIO local exigem atualização explícita do digest SHA-256 no `docker-compose.yml`.
