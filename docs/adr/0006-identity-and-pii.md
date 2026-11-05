# ADR 0006: Identidade, Derivação de Source Key e Proteção de PII

- **Status:** Aceito
- **Data:** 2026-11-05
- **Decisores:** Engenharia Whatshelper

## Contexto
O sistema recebe mensagens de múltiplos canais com diferentes formatos de identificadores de contato (números de telefone celular, endereços de email, IDs de websocket). Conforme exigido pela LGPD/GDPR e pelas boas práticas de segurança, dados de identificação pessoal (PII - Personally Identifiable Information) não devem circular em texto plano em chaves públicas ou logs desnecessários, e dados de contato brutos devem ser protegidos em repouso.

## Decisão
1. **Derivação de `source_key` por Canal**:
   - **WhatsApp**: Número de telefone estritamente normalizado no padrão internacional E.164 sem caracteres especiais (ex: `+55 11 99999-8888` torna-se `5511999998888`).
   - **E-mail**: Hash criptográfico SHA-256 do endereço normalizado (trim de espaços em branco, conversão integral para minúsculas). Exemplo: `sha256("cliente@exemplo.com.br")`. O endereço de e-mail em texto puro **nunca** é usado como `source_key` nem como chave de partição.
   - **Simulador Web**: Identificador de sessão de teste alfanumérico (ex: `sim-user-uuid`).
2. **Armazenamento de Dados de Contato Brutos**:
   - O dado bruto de contato (ex.: número original, nome de exibição, endereço de e-mail em claro) fica armazenado **apenas** no item `META` da partição da fonte no DynamoDB, no campo `encrypted_contact_info`.
   - Em produção, esse campo é criptografado com chave gerenciada por KMS ou chave de criptografia de aplicação configurada via variável de ambiente. Em desenvolvimento local, usa-se cifra AES-GCM com chave de teste.
3. **Estrutura de Identidade Unificada (`contact_id`)**:
   - O item `META` contém o atributo `contact_id` (inicialmente nulo/vazio).
   - O índice secundário global `GSI1` é reservado para futuras buscas por contato:
     - `GSI1PK = T#<tenant>#CONTACT#<contact_id>`
     - `GSI1SK = SRC#<canal>#<source_key>`
   - O item de link dedicado utiliza `PK = T#<tenant>#CONTACT#<contact_id>` e `SK = SRC#<canal>#<source_key>`.
   - **Nenhuma lógica de fusão (merge) de conversas é implementada neste estágio**, mantendo as partições das fontes completamente isoladas.
4. **Política de Retenção e Logs**:
   - Logs estruturados (`slog`) nunca devem imprimir dados brutos de contato ou payloads completos em nível INFO ou ERROR; apenas `tenant`, `channel`, `source_key` e `message_id`.
   - Traces de LLM no S3 possuem expiração padrão configurável de 90 dias via TTL/Lifecycle.
   - A exclusão total de dados de um cliente (direito ao esquecimento) é viabilizada removendo a partição no DynamoDB e prefixo correspondente no S3.

## Consequências
- **Positivas:**
  - Conformidade imediata com princípios de minimização e proteção de PII.
  - O email do cliente não vaza em índices abertos ou chaves de partição.
  - A estrutura para agregação multicanal futura já nasce pronta sem retrabalho no modelo.
- **Negativas:**
  - Para obter o email original em claro para envio, o conector precisa descriptografar `encrypted_contact_info`.
