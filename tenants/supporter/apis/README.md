# Especificações Técnicas de APIs (`apis/`)

Esta pasta armazena os documentos `.md` com os aspectos técnicos, credenciais e catálogos de endpoints de APIs internas ou de terceiros relacionadas ao tenant **`supporter`**.

> **Por que esta pasta é separada de `knowledge/`?**
> A pasta `knowledge/` é injetada automaticamente nos prompts de resposta aos usuários finais (WhatsApp). Manter as especificações de APIs em `apis/` garante que URLs internas, logins, senhas e payloads técnicos fiquem organizados junto ao tenant sem risco de exposição nas conversas com usuários.

---

## Formatos Suportados

Você pode documentar um serviço em `tenants/supporter/apis/` usando qualquer um dos dois formatos abaixo (ou combinando ambos no mesmo `.md`):

1. **Formato Descritivo (Markdown)**: veja [`servico-exemplo.md`](./servico-exemplo.md).
2. **Formato Swagger / OpenAPI em Markdown (`.md`)**: veja [`swagger-exemplo.md`](./swagger-exemplo.md) — permite colar ou estruturar diretamente a especificação **Swagger 2.0 / OpenAPI 3.0** (YAML ou JSON) dentro do `.md`, junto com as credenciais de acesso (**Login**, **Senha**, se possui autenticação) e a URL do Swagger original.

---

## Como Documentar um Novo Serviço

1. Crie um arquivo `.md` com o nome do serviço (ex.: `sistema-supporter.md`, `avapro.md`, `crm-swagger.md`) copiando [`servico-exemplo.md`](./servico-exemplo.md) ou [`swagger-exemplo.md`](./swagger-exemplo.md).
2. Preencha obrigatoriamente a seção **1. Visão Geral e Autenticação**:
   - Informe se o serviço **possui ou não autenticação**.
   - Caso possua, especifique o **Login**, a **Senha** (ou variável de ambiente correspondente no `.env`) e como o token/sessão é obtido e enviado.
   - Se o serviço tiver um link de documentação Swagger/OpenAPI (ex.: `/swagger/index.html` ou `/openapi.json`), informe nos campos **Swagger UI URL** / **OpenAPI Spec URL**.
3. Na seção de endpoints, descreva cada rota em Markdown ou incorpore o bloco `yaml`/`json` do **Swagger / OpenAPI** contendo `paths`, `summary`, `description`, `parameters`, `requestBody` e `responses`.
