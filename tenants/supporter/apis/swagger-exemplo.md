# Swagger / OpenAPI: [Nome do Serviço]

Este formato permite documentar uma API usando diretamente a especificação **Swagger 2.0 / OpenAPI 3.0** (em YAML ou JSON) dentro de um arquivo `.md`, acompanhada das credenciais de acesso e descrição de cada endpoint.

---

## 1. Acesso e Autenticação

- **Serviço:** `[Nome do Serviço]`
- **Base URL:** `https://api.exemplo.com.br`
- **Swagger UI URL:** `https://api.exemplo.com.br/swagger/index.html`
- **Swagger / OpenAPI Spec URL:** `https://api.exemplo.com.br/swagger/doc.json`
- **Possui Autenticação?:** `Sim` *(ou `Não`)*
- **Tipo de Autenticação:** `BearerAuth (Login e Senha -> JWT)` *(ou `BasicAuth`, `ApiKeyAuth`, `Nenhuma`)*
- **Login / Usuário:** `seu_usuario_aqui` *(ou `${API_USER}`)*
- **Senha:** `sua_senha_aqui` *(ou `${API_PASSWORD}`)*
- **Endpoint de Login:** `POST /auth/login`

---

## 2. Resumo Rápido dos Endpoints

| Método | Endpoint | Autenticado? | Descrição |
| :--- | :--- | :--- | :--- |
| `POST` | `/auth/login` | Não | Autentica com login e senha e retorna o token JWT (`Bearer`). |
| `GET` | `/v1/contracts/{contractNumber}` | Sim | Consulta detalhes, status (`ACTIVE`, `CANCELED`) e matrícula de um contrato pelo número. |
| `GET` | `/v1/requests` | Sim | Lista solicitações filtrando por status ou consultor. |

---

## 3. Especificação Swagger / OpenAPI (YAML ou JSON)

> Você pode colar abaixo o conteúdo do seu `swagger.yaml` / `openapi.yaml` (ou `swagger.json`) exportado do serviço:

```yaml
openapi: 3.0.3
info:
  title: API Sistema Exemplo
  version: 1.0.0
  description: Especificação OpenAPI/Swagger dos endpoints do serviço.
servers:
  - url: https://api.exemplo.com.br
    description: Servidor de Produção
components:
  securitySchemes:
    BearerAuth:
      type: http
      scheme: bearer
      bearerFormat: JWT
paths:
  /auth/login:
    post:
      summary: Autenticar usuário (Login e Senha)
      description: Recebe login e senha e retorna o token Bearer para uso nos endpoints protegidos.
      security: []
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [login, password]
              properties:
                login:
                  type: string
                  example: "seu_usuario_aqui"
                password:
                  type: string
                  example: "sua_senha_aqui"
      responses:
        "200":
          description: Login realizado com sucesso
          content:
            application/json:
              schema:
                type: object
                properties:
                  token:
                    type: string
                  expires_in:
                    type: integer

  /v1/contracts/{contractNumber}:
    get:
      summary: Consultar contrato por número
      description: Retorna dados cadastrais, status atual, matrícula e data de atualização do contrato.
      security:
        - BearerAuth: []
      parameters:
        - name: contractNumber
          in: path
          required: true
          description: Número do contrato
          schema:
            type: string
      responses:
        "200":
          description: Dados do contrato encontrados
          content:
            application/json:
              schema:
                type: object
                properties:
                  contract_number:
                    type: string
                  status:
                    type: string
                    enum: [ACTIVE, LATE, CANCELED]
                  client_name:
                    type: string
                  matricula:
                    type: string
                  total_value:
                    type: number
                  updated_at:
                    type: string
                    format: date-time
        "404":
          description: Contrato não encontrado
```
