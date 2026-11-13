# Como Gerenciar Níveis de Classificação

A tela **Níveis de Classificação** (`#/classifications`) permite que administradores consultem os níveis do plano de carreira configurados na empresa, acompanhem metas e requisitos de cada nível e gerenciem a atribuição de seus subordinados em cada classificação.

---

## 1. Pré-requisitos
- Possuir perfil **Admin** (ou permissão `teams:manage` / `system:admin`).
- **Escopo de atuação para Admin**: Um administrador visualiza e gerencia os membros que pertencem à sua própria árvore hierárquica de subordinados. (A criação ou alteração das regras dos níveis em si é feita apenas pelo SuperAdmin.)

---

## 2. Passo a Passo

### Passo 1: Consultar os Níveis e Seus Critérios
1. No menu lateral, clique em **Níveis de Classificação** (rota: `#/classifications`).
2. Cada card exibe um nível do plano de carreira com suas informações:
   - **Ativos**: quantidade de membros atualmente nesse nível.
   - **Prêmio / Benefício** *(quando configurado)*.
   - **Meta de Vendas (R$)** e **Retenção (%)**.
   - **Próximo Nível** na progressão.
   - **Mínimo Direto (`Dir. 1` / `Dir. 2`)**: quantidade mínima de pessoas diretas em determinados níveis.

---

### Passo 2: Atribuir um Subordinado a um Nível
1. No card do nível desejado, clique no ícone de **Gerenciar membros** (ícone de grupo de usuários).
2. Na coluna da esquerda (**Atribuir Novos Membros**):
   - Preencha a **Data de Início** *(obrigatória)*.
   - Se aplicável, informe a **Data de Fim** *(opcional — deixe em branco para vigência contínua)*.
   - Use o campo **Buscar usuário...** para localizar o consultor na sua hierarquia.
3. Clique sobre o card do usuário (ou no ícone de adicionar à direita) para atribuí-lo ao nível.

---

### Passo 3: Consultar Membros Ativos, Inativos e Histórico
1. Na coluna da direita (**Membros Ativos**), você pode buscar membros vigentes e visualizar desde quando estão no nível.
2. Clique no ícone de **Ver histórico** (relógio) ao lado de qualquer membro para consultar toda a linha do tempo de classificações daquele usuário.
3. Para encerrar/remover o vínculo atual do membro com o nível, clique no ícone **`X` (Remover do nível)**.
4. Expanda a seção **Membros Inativos** logo abaixo para consultar usuários que já estiveram naquele nível em períodos anteriores.

---

## 3. Dúvidas Comuns / Troubleshooting
- **Um consultor quer solicitar sua própria promoção de nível**: Ele pode abrir um pedido em **Solicitações** (`#/requests`) selecionando **Solicitação de Nível de Classificação**, informando o nível desejado e a data de início para aprovação do gestor.
- **Não encontro um usuário na lista para atribuir**: Verifique se ele está ativo e vinculado abaixo de você na árvore em **Usuários → Árvore** (`#/users/tree`).
