# Como Ajustar Membros de Equipe Usando o Calendário

Este guia explica como gerenciar a linha do tempo e o histórico de equipes de um consultor no **Calendário de Equipes**.

---

## 📌 Quando e Por Que Usar Esta Funcionalidade?

> ⚠️ **Regra Fundamental — Consultor "saiu da equipe"**:
> Quando alguém informar que um consultor **saiu da equipe** (ou "não faz mais parte da equipe"), **sempre confirme primeiro** se ele **saiu da Ademicon (empresa)** ou se **migrou para outra equipe**:
> 1. **Saiu da Ademicon (não trabalha mais na empresa)**: **NÃO** utilize o Calendário de Equipes. O procedimento correto é **somente desativar o usuário** em **Usuários → Lista** (`#/users`), clicando no ícone de lixeira (**Excluir / Desativar**) e confirmando em **`Desativar`**.
> 2. **Saiu da equipe e migrou para outra equipe (ou virou gestor de nova equipe na Ademicon)**: utilize o **Calendário de Equipes** (`#/teams/calendar`) para ajustar a data de corte entre a equipe antiga e a nova equipe.

O uso do **Calendário de Equipes** é **muito importante** especialmente nos cenários em que o consultor **continua na Ademicon**, mas mudou de time:

- **Consultor que se tornou gestor**: Quando um consultor é promovido e passa a liderar sua própria equipe, é essencial registrar a transição no calendário para encerrar sua participação na equipe antiga e iniciar o vínculo na nova equipe.
- **Consultor que mudou/migrou de equipe**: Quando um consultor atuou em uma equipe durante um período e foi remanejado para outra equipe dentro da Ademicon.
- **Correção de datas de vigência**: Elimina sobreposições ou lacunas no histórico do consultor.

### ✂️ Como Funciona a Data de Corte
Ao editar o histórico no calendário, você define uma **data de corte** (a **Data Final** da equipe anterior e a **Data de Início** da nova equipe) que **separa os contratos da equipe recente (ativa) dos contratos da equipe anterior**:
- **Contratos fechados até a data de corte** permanecem contabilizados na **equipe anterior**.
- **Contratos fechados a partir da data de início da nova equipe** passam a contar exclusivamente para a **equipe recente (ativa)**.

Isso garante que metas, comissões e relatórios por equipe não misturem vendas passadas com a produção atual.

### ❓ Por que ao filtrar por equipe continuam aparecendo vendas de consultor que já foi da equipe ou virou líder/gestor?
Se ao filtrar por equipe na tela **Contratos** (`#/contracts`) ou **Meus Contratos** (`#/my-contracts`) continuarem aparecendo vendas de um consultor que já saiu da equipe, migrou ou foi promovido a líder/gestor:
- **Causa**: As datas de vigência da equipe antiga e da nova equipe no **Calendário de Equipes** estão sobrepostas ou com a data final em branco na equipe anterior.
- **Conceito de Líder**: Na plataforma e na operação, **Líder** é sinônimo de **Gestor** (ou Administrador).
- **Solução**: Acesse **Equipes → Calendário de Equipes** (`#/teams/calendar`), localize o consultor/líder e ajuste a **data de corte**:
  1. No bloco da **equipe anterior**, preencha a **Data Final** com o último dia em que ele atuou na equipe antiga.
  2. No bloco da **nova equipe**, defina a **Data de Início** correspondente à sua entrada na nova equipe (ou início como gestor).
  3. Mantenha a equipe atual sem data de fim (aberta/vigente).
Assim que as datas de corte estiverem ajustadas, o filtro por equipe separará corretamente os contratos antigos dos novos.

> ⚠️ **Atenção Importante:** Todo consultor deve **sempre manter uma equipe ativa** (sem data final definida ou com a data mais recente aberta). Se o consultor ficar sem nenhuma equipe ativa, suas novas vendas não serão associadas adequadamente nos relatórios operacionais.

---

## 🎬 Vídeo Demonstrativo

Recomendamos fortemente que você assista ao vídeo abaixo antes de realizar os ajustes. Ele demonstra todo o processo na prática:

<div style="max-width: 100%; aspect-ratio: 16/9; margin: 1.5rem 0;">
  <iframe 
    width="100%" 
    height="100%" 
    src="https://www.youtube.com/embed/GW7HtaYaxy8" 
    title="Como ajustar membros de equipe usando o calendário" 
    frameborder="0" 
    allow="accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture" 
    allowfullscreen>
  </iframe>
</div>

---

## ⚙️ Passo a Passo Rápido

1. **Acesse o Calendário**: No menu, vá em **Equipes → Calendário de Equipes** (rota: `#/teams/calendar`).
2. **Localize o Consultor**: Busque pelo nome ou matrícula e clique sobre ele para abrir a linha do tempo.
3. **Ajuste as Equipes Existentes ou Vincule uma Nova**:
   - **Para ajustar equipes já no painel**: Clique no botão de editar no bloco da equipe para corrigir as datas de início ou fim.
   - **Para vincular uma nova equipe**: Clique em **"Vincular Equipe"**, selecione o time desejado e defina a data de início.
4. **Feche o Período Anterior**: No bloco da equipe antiga, certifique-se de preencher a **Data Final** com o último dia em que o consultor atuou nela.
5. **Confirme a Equipe Ativa**: Verifique qual é a equipe vigente atual. Ela **não pode ter data de fim** (campo em branco / "Atual"), garantindo que continue recebendo novas vendas.
6. **Salve as alterações**.
