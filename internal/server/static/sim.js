(function () {
  const tenantInput = document.getElementById('tenant-input');
  const sessionInput = document.getElementById('session-input');
  const newSessionBtn = document.getElementById('new-session-btn');
  const sourcesList = document.getElementById('sources-list');
  const pendingTasksEl = document.getElementById('pending-tasks');
  const unknownOriginListEl = document.getElementById('unknown-origin-list');
  const chatMessages = document.getElementById('chat-messages');
  const chatForm = document.getElementById('chat-form');
  const chatInput = document.getElementById('chat-input');
  const sendBtn = document.getElementById('send-button');
  const activeTitle = document.getElementById('active-session-title');
  const sourceStatusBadge = document.getElementById('source-status-badge');
  const sseStatus = document.getElementById('sse-status');

  const CHANNEL = 'simulator';
  const LS_TENANT = 'whatshelper.activeTenant';
  const LS_CHANNEL = 'whatshelper.activeChannel';
  const LS_SOURCE = 'whatshelper.activeSource';

  function saveActiveChat(tenant, channel, source) {
    try {
      localStorage.setItem(LS_TENANT, tenant);
      localStorage.setItem(LS_CHANNEL, channel);
      localStorage.setItem(LS_SOURCE, source);
    } catch (_) {}
  }

  let storedTenant = '';
  let storedChannel = '';
  let storedSource = '';
  try {
    storedTenant = localStorage.getItem(LS_TENANT) || '';
    storedChannel = localStorage.getItem(LS_CHANNEL) || '';
    storedSource = localStorage.getItem(LS_SOURCE) || '';
  } catch (_) {}

  if (storedTenant && tenantInput) {
    tenantInput.value = storedTenant;
  }
  let activeTenant = (tenantInput && tenantInput.value.trim()) || 'demo';
  let activeSource = (storedChannel === CHANNEL && storedSource) ? storedSource : (sessionInput.value.trim() || '');
  if (activeSource) {
    sessionInput.value = activeSource;
  }
  let hasExplicitSelection = Boolean(storedChannel === CHANNEL && storedSource);
  let sse = null;

  function renderBubble(direction, text, ts, messageId, origin) {
    const el = document.createElement('article');
    el.className = 'bubble ' + (direction === 'inbound' ? 'inbound' : 'outbound');

    const body = document.createElement('div');
    body.textContent = text;
    el.appendChild(body);

    const meta = document.createElement('span');
    meta.className = 'bubble-meta';
    const label = direction === 'inbound' ? 'Cliente' : 'Assistente';
    const timeStr = ts ? new Date(ts).toLocaleTimeString('pt-BR') : '';
    meta.textContent = label + (timeStr ? ' · ' + timeStr : '');
    if (origin) {
      const badge = document.createElement('span');
      badge.className = 'status-pill';
      badge.style.marginLeft = '0.4rem';
      badge.style.fontSize = '0.7rem';
      badge.textContent = 'origin:' + origin;
      meta.appendChild(badge);
    }
    el.appendChild(meta);

    if (direction === 'outbound') {
      const corrBtn = document.createElement('button');
      corrBtn.type = 'button';
      corrBtn.className = 'btn-sm';
      corrBtn.style.marginTop = '0.35rem';
      corrBtn.textContent = 'Corrigir resposta';
      corrBtn.addEventListener('click', async () => {
        const correction = window.prompt('Qual deveria ser a resposta correta? (será gerada proposta para QA.md)');
        if (!correction || !correction.trim()) return;
        const resp = await fetch('/api/learning/correct', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            tenant_id: activeTenant,
            channel: CHANNEL,
            source_key: activeSource,
            message_id: messageId || '',
            correction: correction.trim(),
          }),
        });
        if (resp.ok) {
          sseStatus.textContent = 'Proposta de aprendizado criada (/knowledge)';
        }
      });
      el.appendChild(corrBtn);
    }

    chatMessages.appendChild(el);
    chatMessages.scrollTop = chatMessages.scrollHeight;
  }

  async function loadMessages() {
    activeTitle.textContent = `Conversa (${activeTenant}): ${activeSource}`;
    chatMessages.textContent = '';
    try {
      const [msgRes, stateRes] = await Promise.all([
        fetch(`/api/sources/${encodeURIComponent(CHANNEL)}/${encodeURIComponent(activeSource)}/messages?tenant=${encodeURIComponent(activeTenant)}`),
        fetch(`/api/sources/${encodeURIComponent(CHANNEL)}/${encodeURIComponent(activeSource)}/state?tenant=${encodeURIComponent(activeTenant)}`),
      ]);
      if (msgRes.ok) {
        const data = await msgRes.json();
        for (const m of (data.messages || [])) {
          renderBubble(m.direction, m.text, m.timestamp, m.message_id, m.origin);
        }
      }
      if (stateRes.ok) {
        const st = await stateRes.json();
        sourceStatusBadge.textContent = (st.meta && st.meta.status) || 'active';
      }
    } catch (err) {
      console.error('Erro ao carregar mensagens:', err);
    }
  }

  async function loadSources() {
    try {
      const res = await fetch(`/api/sources?tenant=${encodeURIComponent(activeTenant)}`);
      if (!res.ok) return;
      const data = await res.json();
      const sources = data.sources || [];
      const simSources = sources.filter(s => s.channel === CHANNEL);
      if (!hasExplicitSelection && simSources.length > 0) {
        activeSource = simSources[0].source_key;
        sessionInput.value = activeSource;
        saveActiveChat(activeTenant, CHANNEL, activeSource);
        hasExplicitSelection = true;
        connectSSE();
        loadMessages();
      } else if (!activeSource) {
        activeSource = 'sim-user-1';
        sessionInput.value = activeSource;
        saveActiveChat(activeTenant, CHANNEL, activeSource);
      }
      sourcesList.textContent = '';
      for (const s of sources) {
        const li = document.createElement('li');
        const btn = document.createElement('button');
        btn.type = 'button';
        btn.textContent = `${s.channel}:${s.source_key} (${s.status})`;
        if (s.channel === CHANNEL && s.source_key === activeSource) {
          btn.className = 'active';
        }
        btn.addEventListener('click', () => {
          activeSource = s.source_key;
          hasExplicitSelection = true;
          sessionInput.value = activeSource;
          saveActiveChat(activeTenant, s.channel, s.source_key);
          connectSSE();
          loadMessages();
          loadSources();
        });
        li.appendChild(btn);
        sourcesList.appendChild(li);
      }
    } catch (err) {
      console.error('Erro ao listar fontes:', err);
    }
  }

  async function loadPendingTasks() {
    if (!pendingTasksEl) return;
    try {
      const res = await fetch(`/api/tasks/pending?tenant=${encodeURIComponent(activeTenant)}`);
      if (!res.ok) return;
      const data = await res.json();
      pendingTasksEl.textContent = '';
      const tasks = data.tasks || [];
      if (tasks.length === 0) {
        const empty = document.createElement('p');
        empty.style.fontSize = '0.8rem';
        empty.textContent = 'Nenhuma pendência.';
        pendingTasksEl.appendChild(empty);
        return;
      }
      for (const t of tasks) {
        const card = document.createElement('div');
        card.className = 'task-card';
        const title = document.createElement('strong');
        title.textContent = `[${t.kind || t.type}] ${t.title}`;
        card.appendChild(title);

        const info = document.createElement('span');
        info.textContent = `${t.channel}:${t.source_key}` + (t.metadata && t.metadata.preview ? ` — ${t.metadata.preview}` : '');
        card.appendChild(info);

        const actions = document.createElement('div');
        actions.className = 'toolbar-row';
        if (t.kind === 'approval') {
          const apprBtn = document.createElement('button');
          apprBtn.type = 'button';
          apprBtn.className = 'btn-sm primary';
          apprBtn.textContent = 'Aprovar & Enviar';
          apprBtn.addEventListener('click', async () => {
            await fetch(`/api/tasks/${encodeURIComponent(t.id)}/approve?tenant=${encodeURIComponent(activeTenant)}`, {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ tenant_id: activeTenant, channel: t.channel, source_key: t.source_key }),
            });
            loadPendingTasks();
            loadMessages();
          });
          actions.appendChild(apprBtn);
        } else {
          const doneBtn = document.createElement('button');
          doneBtn.type = 'button';
          doneBtn.className = 'btn-sm';
          doneBtn.textContent = 'Concluir';
          doneBtn.addEventListener('click', async () => {
            await fetch(`/api/tasks/${encodeURIComponent(t.id)}/complete?tenant=${encodeURIComponent(activeTenant)}`, {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ tenant_id: activeTenant, channel: t.channel, source_key: t.source_key }),
            });
            loadPendingTasks();
          });
          actions.appendChild(doneBtn);
        }
        card.appendChild(actions);
        pendingTasksEl.appendChild(card);
      }
    } catch (err) {
      console.error('Erro ao listar tarefas:', err);
    }
  }

  async function setSourceStatus(status, reason) {
    const res = await fetch(`/api/sources/${encodeURIComponent(CHANNEL)}/${encodeURIComponent(activeSource)}/status`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ tenant_id: activeTenant, status, reason }),
    });
    if (res.ok) {
      sourceStatusBadge.textContent = status;
      loadSources();
    }
  }

  function connectSSE() {
    if (sse) sse.close();
    const url = `/api/events?tenant=${encodeURIComponent(activeTenant)}&channel=${encodeURIComponent(CHANNEL)}&source=${encodeURIComponent(activeSource)}`;
    sse = new EventSource(url);

    sse.onopen = () => {
      sseStatus.textContent = 'SSE conectado';
    };
    sse.onerror = () => {
      sseStatus.textContent = 'SSE reconectando...';
    };

    sse.addEventListener('message.received', (ev) => {
      const data = JSON.parse(ev.data);
      if (data.payload && data.payload.direction === 'inbound') {
        renderBubble('inbound', data.payload.text || '', data.at);
        loadSources();
      }
    });

    sse.addEventListener('analysis.started', () => {
      sseStatus.textContent = 'Analisando...';
    });

    sse.addEventListener('reply.sent', (ev) => {
      const data = JSON.parse(ev.data);
      renderBubble('outbound', (data.payload && data.payload.text) || '', data.at, data.message_id || '');
      sseStatus.textContent = 'SSE conectado';
    });

    sse.addEventListener('approval.pending', () => {
      sseStatus.textContent = 'Aguardando aprovação';
      loadPendingTasks();
    });

    sse.addEventListener('task.created', () => {
      loadPendingTasks();
    });

    sse.addEventListener('source.status_updated', (ev) => {
      const data = JSON.parse(ev.data);
      if (data.payload && data.payload.status) {
        sourceStatusBadge.textContent = data.payload.status;
      }
      loadSources();
    });

    sse.addEventListener('run.skipped', (ev) => {
      const data = JSON.parse(ev.data);
      sseStatus.textContent = 'Ignorado: ' + ((data.payload && data.payload.reason) || '');
    });

    sse.addEventListener('pipeline.failed', () => {
      sseStatus.textContent = 'Falha na análise (fallback seguro)';
    });
  }

  chatInput.addEventListener('keydown', (event) => {
    if (event.key === 'Enter' && !event.shiftKey) {
      event.preventDefault();
      if (event.isComposing || event.keyCode === 229) {
        return;
      }
      chatForm.requestSubmit();
    }
  });

  let simTypingState = false;
  let simTypingIdleTimer = null;

  async function notifySimTyping(isTyping) {
    if (simTypingState === isTyping) return;
    simTypingState = isTyping;
    const tenant = (tenantInput && tenantInput.value.trim()) || activeTenant || 'demo';
    const source = (sessionInput && sessionInput.value.trim()) || activeSource || 'sim-user-1';
    try {
      await fetch('/api/sim/typing', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          tenant_id: tenant,
          source_key: source,
          typing: isTyping,
        }),
      });
    } catch (_) {
      // best-effort typing indicator
    }
  }

  chatInput.addEventListener('input', () => {
    if (simTypingIdleTimer) {
      clearTimeout(simTypingIdleTimer);
      simTypingIdleTimer = null;
    }
    if (chatInput.value.trim().length > 0) {
      notifySimTyping(true);
      simTypingIdleTimer = setTimeout(() => {
        notifySimTyping(false);
      }, 3000);
    } else {
      notifySimTyping(false);
    }
  });

  chatInput.addEventListener('blur', () => {
    if (simTypingIdleTimer) {
      clearTimeout(simTypingIdleTimer);
      simTypingIdleTimer = null;
    }
    notifySimTyping(false);
  });

  chatForm.addEventListener('submit', async (event) => {
    event.preventDefault();
    const text = chatInput.value.trim();
    if (!text) return;

    if (simTypingIdleTimer) {
      clearTimeout(simTypingIdleTimer);
      simTypingIdleTimer = null;
    }
    notifySimTyping(false);

    activeTenant = (tenantInput && tenantInput.value.trim()) || 'demo';
    activeSource = sessionInput.value.trim() || 'sim-user-1';
    sendBtn.disabled = true;
    try {
      const res = await fetch('/api/sim/messages', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          tenant_id: activeTenant,
          source_key: activeSource,
          text: text,
        }),
      });
      if (res.ok) {
        chatInput.value = '';
        chatInput.focus();
      }
    } finally {
      sendBtn.disabled = false;
    }
  });

  document.getElementById('btn-pause').addEventListener('click', () => setSourceStatus('paused', 'pausado pelo operador'));
  document.getElementById('btn-handoff').addEventListener('click', () => setSourceStatus('human', 'assumido por humano'));
  document.getElementById('btn-resume').addEventListener('click', () => setSourceStatus('active', 'reativado pelo operador'));

  async function submitSimOrigin(mode, defaultText) {
    const text = window.prompt('Texto da mensagem simulada:', defaultText);
    if (!text || !text.trim()) return;
    await fetch('/api/sim/origin', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        tenant_id: activeTenant,
        channel: CHANNEL,
        source_key: activeSource,
        mode,
        text: text.trim(),
      }),
    });
    loadMessages();
    loadUnknownOrigins();
  }

  const btnHumanPhone = document.getElementById('btn-sim-human-phone');
  if (btnHumanPhone) {
    btnHumanPhone.addEventListener('click', () => submitSimOrigin('human_phone', 'Oi, aqui é o atendente pelo celular!'));
  }
  const btnBotEcho = document.getElementById('btn-sim-bot-echo');
  if (btnBotEcho) {
    btnBotEcho.addEventListener('click', () => submitSimOrigin('bot_echo', 'Eco simulado do robô'));
  }

  const btnTestLLM = document.getElementById('btn-test-llm');
  const llmTestStatus = document.getElementById('llm-test-status');
  if (btnTestLLM && llmTestStatus) {
    btnTestLLM.addEventListener('click', async () => {
      btnTestLLM.disabled = true;
      llmTestStatus.style.display = 'inline-block';
      llmTestStatus.style.color = '';
      llmTestStatus.textContent = 'Testando LLM...';
      try {
        const res = await fetch('/api/llm/test', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ tenant: activeTenant, stage: 'response' }),
        });
        const data = await res.json().catch(() => ({}));
        if (!res.ok) {
          llmTestStatus.style.color = '#dc2626';
          llmTestStatus.textContent = `❌ ${data.error || 'Erro HTTP ' + res.status}`;
          return;
        }
        if (data.ok) {
          llmTestStatus.style.color = '#16a34a';
          const snippet = (data.content || '').trim().slice(0, 60);
          llmTestStatus.textContent = `✅ ${data.provider}/${data.model} (${data.duration_ms}ms): "${snippet}"`;
        } else {
          llmTestStatus.style.color = '#dc2626';
          llmTestStatus.textContent = `❌ ${data.provider || '-'}/${data.model || 'sem modelo'}: ${data.error || 'erro desconhecido'}`;
        }
      } catch (e) {
        llmTestStatus.style.color = '#dc2626';
        llmTestStatus.textContent = '❌ Falha de rede ao testar LLM';
      } finally {
        btnTestLLM.disabled = false;
      }
    });
  }

  async function loadUnknownOrigins() {
    if (!unknownOriginListEl) return;
    try {
      const res = await fetch(`/api/messages/unknown-origin?tenant=${encodeURIComponent(activeTenant)}`);
      if (!res.ok) return;
      const data = await res.json();
      unknownOriginListEl.textContent = '';
      const msgs = data.messages || [];
      if (msgs.length === 0) {
        const li = document.createElement('li');
        li.style.fontSize = '0.8rem';
        li.textContent = 'Nenhuma mensagem origin=unknown.';
        unknownOriginListEl.appendChild(li);
        return;
      }
      for (const m of msgs.slice(0, 10)) {
        const li = document.createElement('li');
        li.style.fontSize = '0.8rem';
        li.textContent = `${m.channel}:${m.source_key} · ${m.text || m.message_id}`;
        unknownOriginListEl.appendChild(li);
      }
    } catch (err) {
      console.error('Erro ao carregar auditoria origin=unknown:', err);
    }
  }

  if (tenantInput) {
    tenantInput.addEventListener('change', () => {
      activeTenant = tenantInput.value.trim() || 'demo';
      saveActiveChat(activeTenant, CHANNEL, activeSource);
      connectSSE();
      loadMessages();
      loadSources();
      loadPendingTasks();
    });
  }

  sessionInput.addEventListener('change', () => {
    activeSource = sessionInput.value.trim() || 'sim-user-1';
    hasExplicitSelection = true;
    saveActiveChat(activeTenant, CHANNEL, activeSource);
    connectSSE();
    loadMessages();
    loadSources();
  });

  newSessionBtn.addEventListener('click', () => {
    activeSource = 'sim-' + Math.random().toString(36).slice(2, 8);
    hasExplicitSelection = true;
    sessionInput.value = activeSource;
    saveActiveChat(activeTenant, CHANNEL, activeSource);
    connectSSE();
    loadMessages();
    loadSources();
  });

  if (activeSource) {
    connectSSE();
    loadMessages();
  }
  loadSources();
  loadPendingTasks();
  loadUnknownOrigins();
})();
