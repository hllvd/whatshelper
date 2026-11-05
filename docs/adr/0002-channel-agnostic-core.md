# ADR 0002: Núcleo Agnóstico de Canal de Mensageria

- **Status:** Aceito
- **Data:** 2026-11-05
- **Decisores:** Engenharia Whatshelper

## Contexto
O cérebro precisa atender clientes vindos de múltiplos canais de mensageria: inicialmente um simulador web para desenvolvimento e testes, seguido de integração não-oficial com WhatsApp via biblioteca `whatsmeow`, e potencialmente canais de e-mail e API oficial da Meta (Cloud API) no futuro. O pipeline de processamento cognitivo (análise, decisão, geração de resposta e aprendizado) não deve conhecer os detalhes de transporte ou protocolos de rede de nenhum canal específico.

## Decisão
1. **Interface `Channel`**: O canal é abstraído por uma interface Go definida no domínio (`internal/core`):
   ```go
   type Channel interface {
       ID() string
       Start(ctx context.Context) error
       Stop(ctx context.Context) error
       Messages() <-chan NormalizedMessage
       Send(ctx context.Context, msg NormalizedMessage) error
       SendTyping(ctx context.Context, sourceKey string, typing bool) error
   }
   ```
2. **Modelo de Mensagem Normalizado (`NormalizedMessage`)**:
   - Todo canal traduz mensagens de entrada em um envelope padronizado contendo `TenantID`, `Channel`, `SourceKey`, `MessageID`, `Timestamp`, `Direction`, `Type`, `Text`, `MediaRef`, `RawRef`.
   - Na saída, o cérebro entrega uma `NormalizedMessage` e o canal é responsável por formatar e despachar para o meio de entrega de acordo com as capacidades declaradas (ex.: texto puro sem markdown para WhatsApp).

## Consequências
- **Positivas:**
  - O pipeline do cérebro é idêntico se a mensagem veio do WhatsApp, do simulador web ou de um webhook de email.
  - Novos canais podem ser plugados sem alterar nenhuma linha de código do cérebro.
- **Negativas:**
  - Recursos específicos de um canal (ex.: botões interativos do WhatsApp ou cabeçalhos de e-mail) devem ser traduzidos ou mapeados em atributos genéricos quando aplicável.
