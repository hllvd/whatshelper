# Como Gerenciar e Solicitar Matrículas

A tela **Matrículas** (`#/matriculas`) permite que administradores consultem as matrículas cadastradas, acompanhem vínculos de usuários, verifiquem titularidade e solicitem novas matrículas.

---

## 1. Pré-requisitos
- Para acessar a tela **Matrículas** (`#/matriculas`): possuir perfil **Admin** (ou permissão `matriculas:read`).
- Para consultores sem acesso à tela de Matrículas: utilizar o módulo **Solicitações** (`#/requests`).

---

## 2. Passo a Passo na Tela de Matrículas (Admin)

### Passo 1: Pesquisar e Filtrar Matrículas
1. No menu lateral, clique em **Matrículas** (rota: `#/matriculas`).
2. Use o campo **Pesquisar** para buscar por número de matrícula (é possível informar vários números separados por vírgula) ou pelo nome do usuário vinculado.
3. Utilize o seletor **Status da Matrícula** para filtrar entre:
   - **Todas**
   - **Ativas**
   - **Inativas**
4. Clique em **`Atualizar`** no canto superior direito sempre que quiser recarregar os dados mais recentes.

---

### Passo 2: Entender as Colunas da Tabela
Na listagem, você visualiza:
- **Número da Matrícula** e **Usuário** vinculado.
- **Data Início** e **Data Fim** de vigência.
- **Ativo** e **Status** da matrícula.
- **Proprietário**: indica quem é o titular oficial da matrícula.
- **Última Atualização**: data da modificação mais recente.

---

### Passo 3: Solicitar uma Nova Matrícula (Admin)
1. No canto superior direito da tela **Matrículas**, clique no botão **`Solicitar Matrícula`**.
2. Informe o **Número da Matrícula** (apenas números) no modal aberto.
3. Clique em **`Enviar Solicitação`** para encaminhar o pedido para validação e criação.

---

## 3. Adendo: Como Criar ou Vincular Matrículas via Solicitações (Consultor e Gestor)

Consultores e gestores que precisam de uma nova matrícula (ou de acesso à matrícula de um líder) devem usar o módulo **Solicitações** (`#/requests`):

1. Acesse **Solicitações** (`#/requests`) e clique em **`+ Nova Solicitação`**.
2. Escolha o tipo adequado ao seu caso:
   - **Solicitação de Matrícula (Proprietário)**: para gestores/proprietários ou consultores que precisam cadastrar ou reivindicar uma nova matrícula própria ou de filial no sistema. Informe o **Número da Matrícula** e aguarde a aprovação.
   - **Solicitar o uso da matrícula do gestor**: para consultores que precisam utilizar a matrícula corporativa do seu gestor direto. Informe o **Número da Matrícula** do gestor e aguarde a aprovação do titular.

> **Importante — Ao tentar adicionar um contrato e a matrícula não aparecer:**
> Se você acessar **Meus Contratos** (`#/my-contracts`), clicar em **`+ Novo`** e perceber que a matrícula desejada **não aparece na lista**, isso significa que ela ainda não está cadastrada ou vinculada ao seu usuário.
> Nesse caso, acesse **Solicitações** (`#/requests`) e escolha entre:
> - **Solicitar uma nova matrícula** (*Solicitação de Matrícula (Proprietário)*), ou
> - **Solicitar o uso da matrícula do seu gestor** (*Solicitar o uso da matrícula do gestor*).
>
> Assim que a solicitação for aprovada, a matrícula ficará disponível para seleção ao adicionar seus contratos.

---

## 4. Dúvidas Comuns / Troubleshooting
- **Já enviei a solicitação, mas a matrícula ainda não aparece**: Verifique na aba **Minhas Solicitações** em `#/requests` se o status ainda está como **Pendente** de aprovação ou se foi **Rejeitado** (com justificativa).
- **Preciso buscar várias matrículas de uma vez**: No campo **Pesquisar** da tela `#/matriculas`, digite os números separados por vírgula (ex.: `123456, 789012`).
