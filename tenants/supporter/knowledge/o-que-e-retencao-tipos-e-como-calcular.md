# Guia Completo: O que é Retenção, Tipos e Como Calcular

## 1. O que é Retenção?

**Retenção** é a capacidade de uma empresa, serviço ou produto de manter seus clientes, usuários ou receitas ativos ao longo de determinado período. 

No contexto de negócios, ela representa a saúde da base:
- É o oposto direto do **Churn** (taxa de cancelamento/perda).
- Manter clientes ativos custa consideravelmente menos do que adquirir novos (CAC).
- Aumenta a previsibilidade financeira e o valor do tempo de vida do cliente (LTV).

---

## 2. Principais Tipos de Retenção

### A. Retenção de Clientes / Contas (*Customer Retention* ou *Logo Retention*)
- **Foco:** Quantidade de clientes/contas.
- **Conceito:** Avalia a proporção de clientes que continuam ativos ao final do período, desconsiderando novas aquisições.

### B. Retenção de Receita Bruta (*Gross Revenue Retention* - GRR)
- **Foco:** Receita existente (MRR/ARR) sem expansão.
- **Conceito:** Percentual da receita recorrente que você preservou da base existente, descontando cancelamentos (*churn*) e reduções de plano (*downgrades*). Não inclui *upsell*. O teto máximo é sempre 100%.

### C. Retenção de Receita Líquida (*Net Revenue Retention* - NRR)
- **Foco:** Receita total gerada pela mesma base de clientes ao longo do tempo.
- **Conceito:** Considera perdas (churn e downgrades) e ganhos adicionais (*upsell* e *cross-sell*). Quando o NRR é maior que 100%, o negócio cresce mesmo sem novas vendas.

### D. Retenção de Engajamento / Produto (*Product / User Retention*)
- **Foco:** Atividade e uso do produto/app (análise de cohorts/safras).
- **Tipos comuns:**
  - **D1 / D7 / D30:** Usuários que retornam exatamente no dia 1, 7 ou 30 após o cadastro/instalação.
  - **Rolling / Unbounded Retention:** Usuários que retornam no dia X ou em qualquer dia posterior.
  - **Bracket Retention:** Usuários que realizaram ações dentro de uma janela de tempo específica (ex.: semana 1, mês 1).

---

## 3. Fórmulas e Como Calcular

### 1. Taxa de Retenção de Clientes (CRR - Customer Retention Rate)

Calcula a porcentagem de clientes retidos em um determinado período:

$$\text{CRR} = \left( \frac{E - N}{S} \right) \times 100$$

- **S (Start):** Número de clientes no início do período
- **E (End):** Número total de clientes no final do período
- **N (New):** Novos clientes adquiridos durante o período

#### Exemplo:
- Início do mês ($S$): 100 clientes
- Fim do mês ($E$): 110 clientes
- Novos clientes no período ($N$): 25 clientes

$$\text{CRR} = \left( \frac{110 - 25}{100} \right) \times 100 = \frac{85}{100} \times 100 = 85\%$$

---

### 2. Retenção de Receita Bruta (GRR - Gross Revenue Retention)

Mede quanto da sua receita inicial foi mantida (máximo 100%):

$$\text{GRR} = \left( \frac{\text{MRR Inicial} - \text{Churn} - \text{Contração}}{\text{MRR Inicial}} \right) \times 100$$

#### Exemplo:
- MRR Inicial: R$ 50.000
- Churn (Cancelamentos): R$ 3.000
- Contração (Downgrades): R$ 1.000

$$\text{GRR} = \left( \frac{50.000 - 3.000 - 1.000}{50.000} \right) \times 100 = \frac{46.000}{50.000} \times 100 = 92\%$$

---

### 3. Retenção de Receita Líquida (NRR - Net Revenue Retention)

Mede o crescimento líquido da base existente incluindo expansões:

$$\text{NRR} = \left( \frac{\text{MRR Inicial} + \text{Expansão} - \text{Churn} - \text{Contração}}{\text{MRR Inicial}} \right) \times 100$$

#### Exemplo:
- MRR Inicial: R$ 50.000
- Expansão (*Upsell* / *Cross-sell*): R$ 8.000
- Churn: R$ 3.000
- Contração: R$ 1.000

$$\text{NRR} = \left( \frac{50.000 + 8.000 - 3.000 - 1.000}{50.000} \right) \times 100 = \frac{54.000}{50.000} \times 100 = 108\%$$

---

### 4. Retenção por Safra / Cohort (Uso de Produto)

Para avaliar engajamento em aplicativos ou plataformas:

$$\text{Retenção do Dia } N = \left( \frac{\text{Usuários ativos no Dia } N}{\text{Total de usuários que entraram no Dia } 0} \right) \times 100$$

---

## 4. Tabela Comparativa de Benchmarks (SaaS B2B)

| Métrica | O que analisa | Meta Saudável de Mercado |
| :--- | :--- | :--- |
| **CRR** | Volume de contas físicas preservadas | > 85% a 90% ao ano |
| **GRR** | Receita retida sem contar upsell | > 85% a 92% ao ano |
| **NRR** | Expansão líquida da base de clientes | > 105% a 120%+ ao ano |
