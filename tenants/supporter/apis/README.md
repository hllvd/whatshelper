# Especificações Técnicas de APIs (`apis/`)

Esta pasta armazena documentos `.md` e `.yml` / `.yaml` com aspectos técnicos, credenciais e catálogos de endpoints de APIs internas ou de terceiros relacionadas ao tenant **`supporter`**.

> **Por que esta pasta é separada de `knowledge/`?**
> A pasta `knowledge/` é injetada automaticamente nos prompts de resposta aos usuários finais (WhatsApp). Manter as especificações de APIs em `apis/` garante que URLs internas, logins, senhas e payloads técnicos fiquem organizados junto ao tenant sem risco de exposição nas conversas com usuários.

---

## Formatos Suportados (Sem Campos Obrigatórios)

Não há campos obrigatórios — você pode preencher apenas as informações que tiver disponíveis para cada serviço usando qualquer um dos formatos abaixo:

1. **Arquivo YAML direto (`.yml` / `.yaml`)**: veja [`swagger-exemplo.yml`](./swagger-exemplo.yml) — permite salvar diretamente o arquivo Swagger / OpenAPI (`.yml`) ou uma definição YAML simples do serviço.
2. **Formato Swagger / OpenAPI em Markdown (`.md`)**: veja [`swagger-exemplo.md`](./swagger-exemplo.md) — permite colar o Swagger/OpenAPI dentro de um `.md` com notas adicionais.
3. **Formato Descritivo livre (`.md`)**: veja [`servico-exemplo.md`](./servico-exemplo.md) — documentação em texto/Markdown para descrever endpoints, login/senha e exemplos.

---

## Como Documentar um Novo Serviço

1. Adicione um arquivo `<nome-do-servico>.yml` (ou `<nome-do-servico>.md`) nesta pasta.
2. Inclua apenas os campos relevantes para aquele serviço (por exemplo: URL base, se tem autenticação, login e senha, endpoints e a descrição do que cada endpoint faz).
