# ADR 0003: Núcleo Agnóstico de Mercado via Vertical Packs

- **Status:** Aceito
- **Data:** 2026-11-05
- **Decisores:** Engenharia Whatshelper

## Contexto
O primeiro caso de uso do sistema é o mercado imobiliário (corretores conversando com compradores/inquilinos de imóveis), originado de prompts existentes (`analysis_system.md`, `broker_system.md`, `nudge_system.md`). No entanto, o sistema deve ser utilizável em qualquer mercado (ex.: clínicas médicas, escritórios de advocacia, e-commerce, suporte B2B). Se termos como "imóvel", "corretor", "quartos", "visita" forem embutidos no código-fonte do core, o sistema perderá sua generalidade.

## Decisão
1. **Conceito de Vertical Pack**: Um pack é uma pasta autossuficiente contendo:
   - `pack.yaml`: metadados (nome, idioma padrão), catálogo de intenções (`intents`), schema de fatos extraíveis (`facts`), sinais comportamentais (`signals`) e campos de contexto (`context`).
   - `prompts/`: templates de prompt em texto (`analysis.tmpl`, `reply.tmpl`, `nudge.tmpl`).
2. **Schema Dinâmico**: O schema JSON exigido do LLM no estágio de análise é montado dinamicamente em tempo de execução a partir da configuração do pack ativo para o tenant.
3. **Isolamento de Domínio**: Todo termo, fato e regra específica de imobiliário residirá exclusivamente dentro de `packs/realestate/`. O core manipulará apenas entidades genéricas: fatos como pares chave-valor tipados, intenções como identificadores com ações associadas, e sinais como flags booleanas avaliadas.

## Consequências
- **Positivas:**
  - Para criar um novo assistente (ex.: clínica veterinária ou consultório), basta criar uma nova pasta em `packs/` com `pack.yaml` e templates.
  - O core permanece enxuto, genérico e imune a mudanças de vocabulário de negócio.
- **Negativas:**
  - A validação de saída do LLM deve ser genérica e baseada no schema do pack carregado, em vez de structs estáticas fixas no código Go.
