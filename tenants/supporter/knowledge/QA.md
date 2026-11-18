# Perguntas e Respostas Aprovadas (QA)

P: Um consultor saiu da minha equipe (ou não faz mais parte da nossa equipe / quero tirar ele da equipe). O que devo fazer?
R: Para te orientar com o procedimento exato, preciso confirmar primeiro: esse consultor saiu da Ademicon (não trabalha mais na empresa) ou ele migrou para outra equipe?
- Se ele saiu da Ademicon (não trabalha mais na empresa), basta somente desativar o usuário em Usuários → Lista (#/users), clicando no ícone de lixeira (Desativar).
- Se ele migrou para outra equipe (ou virou gestor de uma nova equipe), você deve ajustar a data de corte no Calendário de Equipes em Equipes → Calendário (#/teams/calendar).

P: O consultor saiu da Ademicon (não trabalha mais na empresa). Como faço para remover/desativar ele?
R: Quando o consultor saiu da Ademicon (não trabalha mais na empresa), você deve somente desativar o usuário. Acesse Usuários → Lista (#/users), localize o consultor pela busca, clique no ícone de lixeira (Excluir / Desativar) na coluna Ações e confirme clicando em Desativar. Não é necessário mexer no Calendário de Equipes.

P: O consultor saiu da minha equipe porque migrou para outra equipe (ou se tornou gestor de outra equipe). Como devo proceder?
R: Quando o consultor continua na Ademicon e migrou para outra equipe (ou virou gestor), você deve ajustar o Calendário de Equipes. Acesse Equipes → Calendário (#/teams/calendar), busque pelo consultor, edite o bloco da equipe anterior preenchendo a Data Final (data de corte) e vincule/confirme a nova equipe com a respectiva Data de Início (mantendo a equipe atual sem data final).

P: Estou filtrando por equipe em Contratos (#/contracts) e continuam aparecendo vendas de um consultor ou de outro gestor/líder que já foi da minha equipe mas agora está em outra equipe (ou virou líder/gestor). O que fazer?
R: Isso acontece porque esse consultor já fez parte da sua equipe no passado e as datas de vigência de equipe no Calendário de Equipes estão incorretas ou sobrepostas. Para corrigir:
1. No menu lateral, clique em Equipes e acesse o Calendário (`#/teams/calendar`).
2. Localize o consultor ou gestor/líder cujas vendas estão aparecendo indevidamente.
3. Ajuste a data de corte:
   - No bloco da equipe antiga, preencha a Data Final (último dia em que atuou nela).
   - No bloco da nova equipe, preencha a Data de Início (dia em que iniciou na nova equipe / como gestor).
   - A equipe atual deve ficar sem data final (aberta/vigente).
A data de corte no Calendário de Equipes é o que define quais contratos pertencem à equipe antiga (fechados até a data final) e quais pertencem à nova equipe (fechados a partir da data de início). Após esse ajuste, ao filtrar por equipe em Contratos, as vendas da nova equipe não serão mais puxadas para a equipe antiga.
(Nota: No sistema, Líder é sinônimo de Gestor ou Administrador).

P: Fui promovido ao nível Guimel (bati o Guimel) e quero criar minha própria equipe. Como faço?
R: No menu lateral, clique em Solicitações (#/requests), clique em "+ Nova Solicitação", selecione o tipo "Eu sou Guimel agora, quero criar minha equipe", preencha o Nome da Equipe e envie. A solicitação será encaminhada ao gestor e ao SuperAdmin. Quando aprovada, a equipe será criada automaticamente, você se tornará o proprietário (Owner) e seu perfil será atualizado para Administrador.

P: Eu não encontro o contrato no sistema (ou meu contrato não aparece na lista). O que pode ser?
R: Se você não está encontrando um contrato no sistema, verifique os dois motivos mais comuns:
1. Filtros ativos ou mal formatados: pode haver algum filtro restringindo a listagem (como Data Início e Data Fim, matrícula ou status). Clique sempre no botão "Limpar Filtros" no topo da tela para garantir que a busca não esteja sendo bloqueada.
2. Contrato ainda não sincronizado: todos os dias sincronizamos o banco de dados com os contratos do Avapro (Ava Pro). Por isso, um contrato recém-fechado pode demorar até 24 horas para aparecer no sistema. Se você for consultor e já quiser deixá-lo vinculado em Meus Contratos (#/my-contracts), pode clicar em "+ Novo" para solicitá-lo — ele ficará em "Contratos Solicitados" (Aguardando importação) e será atribuído automaticamente assim que a sincronização ocorrer.

P: Tem um contrato que está como cancelado e já está ativo, o cliente pagou mas o sistema ainda não reflete. Por que isso acontece e o que fazer?
R: Isso acontece por um destes dois motivos:
1. No Avapro (Ava Pro) o contrato ainda consta como cancelado: nesse caso, é necessário entrar em contato com a Ademicon (para verificar/regularizar junto ao Avapro).
2. O status no Avapro mudou há menos de 24h e ainda não refletiu aqui: nesse caso, basta aguardar a sincronização automática, que ocorre todos os dias pela manhã.

P: Você teria como me enviar um pequeno treinamento de como funciona o sistema, o que tenho que fazer como gestor etc.?
R: Claro! Como gestor, o seu papel principal no sistema é adicionar e remover consultores (em Usuários → Lista `#/users`) e dar suporte a eles no dia a dia. Veja o resumo do que você consegue fazer e acompanhar:
1. Visão da operação e equipes: você consegue visualizar todos os contratos abaixo de você, gerenciar as equipes abaixo de você (`#/teams` e `#/teams/calendar`) e acompanhar a evolução em Plano de Carreira (`#/my-career-plan`).
2. Uso de filtros em Contratos (`#/contracts`): utilizar os filtros na tela "Contratos" para filtrar a sua equipe, consultores ou período é fundamental para acompanhar os resultados do time.
3. Atribuição de contratos vendidos: oriente os consultores abaixo de você a atribuírem os próprios contratos vendidos acessando "Meus Contratos" (`#/my-contracts`) > "+ Novo" (informando o número do contrato e a matrícula). É papel do consultor fazer essa atribuição, embora você como gestor também consiga fazer isso manualmente na tela "Contratos" (`#/contracts`), editando o contrato e adicionando ou modificando o vendedor.

P: Você consegue saber quem é o meu gestor da equipe? / Você consegue saber quem é?
R: Consigo sim! Para que eu possa localizar o seu cadastro no sistema e verificar quem é o seu gestor responsável, por favor me informe o seu nome completo, seu e-mail de acesso ou o número de um contrato atribuído a você.

P: Os números não estão batendo (ou valores de fechamento/retenção não batem). O que fazer?
R: Para te ajudar a entender essa divergência, vamos verificar passo a passo:
1. Em qual página você está vendo essa diferença e qual é o contrato, venda ou matrícula (ex.: número do contrato ou matrícula)?
2. Você está comparando os números com a planilha "Modelo de Retenção"?
   - Se sim, nos envie o arquivo do modelo de retenção por aqui: um operador humano fará a análise comparativa detalhada do arquivo e devolverá o resultado para você.
   - Vale lembrar que os status na planilha manual do modelo de retenção geralmente ficam desatualizados, enquanto o sistema é atualizado periodicamente com a base oficial.
   - Uma recomendação importante para os gestores é pedir para que cada consultor da equipe verifique individualmente na sua própria carteira quais contratos estão sobrando ou faltando no sistema.

P: O que é retenção, quais são os tipos e como calcular?
R: Retenção é a capacidade de manter clientes, contas ou receitas ativos ao longo do tempo (o oposto de Churn). Os principais tipos são:
1. Retenção de Clientes (CRR): mede a proporção de contas preservadas: CRR = ((E - N) / S) * 100, onde S é início, E é fim e N são novos clientes.
2. Retenção de Receita Bruta (GRR): mede a receita preservada sem contar novas expansões: GRR = ((MRR Inicial - Churn - Downgrades) / MRR Inicial) * 100 (teto máximo 100%).
3. Retenção de Receita Líquida (NRR): considera perdas e ganhos adicionais (upsell/cross-sell): NRR = ((MRR Inicial + Expansão - Churn - Downgrades) / MRR Inicial) * 100. Acima de 100% indica que a base cresce organicamente.
4. Retenção por Safra/Cohort (Produto): avalia o retorno de usuários ativos ao longo do tempo (D1, D7, D30).
Para detalhes completos de fórmulas e exemplos numéricos, consulte o guia de retenção na base de conhecimento.
