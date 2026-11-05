# ADR 0001: Adoção de Go e Arquitetura Hexagonal (Ports & Adapters)

- **Status:** Aceito
- **Data:** 2026-11-05
- **Decisores:** Engenharia Whatshelper

## Contexto
O sistema é um "brain" (cérebro) de atendimento por mensageria projetado para operar com alta concorrência, baixo consumo de memória, previsibilidade de latência e facilidade de empacotamento em contêineres Docker. O sistema precisa rodar 100% em ambiente local para testes e desenvolvimento rápido (docker-compose com DynamoDB Local e MinIO) e suportar deploy em produção (AWS Fargate) sem reescrever o código central.

## Decisão
1. **Linguagem Go**: Utilizaremos Go moderno pela sua compilação estática (`CGO_ENABLED=0`), inicialização instantânea, suporte de primeira classe a concorrência (`goroutines` e `channels`) e tipagem estática rigorosa.
2. **Arquitetura Hexagonal (Ports & Adapters)**:
   - O pacote `internal/core` conterá as entidades de domínio puras, regras de negócio e interfaces abstratas (`ports.go`). Não terá qualquer dependência de bibliotecas externas de I/O, bancos de dados, AWS ou provedores de LLM.
   - Os adaptadores (`internal/store`, `internal/channels`, `internal/llm`, `internal/server`) implementarão essas interfaces.
   - Todas as portas terão implementações in-memory / fakes para execução de testes unitários rápidos e determinísticos sem I/O real.

## Consequências
- **Positivas:**
  - Código limpo, testável em frações de segundo sem Docker ou rede.
  - Facilidade de substituir adaptadores (ex: simulador web vs WhatsApp whatsmeow vs Meta Cloud API).
  - Executável estático único e imagem Docker leve (~20MB).
- **Negativas:**
  - Exige definição explícita de DTOs e mapeamento entre modelos de transporte/banco e entidades de domínio.
