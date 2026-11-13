# Como Gerenciar Equipes (Lista)

A tela **Equipes → Lista** (`#/teams`) permite que administradores visualizem suas equipes, filtrem por status ou membros e gerenciem a composição e os períodos de participação de cada integrante.

---

## 1. Pré-requisitos
- Possuir perfil **Admin** (ou permissão `teams:manage`).
- **Criação de nova equipe**: Um perfil Admin não cria equipes diretamente pelo botão nesta tela. Caso você tenha sido promovido a **Guimel** e precise criar sua própria equipe, utilize o módulo **Solicitações** (`#/requests`) selecionando a opção *Eu sou Guimel agora, quero criar minha equipe*.

---

## 2. Passo a Passo

### Passo 1: Acessar, Buscar e Filtrar Equipes
1. No menu lateral, clique em **Equipes** e depois em **Lista** (rota: `#/teams`).
2. Use a barra de busca (**Buscar por equipe, proprietário ou membro...**) para localizar uma equipe pelo nome, pelo líder ou por qualquer integrante.
3. Use o seletor de status ao lado da busca para alternar entre:
   - **Ativas** (padrão)
   - **Inativas**
   - **Todas**
4. Clique em **`Atualizar`** no topo direito sempre que quiser recarregar a listagem.

---

### Passo 2: Editar Equipe e Gerenciar Membros
1. Localize a equipe desejada na tabela (que exibe *Nome da Equipe*, *Proprietário (Owner)*, *Loja*, *Membros* e *Criado em*).
2. Na coluna **Ações**, clique no ícone de lápis (**Editar**).
3. No modal de edição da equipe, você pode:
   - Atualizar o **Nome da Equipe**.
   - **Selecionar Membros da Equipe**: adicionar novos consultores ou ajustar os membros vinculados e seus períodos.
4. Salve as alterações.

> **Resolução automática de conflitos**: Se um usuário for associado a uma nova equipe em um período que sobrepõe o vínculo anterior, o sistema encerra/ajusta o vínculo conflitante e exibe um aviso (*"Conflitos de Associação Resolvidos!"*) listando os usuários ajustados.

---

## 3. Dúvidas Comuns / Troubleshooting
- **Preciso separar contratos antigos e novos de um consultor que mudou de equipe ou virou gestor**: Utilize a tela **Equipes → Calendário** (`#/teams/calendar`) para definir a data de corte exata entre a equipe anterior e a equipe atual.
- **Não aparece o botão "Nova Equipe" para mim**: Para perfis Admin, a criação de novas equipes é feita mediante aprovação em **Solicitações** (`#/requests`).
