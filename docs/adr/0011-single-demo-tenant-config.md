# ADR 0011: Configuração do Tenant Demo via Arquivo no M1

- **Status:** Aceito
- **Data:** 2026-11-11
- **Decisores:** Engenharia Whatshelper

## Contexto
No M1 (fatia vertical local), o objetivo é validar o fluxo completo ponta a ponta (`Simulador → Brain → Store → Visualizador`) sem depender ainda de UI administrativa nem CRUD completo de múltiplos tenants em banco (`CONFIG#` no DynamoDB, previsto para o M2).

## Decisão
1. Carregar a configuração do tenant de demonstração a partir de `tenants/<tenant>/tenant.yaml` (contendo `id`, `name`, `pack`, e flags iniciais) através de uma interface `TenantConfigLoader`.
2. Os arquivos de conhecimento iniciais vivem em `tenants/<tenant>/knowledge/*.md` e `QA.md`, servidos por `LocalKnowledgeLoader` com recarga automática por *fingerprint* (mtime + tamanho), mantendo compatibilidade com `BlobKnowledgeLoader` (S3/MinIO).
3. No M2, a implementação de `TenantConfigLoader` passará a ler `PK = T#<tenant>, SK = CONFIG#<nome>` no DynamoDB com fallback ou seed inicial de disco.

## Consequências
- **Positivas:**
  - `make up` já sobe com um tenant funcional (`demo`) ligado ao pack `realestate` e conhecimento de exemplo montado no contêiner.
  - Desacoplamento limpo via interface para evolução multi-tenant no M2.
- **Negativas:**
  - Alterar o `pack` ou flags no M1 exige editar `tenants/demo/tenant.yaml`.
