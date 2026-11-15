# Como Acompanhar Meus Contratos

A tela **Meus Contratos** (`#/my-contracts`) é a área onde consultores e administradores acompanham sua própria carteira de vendas, indicadores financeiros, produção histórica e solicitações de atribuição de contratos.

---

## 1. Pré-requisitos
- Estar autenticado no sistema (disponível para **Usuários** e **Admins**).
- Possuir ao menos uma **matrícula** vinculada para solicitar/atribuir novos contratos.

---

## 2. Passo a Passo

### Passo 1: Acessar e Filtrar Sua Carteira
1. No menu lateral, clique em **Meus Contratos** (rota: `#/my-contracts`).
2. Utilize os filtros no topo da página:
   - **Data Início** e **Data Fim**
   - **Filtrar por matrícula** *(multi-seleção)*
   - **Equipe** *(exibido quando você participa de equipes)*
3. Clique em **`Limpar Filtros`** para voltar à visão completa ou em **`Atualizar`** no topo para recarregar os dados.

---

### Passo 2: Consultar Tabela, Resumo e Produção Histórica
1. Na tabela principal, consulte seus contratos com **Número do Contrato**, **Cliente**, **Matrícula**, **Grupo**, **Segmento**, **Valor Total**, **Status** e **Data Início**.
2. Abaixo da tabela, acompanhe:
   - **Cards de Resumo**: Total Geral, Total Ativo, Total Atrasado, Total Cancelado e Retenção.
   - **Produção Histórica**: Gráfico de evolução da sua produção no período.

---

### Passo 3: Atribuir ou Solicitar um Contrato (`+ Novo`)
1. No canto superior direito, clique em **`+ Novo`** (ou consulte o botão de ajuda **`Como Atribuir um contrato?`**).
2. Digite o **Número do Contrato** e selecione a **Matrícula** correspondente.
3. Confirme o envio:
   - Se o contrato já estiver importado no sistema, ele será vinculado à sua carteira.
   - Se ainda não tiver sido importado, ele ficará listado na seção **Contratos Solicitados** com o selo **`Aguardando importação`**, e será atribuído automaticamente assim que a importação ocorrer.
4. Caso tenha solicitado por engano, clique em **`Cancelar`** na linha do contrato dentro de **Contratos Solicitados**.

> **Atenção Proprietário**: Se você for titular de matrícula e outros usuários tiverem solicitado contratos ainda não importados vinculados à sua matrícula, a tela exibirá um alerta listando esses pedidos para que você realize a importação.

---

### Passo 4: Exportar Seus Contratos
1. Aplique os filtros desejados (período, matrícula, equipe).
2. Clique em **`Exportar`** no topo da página e aguarde a barra de progresso concluir para baixar a planilha.

---

## 3. Dúvidas Comuns / Troubleshooting
- **Não encontro o contrato no sistema (ou não aparece na listagem)**:
  1. **Filtros ativos ou mal formatados**: Verifique se há filtros preenchidos restringindo a busca (especialmente **Data Início** e **Data Fim**, matrícula ou equipe). É sempre recomendável clicar em **`Limpar Filtros`** para visualizar todos os registros.
  2. **Contrato ainda não está no sistema (sincronização com o Avapro)**: Todos os dias sincronizamos o banco de dados com os contratos do **Avapro (Ava Pro)**. Por isso, um novo contrato pode demorar **até 24h** para aparecer no sistema.
- **Um contrato aparece como cancelado (ou desatualizado), mas o cliente já pagou e está ativo**:
  1. **O Avapro ainda exibe como cancelado**: Nesse caso, é necessário entrar em contato com a **Ademicon** para regularizar a situação no Avapro.
  2. **O Avapro mudou o status há menos de 24h**: Como a sincronização ocorre todos os dias pela manhã, basta aguardar a próxima sincronização matinal para que o novo status seja refletido no sistema.
- **Meu contrato apareceu em "Contratos Solicitados" e não na tabela principal**: Isso significa que o contrato ainda não entrou na sincronização diária com o Avapro / Power BI (que pode levar até 24h). Assim que for importado/sincronizado, o vínculo é feito automaticamente.
- **Preciso editar dados de um contrato já cadastrado**: A tela **Meus Contratos** é voltada para acompanhamento e atribuição. Alterações cadastrais em contratos são feitas na tela administrativa **Contratos** (`#/contracts`).
