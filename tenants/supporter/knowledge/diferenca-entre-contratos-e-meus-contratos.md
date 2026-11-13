# Diferença entre "Contratos" e "Meus Contratos"

Embora ambas as telas exibam informações de vendas e indicadores financeiros, **Contratos** (`#/contracts`) e **Meus Contratos** (`#/my-contracts`) têm públicos, permissões e objetivos diferentes.

---

## 1. Comparativo Rápido

| Característica | Contratos (`#/contracts`) | Meus Contratos (`#/my-contracts`) |
| :--- | :--- | :--- |
| **Quem tem acesso** | Apenas **Admin** (com permissão administrativa) | **Todos os usuários** (Consultores e Admins) |
| **Quais contratos exibe** | Todos os contratos da operação / equipe (conforme filtros) | Apenas os contratos atribuídos ao **seu usuário** |
| **Criar contrato manualmente** | Sim (`+ Criar`) | Não (apenas atribuição/solicitação via `+ Novo`) |
| **Editar e excluir contratos** | Sim (ícones na coluna *Ações*) | Não (somente leitura) |
| **Importação em lote** | Sim (`Importar`) | Não |
| **Configurar colunas da tabela** | Sim (`Configurações`) | Não (colunas fixas) |
| **Solicitar/reivindicar contrato** | Não | Sim (`+ Novo` + fila *Contratos Solicitados*) |
| **Gráfico de Produção Histórica** | Não | Sim (abaixo do resumo) |
| **Exportação para Excel** | Sim (da base filtrada) | Sim (da própria carteira filtrada) |

---

## 2. Quando Usar Cada Tela?

### Use **Contratos** (`#/contracts`) quando você for Admin e precisar:
- Auditar, filtrar ou exportar contratos de **vários consultores, equipes ou matrículas** ao mesmo tempo.
- Identificar contratos **não atribuídos** ou **aguardando pagamento**.
- **Criar, editar ou excluir** registros de contratos diretamente no sistema.
- Realizar **importações em lote** ou personalizar quais colunas aparecem na tabela.

### Use **Meus Contratos** (`#/my-contracts`) quando você for Consultor (ou Admin acompanhando suas próprias vendas) e precisar:
- Consultar o status, valores e retenção da **sua carteira pessoal**.
- Acompanhar sua **curva de produção histórica**.
- **Reivindicar/atribuir** um contrato fechado por você usando o botão **`+ Novo`**.
- Acompanhar contratos que você já solicitou e estão **aguardando importação**.

> **Dica para Admins**: Mesmo tendo acesso à tela **Contratos**, um administrador que também realiza vendas pessoais deve usar **Meus Contratos** para reivindicar suas vendas via **`+ Novo`** e acompanhar seu gráfico individual de produção.
