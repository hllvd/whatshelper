# Como Visualizar e Navegar na Árvore de Usuários

A tela **Usuários → Árvore** (`#/users/tree`) apresenta uma visualização hierárquica completa dos usuários subordinados à sua conta, facilitando o entendimento da estrutura de liderança, equipes e vínculos.

---

## 1. Pré-requisitos
- Possuir perfil **Admin** (ou permissão `users:read`).

---

## 2. Passo a Passo

### Passo 1: Acessar a Árvore de Usuários
1. No menu lateral, clique em **Usuários** e selecione **Árvore** (rota: `#/users/tree`).
2. Por padrão, os nós raiz são carregados expandidos. Clique nas setas (**`>`** / **`v`**) ao lado de cada usuário para expandir ou recolher seus subordinados.
3. No topo da área da árvore, você acompanha dois indicadores rápidos:
   - Total de usuários exibidos na árvore.
   - **Profundidade máxima** da hierarquia carregada.

---

### Passo 2: Buscar e Ajustar a Profundidade
1. Use o campo **Buscar na Árvore** (*Buscar por nome, email ou função...*) para destacar visualmente os usuários correspondentes na hierarquia (também busca pelo nome da equipe da qual o usuário é dono).
2. Ajuste o campo **Profundidade da Hierarquia** (de `1` a `100`, padrão `10`) caso precise limitar ou ampliar quantos níveis abaixo de você serão exibidos.

---

### Passo 3: Entender os Indicadores de Cada Nó
Cada item da árvore exibe:
- **Nome e E-mail** do usuário.
- **Badge de Função**: `Admin`, `Usuário` (ou `Super Admin`).
- **Badge `Inativo`**: Exibido quando a conta está desativada.
- **Badge `Dono: [Nome da Equipe]`**: Indica que o usuário é líder/dono de uma equipe.

---

### Passo 4: Ações Rápidas Direto na Árvore
Ao passar o mouse ou olhar à direita de cada usuário na árvore, você pode utilizar os ícones de ação rápida:
1. **Ir para Equipe** (ícone de grupo em verde, quando o usuário é dono de equipe): Abre diretamente a tela de **Equipes** (`#/teams`) já filtrada pela equipe daquele líder.
2. **Editar Usuário** (ícone de lápis azul, disponível para Admin): Abre o formulário de edição do usuário selecionado sem sair da árvore.
3. **Visualizar Perfil** (ícone de olho): Abre o modal de perfil completo do usuário para consulta rápida de detalhes e matrículas.

---

## 3. Dúvidas Comuns / Troubleshooting
- **A tela exibe "Nenhum usuário na hierarquia"**: Sua conta ainda não possui usuários subordinados vinculados a você como Usuário Pai.
- **Um subordinado não aparece na árvore**: Verifique se o campo **Profundidade da Hierarquia** está alto o suficiente para alcançar o nível dele, ou confira na tela **Usuários → Lista** (`#/users`) quem está configurado como **Usuário Pai** dessa conta.
