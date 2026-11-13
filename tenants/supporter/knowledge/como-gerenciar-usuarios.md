# Como Gerenciar Consultores (Usuários → Lista)

A tela **Usuários → Lista** (`#/users`) é o local oficial onde administradores **criam novos consultores, editam dados cadastrais e desativam (ou reativam) contas de consultores** dentro da sua hierarquia.

---

## 1. Pré-requisitos
- Possuir perfil **Admin** (ou permissão `users:read`).
- **Escopo de edição para Admin**: Um administrador pode visualizar os consultores listados, porém só consegue **criar, editar, desativar ou reativar** consultores que pertencem à sua própria árvore de subordinados. Para consultores fora da sua hierarquia, a coluna **Ações** exibirá *Apenas leitura*.

---

## 2. Passo a Passo

### Passo 1: Acessar e Filtrar a Lista de Consultores
1. No menu lateral, clique em **Usuários** e depois em **Lista** (rota: `#/users`).
2. Use a barra de busca (**Buscar por nome ou email...**) para localizar rapidamente um consultor específico.
3. Use o seletor de status ao lado da busca para alternar entre:
   - **Ativos** (padrão)
   - **Inativos**
   - **Todos**
4. Caso existam mais de 10 consultores no resultado, utilize os botões **`← Anterior`** e **`Próxima →`** no rodapé da tabela para navegar entre as páginas.

> **Dica**: Clique diretamente sobre o **Nome** de qualquer consultor na tabela para abrir o modal com o **Perfil completo** (dados cadastrais, matrículas e histórico).

---

### Passo 2: Criar um Novo Consultor
1. No canto superior direito da página, clique no botão **`+ Criar`**.
2. Preencha os campos do formulário:
   - **Nome** *(obrigatório)*
   - **Email** *(obrigatório)*
   - **Senha** *(obrigatório na criação)*
   - **Função** *(obrigatório — ex.: Usuário/Consultor ou Admin, conforme seu nível de permissão)*
   - **Usuário Pai**: Selecione o gestor/superior direto do consultor na hierarquia (para Admin, a lista exibe você mesmo e seus subordinados).
   - **Matrícula** / **Usar matrícula do gestor**: Informe a matrícula do consultor ou marque a opção para utilizar a matrícula do gestor selecionado.
   - **Participar da equipe**: Quando aplicável, marque para incluir o consultor automaticamente na equipe do gestor selecionado.
3. Salve o formulário para concluir o cadastro do novo consultor.

---

### Passo 3: Editar um Consultor Existente
1. Localize o consultor desejado na tabela.
2. Na coluna **Ações**, clique no ícone de lápis (**Editar**).
3. Atualize as informações necessárias (nome, e-mail, função, gestor/usuário pai, matrícula ou status **Usuário Ativo**) e salve.

---

### Passo 4: Desativar ou Reativar um Consultor
1. **Para desativar**: Na coluna **Ações** de um consultor ativo, clique no ícone de lixeira (**Excluir / Desativar**) e confirme clicando em **`Desativar`** no modal **Confirmar Desativação**.
   - O consultor perde o acesso ao sistema e deixa de constar como membro ativo de equipe, mas seus contratos permanecem preservados nos relatórios.
   - Caso o consultor seja **Titular de Matrícula**, o sistema exibirá um alerta lembrando de transferir a titularidade caso a matrícula continue em uso.
2. **Para reativar**: Altere o filtro de status para **Inativos** (ou **Todos**), localize o consultor e clique no ícone de atualização (**Reativar**) na coluna **Ações**.

---

## 3. Dúvidas Comuns / Troubleshooting
- **Aparece "Apenas leitura" na coluna Ações**: Isso significa que o consultor não está abaixo de você na árvore hierárquica. Apenas o gestor superior correspondente (ou um SuperAdmin) pode editá-lo ou desativá-lo.
- **Um consultor precisa alterar o próprio superior ou solicitar mudança de equipe**: Ele deve utilizar o módulo **Solicitações** (`#/requests`) para pedidos que dependam de aprovação.
