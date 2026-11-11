# ADR 0009: Validador de Saída JSON Guiado pelo Vertical Pack

- **Status:** Aceito
- **Data:** 2026-11-11
- **Decisores:** Engenharia Whatshelper

## Contexto
O estágio de análise do cérebro solicita ao LLM um JSON estruturado cujos campos (`facts`, `signals`, `context`, `last_sender`) variam integralmente de acordo com o vertical pack ativo do tenant. Precisamos:
1. Gerar o JSON Schema para incluir no prompt e enviar ao provedor de LLM.
2. Validar a resposta crua do LLM contra as definições do `pack.yaml`, produzindo erros precisos e acionáveis para o estágio de *repair* (LLM-02).
3. Aplicar semântica específica de extração conversacional: distinguir fato omitido (mantém valor anterior) de fato explicitamente nulo (`null`, que remove o fato), normalizar tipos numéricos/inteiros/enum para representação uniforme e preencher sinais ausentes como `false`.

## Decisão
Implementar um gerador de JSON Schema e um validador próprios em Go puro (`internal/pack/schema.go` e `internal/pack/validate.go`), dirigidos diretamente pelas structs do `pack.yaml`, sem depender de bibliotecas genéricas de JSON Schema Draft-07/2020-12 em tempo de execução.

## Consequências
- **Positivas:**
  - Zero dependências externas adicionais para validação.
  - Mensagens de erro de validação legíveis e determinísticas para alimentar o prompt de *repair*.
  - Controle exato sobre normalização de tipos (ex.: rejeitar `"500 mil"` para campo `number`, aceitar número ou string numérica pura, diferenciar chave omitida de `null`).
- **Negativas:**
  - Caso um pack futuro exija palavras-chave complexas de JSON Schema (ex.: `oneOf`, `patternProperties`, regex avançadas), será necessário estender explicitamente o validador do pacote `internal/pack`.
