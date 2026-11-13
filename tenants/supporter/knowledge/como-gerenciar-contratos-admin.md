# Como Gerenciar Contratos (Visão Administrativa)

A tela **Contratos** (`#/contracts`) é a central administrativa para consultar, filtrar, criar, editar, excluir, importar e exportar contratos da operação.

---

## 1. Pré-requisitos
- Possuir perfil **Admin** (ou permissão `system:admin`).

---

## 2. Passo a Passo

### Passo 1: Acessar e Filtrar Contratos
1. No menu lateral, clique em **Contratos** (rota: `#/contracts`).
2. Utilize os filtros disponíveis para refinar a listagem:
   - **Usuários** e **Times** *(multi-seleção)*
   - **Matrícula** *(multi-seleção)*
   - **Número do Contrato**
   - **Status** *(multi-seleção)*
   - **Data Início** e **Data Fim**
   - **Atribuição** (filtrar atribuídos ou não atribuídos) e **Aguardando Pagamento** (*Todos*, *Sim* ou *Não*)
3. Clique em **`Limpar Filtros`** quando desejar resetar a busca.

---

### Passo 2: Configurar Colunas e Opções de Exibição
1. No topo da página, clique em **`Configurações`**.
2. Marque ou desmarque as colunas que deseja visualizar na tabela (*Número do Contrato*, *Usuário*, *Equipe*, *Matrícula*, *Grupo*, *Segmento*, *Cota*, *Cliente*, *Valor Total*, *Status*, *Data Início*, *Data da Última Atualização*, *Aguardando Pagamento*).
3. Se necessário, ajuste as opções adicionais:
   - **Mostrar contratos como não pago, como 'Aguardando pagamento'**
   - **Incluir usuários desativados no filtro Usuário**

---

### Passo 3: Criar, Editar ou Excluir um Contrato
1. **Criar**: Clique em **`+ Criar`** no canto superior direito, preencha os dados do contrato e salve.
2. **Editar**: Localize o contrato na tabela e clique no ícone de lápis (**Editar**) na coluna **Ações**.
3. **Excluir**: Clique no ícone de lixeira (**Excluir**) na coluna **Ações** e confirme a exclusão no modal.

---

### Passo 4: Importar ou Exportar Contratos
1. **Importar**: Clique em **`Importar`** no topo para abrir o modal de importação em lote.
2. **Exportar**: Aplique os filtros desejados e clique em **`Exportar`**. O sistema processará o arquivo em segundo plano exibindo uma barra de progresso e disponibilizará o download assim que concluir.

---

### Passo 5: Analisar o Resumo Financeiro
Logo abaixo da tabela, o painel de **Resumo** consolida os totais dos contratos filtrados (Total Geral, Total Ativo, Total Atrasado, Total Cancelado e índices de Retenção).

---

## 3. Dúvidas Comuns / Troubleshooting
- **Não encontro um usuário inativo no filtro**: Abra **`Configurações`** e marque a opção *Incluir usuários desativados no filtro Usuário*.
- **Preciso apenas acompanhar as minhas próprias vendas**: Acesse **Meus Contratos** (`#/my-contracts`).
