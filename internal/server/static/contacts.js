(function() {
  'use strict';

  const tenantInput = document.getElementById('tenant-select');
  const searchInput = document.getElementById('contact-search');
  const refreshBtn = document.getElementById('btn-refresh');
  const contactsListEl = document.getElementById('contacts-list');
  const contactDetailEl = document.getElementById('contact-detail');
  const globalListeningBtn = document.getElementById('btn-toggle-global-listening');

  let selectedContactId = null;
  let searchDebounceTimer = null;
  let globalListeningActive = false;

  function currentTenant() {
    return (tenantInput.value || 'demo').trim();
  }

  async function loadGlobalListening() {
    try {
      const res = await fetch(`/api/listening/config?tenant=${encodeURIComponent(currentTenant())}`);
      if (!res.ok) return;
      const data = await res.json();
      globalListeningActive = !!data.global;
      updateGlobalListeningBtn();
    } catch (e) {
      console.error('Erro ao carregar listening global:', e);
    }
  }

  function updateGlobalListeningBtn() {
    if (globalListeningActive) {
      globalListeningBtn.textContent = 'Ativado (Silencioso)';
      globalListeningBtn.style.backgroundColor = 'var(--add-fg)';
      globalListeningBtn.style.color = '#fff';
    } else {
      globalListeningBtn.textContent = 'Desativado';
      globalListeningBtn.style.backgroundColor = '';
      globalListeningBtn.style.color = '';
    }
  }

  async function toggleGlobalListening() {
    try {
      const next = !globalListeningActive;
      const res = await fetch(`/api/listening/config?tenant=${encodeURIComponent(currentTenant())}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ global: next })
      });
      if (res.ok) {
        globalListeningActive = next;
        updateGlobalListeningBtn();
      }
    } catch (e) {
      alert('Falha ao alterar listening global: ' + e.message);
    }
  }

  async function loadContacts() {
    const q = (searchInput.value || '').trim();
    contactsListEl.innerHTML = '<div style="padding:1rem; color:var(--muted); text-align:center;">Buscando clientes...</div>';

    try {
      const res = await fetch(`/api/contacts?tenant=${encodeURIComponent(currentTenant())}&q=${encodeURIComponent(q)}`);
      if (!res.ok) throw new Error('Erro HTTP ' + res.status);
      const data = await res.json();
      const contacts = data.contacts || [];

      if (contacts.length === 0) {
        contactsListEl.innerHTML = '<div style="padding:1rem; color:var(--muted); text-align:center;">Nenhum cliente encontrado.</div>';
        return;
      }

      contactsListEl.innerHTML = '';
      contacts.forEach(c => {
        const card = document.createElement('div');
        card.className = 'contact-card' + (c.id === selectedContactId ? ' selected' : '');
        card.tabIndex = 0;

        const listeningBadge = c.listening 
          ? '<span class="status-pill" style="background:var(--add-bg);color:var(--add-fg);">Listening</span>' 
          : '';

        const name = c.name || c.email || c.phone || c.id;
        card.innerHTML = `
          <div class="contact-header">
            <span class="contact-name">${escapeHtml(name)}</span>
            <div style="display:flex; align-items:center; gap:0.35rem;">
              ${listeningBadge}
              <span class="status-pill">${c.sources_count || 0} canais</span>
            </div>
          </div>
          <div class="contact-meta">
            ${c.email ? `<span>✉️ ${escapeHtml(c.email)}</span>` : ''}
            ${c.phone ? `<span>📞 ${escapeHtml(c.phone)}</span>` : ''}
            <span>🆔 ${escapeHtml(c.id)}</span>
          </div>
          ${c.last_summary ? `<div style="font-size:0.8rem; color:var(--muted); margin-top:0.35rem; display:-webkit-box; -webkit-line-clamp:2; -webkit-box-orient:vertical; overflow:hidden;">${escapeHtml(c.last_summary)}</div>` : ''}
        `;

        card.addEventListener('click', () => selectContact(c.id));
        contactsListEl.appendChild(card);
      });
    } catch (e) {
      contactsListEl.innerHTML = `<div style="padding:1rem; color:var(--rem-fg); text-align:center;">Erro: ${escapeHtml(e.message)}</div>`;
    }
  }

  async function selectContact(contactId) {
    selectedContactId = contactId;
    document.querySelectorAll('.contact-card').forEach(el => el.classList.remove('selected'));

    contactDetailEl.innerHTML = '<div style="color:var(--muted); text-align:center; padding:2rem;">Carregando dados do cliente...</div>';

    try {
      const res = await fetch(`/api/contacts/${encodeURIComponent(contactId)}?tenant=${encodeURIComponent(currentTenant())}`);
      if (!res.ok) throw new Error('Cliente não encontrado');
      const c = await res.json();

      let sourcesHtml = '<p style="color:var(--muted); font-size:0.85rem;">Nenhum canal vinculado.</p>';
      let summariesHtml = '';
      let factsHtml = '';

      if (c.sources && c.sources.length > 0) {
        sourcesHtml = '<div style="display:flex; flex-direction:column; gap:0.5rem;">';
        c.sources.forEach(s => {
          const stateKey = `${s.channel}/${s.source_key}`;
          const state = c.source_states ? c.source_states[stateKey] : null;
          const status = state && state.meta ? state.meta.status : 'desconhecido';
          const stage = state && state.meta ? state.meta.journey_stage : '';

          sourcesHtml += `
            <div style="display:flex; justify-content:space-between; align-items:center; background:var(--surface-alt); padding:0.5rem 0.75rem; border-radius:6px; border:1px solid var(--border);">
              <div>
                <strong>${escapeHtml(s.channel)}:</strong> <code>${escapeHtml(s.source_key)}</code>
                <span class="status-pill" style="margin-left:0.5rem;">${escapeHtml(status)}</span>
                ${stage ? `<span class="status-pill" style="margin-left:0.25rem;">etapa: ${escapeHtml(stage)}</span>` : ''}
              </div>
              <div style="display:flex; gap:0.4rem;">
                <a class="source-pill-link" href="/?source_key=${encodeURIComponent(s.source_key)}&channel=${encodeURIComponent(s.channel)}" target="_blank">Chat ↗</a>
                <a class="source-pill-link" href="/thinking?source_key=${encodeURIComponent(s.source_key)}&channel=${encodeURIComponent(s.channel)}" target="_blank">Thinking ↗</a>
              </div>
            </div>
          `;

          if (state && state.summary && state.summary.text) {
            summariesHtml += `<div class="summary-box"><strong>${escapeHtml(s.channel)} (${escapeHtml(s.source_key)}):</strong>\n${escapeHtml(state.summary.text)}</div>`;
          }

          if (state && state.facts) {
            for (const [k, v] of Object.entries(state.facts)) {
              factsHtml += `<li class="fact-item"><strong>${escapeHtml(k)}:</strong> ${escapeHtml(v)}</li>`;
            }
          }
        });
        sourcesHtml += '</div>';
      }

      contactDetailEl.innerHTML = `
        <div class="detail-section" style="display:flex; justify-content:space-between; align-items:flex-start;">
          <div>
            <h2 style="margin:0 0 0.25rem 0;">${escapeHtml(c.name || c.id)}</h2>
            <div style="color:var(--muted); font-size:0.85rem;">ID: <code>${escapeHtml(c.id)}</code></div>
          </div>
          <button id="btn-delete-contact" class="btn-danger" type="button">🗑 Apagar Cliente & Memória</button>
        </div>

        <div class="detail-section">
          <div class="detail-title">Metadados de Contato</div>
          <div style="display:grid; grid-template-columns:1fr 1fr; gap:0.75rem; font-size:0.9rem;">
            <div><strong>E-mail:</strong> ${escapeHtml(c.email || 'Não informado')}</div>
            <div><strong>Telefone:</strong> ${escapeHtml(c.phone || 'Não informado')}</div>
            <div><strong>Cadastrado em:</strong> ${formatDate(c.created_at)}</div>
            <div><strong>Última atualização:</strong> ${formatDate(c.updated_at)}</div>
          </div>
          <div style="margin-top:1rem; display:flex; align-items:center; gap:0.75rem;">
            <span style="font-weight:600; font-size:0.9rem;">Modo Listening para este cliente:</span>
            <button id="btn-toggle-client-listening" class="btn-sm" type="button">
              ${c.listening ? 'Desativar Listening' : 'Ativar Listening'}
            </button>
          </div>
        </div>

        <div class="detail-section">
          <div class="detail-title">Canais & Fontes Conectadas</div>
          ${sourcesHtml}
        </div>

        <div class="detail-section">
          <div class="detail-title">Memória Acumulada do LLM (Resumo)</div>
          ${summariesHtml || '<p style="color:var(--muted); font-size:0.85rem;">Nenhum resumo gerado ainda.</p>'}
        </div>

        <div class="detail-section">
          <div class="detail-title">Fatos Conhecidos</div>
          ${factsHtml ? `<ul class="facts-list">${factsHtml}</ul>` : '<p style="color:var(--muted); font-size:0.85rem;">Nenhum fato extraído.</p>'}
        </div>
      `;

      // Handlers
      document.getElementById('btn-delete-contact').addEventListener('click', () => deleteContact(c.id, c.name || c.id));
      document.getElementById('btn-toggle-client-listening').addEventListener('click', () => toggleClientListening(c.id, !c.listening));

    } catch (e) {
      contactDetailEl.innerHTML = `<div style="color:var(--rem-fg); text-align:center; padding:2rem;">Erro ao carregar detalhes: ${escapeHtml(e.message)}</div>`;
    }
  }

  async function toggleClientListening(contactId, nextState) {
    try {
      const res = await fetch(`/api/contacts/${encodeURIComponent(contactId)}/listening?tenant=${encodeURIComponent(currentTenant())}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ listening: nextState })
      });
      if (!res.ok) throw new Error('Erro ao atualizar modo listening');
      await selectContact(contactId);
      await loadContacts();
    } catch (e) {
      alert(e.message);
    }
  }

  async function deleteContact(contactId, contactName) {
    const confirmed = confirm(
      `ATENÇÃO: Deseja apagar permanentemente o cliente "${contactName}"?\n\n` +
      `Isso excluirá:\n` +
      `- O cadastro do contato e seus metadados\n` +
      `- Todos os canais vinculados\n` +
      `- Todo o histórico de mensagens\n` +
      `- Toda a memória do LLM (resumo, fatos, contexto, jornada e runs)\n\n` +
      `Esta ação não pode ser desfeita.`
    );
    if (!confirmed) return;

    try {
      const res = await fetch(`/api/contacts/${encodeURIComponent(contactId)}?tenant=${encodeURIComponent(currentTenant())}`, {
        method: 'DELETE'
      });
      if (!res.ok) throw new Error('Erro ao excluir contato');
      selectedContactId = null;
      contactDetailEl.innerHTML = '<div style="color:var(--add-fg); text-align:center; padding:3rem 1rem;">Cliente e histórico apagados com sucesso!</div>';
      await loadContacts();
    } catch (e) {
      alert('Falha ao excluir: ' + e.message);
    }
  }

  function escapeHtml(str) {
    if (!str) return '';
    return String(str)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;');
  }

  function formatDate(isoStr) {
    if (!isoStr) return '-';
    try {
      const d = new Date(isoStr);
      return d.toLocaleString('pt-BR');
    } catch (e) {
      return isoStr;
    }
  }

  // Event Listeners
  searchInput.addEventListener('input', () => {
    clearTimeout(searchDebounceTimer);
    searchDebounceTimer = setTimeout(loadContacts, 250);
  });

  refreshBtn.addEventListener('click', () => {
    loadContacts();
    loadGlobalListening();
  });

  tenantInput.addEventListener('change', () => {
    selectedContactId = null;
    loadContacts();
    loadGlobalListening();
  });

  globalListeningBtn.addEventListener('click', toggleGlobalListening);

  // Inicialização
  loadGlobalListening();
  loadContacts();
})();
