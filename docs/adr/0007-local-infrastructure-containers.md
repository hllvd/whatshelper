# ADR 0007: Imagens e Verificações de Saúde para Infraestrutura Local (Docker Compose)

- **Status:** Aceito
- **Data:** 2026-11-05
- **Decisores:** Engenharia Whatshelper

## Contexto
O desenvolvimento e testes locais exigem instâncias de DynamoDB Local e MinIO (compatível com S3) rodando em contêineres Docker via `docker-compose`. Durante a inicialização:
1. Determinados repositórios públicos de imagens (como `minio/minio` no Docker Hub e Quay) passaram a restringir acessos anônimos (`pull access denied` / 401).
2. O contêiner de DynamoDB Local não possui o utilitário `pidof` para probes de processo, mas inclui `/usr/bin/curl`.
3. O contêiner do MinIO requer permissão de gravação no volume persistente montado em `/data`, e seu healthcheck utiliza `/usr/bin/wget`.

## Decisão
1. **MinIO Server e Client**:
   - Utilizar a imagem de distribuição MinIO com `/usr/bin/wget` para healthcheck em `/minio/health/live`.
   - Executar o contêiner MinIO com usuário apropriado (`user: root`) para assegurar propriedade do diretório de volume `/data`.
   - Utilizar contêiner efêmero de inicialização (`minio-init`) com `mc` para provisionar o bucket padrão `whatshelper-blobs` e habilitar o S3 Versioning automaticamente na primeira execução.
2. **DynamoDB Local**:
   - Utilizar a imagem oficial `amazon/dynamodb-local:latest`.
   - Configurar o healthcheck via probe HTTP com `curl -s http://localhost:8000/ > /dev/null || exit 1`.

## Consequências
- **Positivas:**
  - O comando `make up` sobe todo o ecossistema local (DynamoDB, MinIO com bucket versionado e o serviço Brain) de forma 100% autônoma, saudável e confiável.
  - Zero dependência de contas ou recursos da AWS durante os milestones locais (M0 a M4).
- **Negativas:**
  - O contêiner MinIO precisa rodar como root localmente para gerenciamento de volumes montados pelo Docker Desktop.
