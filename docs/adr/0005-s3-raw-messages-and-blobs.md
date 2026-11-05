# ADR 0005: Armazenamento em S3 para Mensagens Cruas, Mídias e Traces

- **Status:** Aceito
- **Data:** 2026-11-05
- **Decisores:** Engenharia Whatshelper

## Contexto
O limite de tamanho de um item no DynamoDB é de 400 KB, tornando inviável o armazenamento de payloads brutos de mensagens com anexos, áudios, imagens, documentos ou históricos longos de traces de LLM (que incluem prompts completos de sistema e respostas verbose). Além disso, os arquivos de conhecimento (`knowledge/*.md` e `QA.md`) precisam de suporte a versionamento e recuperação granular.

## Decisão
1. **Separação Estrita de Armazenamento**:
   - DynamoDB armazena apenas **estado estruturado** e **metadados** com ponteiros para o S3.
   - S3 (ou MinIO em desenvolvimento local) armazena todos os blobs:
     - Mensagem crua: `<tenant>/<canal>/<source_key>/<yyyy>/<mm>/<dd>/<ts>_<message_id>.json`
     - Mídia: `<tenant>/media/<canal>/<source_key>/<yyyy>/<mm>/<media_id>.<ext>`
     - Traces: `<tenant>/traces/<canal>/<source_key>/<run_id>.json`
     - Conhecimento: `<tenant>/knowledge/` com S3 Versioning habilitado.
2. **Interface `BlobStore`**:
   - Todo acesso ao S3 é abstraído pela interface `BlobStore` em `internal/core`, permitindo implementação em memória para testes e integração com MinIO/AWS S3 SDK via adaptador.

## Consequências
- **Positivas:**
  - DynamoDB nunca sofre com estouro do limite de 400 KB por item.
  - Baixo custo de armazenamento em S3 com suporte a políticas de ciclo de vida (lifecycle).
  - Versionamento nativo de `QA.md` e documentos de conhecimento sem complexidade adicional.
- **Negativas:**
  - A leitura completa de uma mensagem com seu payload bruto exige duas consultas (DynamoDB para metadados e S3 para corpo).
