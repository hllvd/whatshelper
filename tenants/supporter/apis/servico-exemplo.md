# API: [Nome do Serviço]

Descrição geral do serviço externo ou interno, qual sistema ele representa e para que o agente/sistema utiliza esta integração.

---

## 1. Visão Geral e Autenticação

- **Serviço:** `[Nome do Serviço — ex.: API Sistema Supporter / Avapro]`
- **Base URL:** `https://api.exemplo.com.br`
- **Swagger UI URL (opcional):** `https://api.exemplo.com.br/swagger/index.html`
- **OpenAPI / Swagger Spec URL (opcional):** `https://api.exemplo.com.br/swagger/doc.json`
- **Formato Padrão:** `Content-Type: application/json`
- **Possui Autenticação?:** `Sim` *(ou `Não`)*
- **Tipo de Autenticação:** `Login e Senha (Bearer Token JWT)` *(ou `Basic Auth`, `API Key`, `Sessão/Cookie`, `Nenhuma`)*

### Credenciais de Acesso
- **Login / Usuário:** `seu_usuario_aqui` *(ou variável `${API_USER}`)*
- **Senha:** `sua_senha_aqui` *(ou variável `${API_PASSWORD}`)*
- **Header de Envio (após autenticado):** `Authorization: Bearer <token>`

### Endpoint de Login / Obtenção de Token *(quando aplicável)*
- **Endpoint:** `POST /auth/login`
- **Descrição:** Realiza a autenticação usando login e senha para obter o token de acesso que será enviado nos demais endpoints autenticados.
- **Body (JSON):**
  ```json
  {
    "login": "seu_usuario_aqui",
    "password": "sua_senha_aqui"
  }
  ```
- **Resposta de Sucesso (`200 OK`):**
  ```json
  {
    "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
    "expires_in": 3600
  }
  ```

---

## 2. Endpoints Disponíveis

### 2.1. [Nome Curto do Endpoint — ex.: Consultar Contrato por Número]
- **Endpoint:** `GET /v1/contracts/{contractNumber}`
- **Descrição:** Consulta os dados cadastrais, status atual (`Ativo`, `Cancelado`, `Atrasado`), matrícula vinculada e data da última sincronização de um contrato específico pelo número.
- **Requer Autenticação?:** `Sim`
- **Parâmetros de Rota (Path Params):**
  - `contractNumber` *(string, obrigatório)*: Número do contrato a ser consultado (ex.: `123456`).
- **Parâmetros de Consulta (Query Params):**
  - *(Nenhum)*
- **Exemplo de Chamada:**
  ```http
  GET https://api.exemplo.com.br/v1/contracts/123456
  Authorization: Bearer <token>
  Accept: application/json
  ```
- **Exemplo de Resposta (`200 OK`):**
  ```json
  {
    "contract_number": "123456",
    "status": "ACTIVE",
    "client_name": "João da Silva",
    "matricula": "98765",
    "total_value": 150000.00,
    "updated_at": "2026-11-15T08:00:00Z"
  }
  ```
- **Possíveis Erros:**
  - `401 Unauthorized`: Credenciais inválidas ou token expirado.
  - `404 Not Found`: Contrato ainda não encontrado na base sincronizada.

---

### 2.2. [Nome Curto do Endpoint — ex.: Listar Solicitações Pendentes]
- **Endpoint:** `GET /v1/requests`
- **Descrição:** Lista as solicitações do usuário ou da equipe filtrando por status (`PENDING`, `APPROVED`, `REJECTED`) ou tipo de solicitação.
- **Requer Autenticação?:** `Sim`
- **Parâmetros de Consulta (Query Params):**
  - `status` *(string, opcional)*: Filtra pelo status da solicitação (ex.: `PENDING`).
  - `user_id` *(string, opcional)*: Filtra pelo ID ou e-mail do consultor.
- **Exemplo de Chamada:**
  ```http
  GET https://api.exemplo.com.br/v1/requests?status=PENDING
  Authorization: Bearer <token>
  Accept: application/json
  ```
- **Exemplo de Resposta (`200 OK`):**
  ```json
  {
    "items": [
      {
        "id": "req_01",
        "type": "CREATE_TEAM_GUIMEL",
        "status": "PENDING",
        "created_at": "2026-11-15T10:30:00Z"
      }
    ]
  }
  ```

---

### 2.3. [Template Vazio para Novo Endpoint]
- **Endpoint:** `[MÉTODO] /caminho/do/endpoint`
- **Descrição:** `[Descreva o que este endpoint faz, quais dados ele retorna ou altera e quando deve ser utilizado]`
- **Requer Autenticação?:** `Sim` / `Não`
- **Parâmetros (Path / Query / Body):**
  - `parametro_1` *(tipo, obrigatório/opcional)*: descrição do parâmetro.
- **Exemplo de Body (se `POST` / `PUT` / `PATCH`):**
  ```json
  {
    "campo": "valor"
  }
  ```
- **Exemplo de Resposta (`200 OK`):**
  ```json
  {
    "status": "ok"
  }
  ```
