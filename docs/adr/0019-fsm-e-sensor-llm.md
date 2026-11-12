# ADR 0019: FSM Declarativa no Pack, LLM como Sensor Determinístico e Cache Hot/Cold

- **Status**: Aceito
- **Data**: 2026-11-12
- **Contexto**: Refinamento arquitetural da orquestração de CRM e pipeline de atendimento conversacional.

## 1. Contexto e Motivação

Modelos de linguagem (LLMs) são probabilísticos. Delegar ao LLM o fluxo conversacional, as regras de negócio ou a decisão de pular etapas de um funil causa alucinações, loops de mensagens e perda de garantias de conformidade.
Para garantir que o agente nunca pule etapas críticas nem tome rumos arbitrários, adotamos uma arquitetura onde:
1. O **LLM opera estritamente como sensor determinístico de extração de dados** (stateless, saída estruturada em JSON).
2. Toda a **inteligência de controle, transições de estado e regras de negócio reside no backend em uma Máquina de Estados Finitos (FSM)**.
3. O núcleo em Go (`internal/`) permanece **100% agnóstico de mercado**, sendo os estados, transições, guardas e templates definidos declarativamente no `pack.yaml`.
4. Um cache local rápido (**Hot Session Cache** em SQLite) reduz a latência e desacopla a leitura imediata do estado durante interações contínuas, mantendo persistência durável no DynamoDB.

## 2. Taxonomia de Extração (5 Dimensões)

O sensor LLM (Stage 1) recebe uma janela deslizante das últimas mensagens e extrai informações delimitadas nas seguintes dimensões:
1. **Eventos Determinísticos (Sistema)**: Mensagens recebidas, timeouts, sessões.
2. **Intenções (Ação do Usuário)**: O que o usuário deseja (e.g. `inquiry_pricing`, `schedule_meeting`, `request_human_handoff`).
3. **Entidades/Fatos (Extração Tipada)**: Informações tipadas para enriquecer o contexto (e.g. `budget`, `location`, `urgency_level`).
4. **Sentimento e Urgência**: `urgency_level` (`low`, `medium`, `high`, `critical`) e `sentiment` (`positive`, `neutral`, `negative`, `frustrated`).
5. **Estado Sugerido**: Sugestão de estágio percebido pelo sensor, que a FSM avalia contra as guardas de transição.

O output do LLM é encapsulado no padrão estrito de envelope:
```json
{
  "success": true,
  "data": {
    "message_analysis": {
      "primary_intent": "schedule_meeting",
      "urgency_level": "medium",
      "sentiment": "positive"
    },
    "extracted_entities": { ... },
    "suggested_state": "qualifying"
  },
  "error": null
}
```

## 3. Máquina de Estados Finitos (FSM) Declarativa

No `pack.yaml`, cada mercado declara sua FSM:
```yaml
fsm:
  initial_state: cold_prospecting
  states: [cold_prospecting, qualifying, scheduling, human_handoff]
  transitions:
    - from: cold_prospecting
      to: qualifying
      when:
        intent: inquiry_pricing
      action: ask_for_budget
    - from: qualifying
      to: scheduling
      when:
        intent: schedule_meeting
        required_facts: [Budget]
      action: send_calendar
  actions:
    ask_for_budget:
      type: message_template
      text: "Para podermos encontrar as melhores opções, qual é a sua faixa de orçamento?"
    send_calendar:
      type: message_template
      text: "Perfeito! Aqui está o link para agendar nosso encontro: https://calendly.com/demo"
```

A função de transição `CalculateNextState(currentState, analysis, fsm)` é **pura e determinística**:
- Avalia o estado corrente e as regras do `pack.yaml`.
- Valida condições de guarda (`When.Intent`, `When.RequiredFacts`, `When.Urgency`).
- Se os requisitos forem atendidos, transiciona para `NextState` e retorna `ActionToTrigger`.
- Se as guardas não forem satisfeitas (ex.: intenção de agendamento sem orçamento informado), transiciona para o estado/ação corretiva (ex.: `enforce_budget`) ou permanece no estado atual.

## 4. Camada Híbrida de Ações

As ações engatilhadas pela FSM são de tipos suportados:
1. `message_template`: Envio direto de mensagem parametrizada definida no pack (sem invocar LLM para redação livre).
2. `knowledge_reply`: Aciona o Stage 2 com busca em `knowledge/*.md` / `QA.md` e validação estrita via `CheckGrounding`.
3. `escalate` / `handoff`: Criação de tarefa administrativa e notificação de atendimento humano.
4. `opt_out`: Desativação de notificações.

## 5. Armazenamento Hot / Cold (SQLite e DynamoDB)

- **Hot Session Cache (SQLite)**: Mantém o estado ativo da conversa (`current_state`, metadados de sessão recente e contadores) em banco SQLite local (`whatsapp_data/session_cache.db` ou em memória nos testes), permitindo leitura sub-milissegundo na hidratação.
- **Cold / Durável (DynamoDB)**: Permanece como fonte primária de verdade persistente. Toda transição de estado da FSM é persistida com concorrência otimista (`ExpectedVersion`) em `META.journey_stage` e `SourceState`.

## 6. Consequências

- **Positivas**:
  - Impossibilidade de o LLM pular etapas do processo comercial ou de atendimento.
  - Core 100% agnóstico e configurável por verticais de mercado via YAML.
  - Testabilidade determinística completa do fluxo de negócios sem mocks complexos de LLM.
  - Desempenho otimizado por templates pré-definidos e cache rápido de sessão.
- **Negativas / Limitações**:
  - Transições não previstas no YAML exigem fallback configurado para conhecimento ou atendimento humano.
