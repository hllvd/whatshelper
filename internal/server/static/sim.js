(function () {
  const sessionInput = document.getElementById('session-input');
  const newSessionBtn = document.getElementById('new-session-btn');
  const sourcesList = document.getElementById('sources-list');
  const chatMessages = document.getElementById('chat-messages');
  const chatForm = document.getElementById('chat-form');
  const chatInput = document.getElementById('chat-input');
  const sendBtn = document.getElementById('send-button');
  const activeTitle = document.getElementById('active-session-title');
  const sseStatus = document.getElementById('sse-status');

  const TENANT = 'demo';
  const CHANNEL = 'simulator';
  let activeSource = sessionInput.value.trim() || 'sim-user-1';
  let sse = null;

  function renderBubble(direction, text, ts) {
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
    el.appendChild(meta);

    chatMessages.appendChild(el);
    chatMessages.scrollTop = chatMessages.scrollHeight;
  }

  async function loadMessages() {
    activeTitle.textContent = 'Conversa: ' + activeSource;
    chatMessages.textContent = '';
    try {
      const res = await fetch(`/api/sources/${encodeURIComponent(CHANNEL)}/${encodeURIComponent(activeSource)}/messages?tenant=${TENANT}`);
      if (!res.ok) return;
      const data = await res.json();
      for (const m of (data.messages || [])) {
        renderBubble(m.direction, m.text, m.timestamp);
      }
    } catch (err) {
      console.error('Erro ao carregar mensagens:', err);
    }
  }

  async function loadSources() {
    try {
      const res = await fetch(`/api/sources?tenant=${TENANT}`);
      if (!res.ok) return;
      const data = await res.json();
      sourcesList.textContent = '';
      for (const s of (data.sources || [])) {
        const li = document.createElement('li');
        const btn = document.createElement('button');
        btn.type = 'button';
        btn.textContent = `${s.channel}:${s.source_key}`;
        if (s.channel === CHANNEL && s.source_key === activeSource) {
          btn.className = 'active';
        }
        btn.addEventListener('click', () => {
          activeSource = s.source_key;
          sessionInput.value = activeSource;
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

  function connectSSE() {
    if (sse) sse.close();
    const url = `/api/events?tenant=${encodeURIComponent(TENANT)}&channel=${encodeURIComponent(CHANNEL)}&source=${encodeURIComponent(activeSource)}`;
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
      renderBubble('outbound', (data.payload && data.payload.text) || '', data.at);
      sseStatus.textContent = 'SSE conectado';
    });

    sse.addEventListener('pipeline.failed', () => {
      sseStatus.textContent = 'Falha na análise (fallback seguro)';
    });
  }

  // IME-safe Enter-to-submit conforme modern-web-guidance
  chatInput.addEventListener('keydown', (event) => {
    if (event.key === 'Enter' && !event.shiftKey) {
      event.preventDefault();
      if (event.isComposing || event.keyCode === 229) {
        return;
      }
      chatForm.requestSubmit();
    }
  });

  chatForm.addEventListener('submit', async (event) => {
    event.preventDefault();
    const text = chatInput.value.trim();
    if (!text) return;

    activeSource = sessionInput.value.trim() || 'sim-user-1';
    sendBtn.disabled = true;
    try {
      const res = await fetch('/api/sim/messages', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          tenant_id: TENANT,
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

  sessionInput.addEventListener('change', () => {
    activeSource = sessionInput.value.trim() || 'sim-user-1';
    connectSSE();
    loadMessages();
    loadSources();
  });

  newSessionBtn.addEventListener('click', () => {
    activeSource = 'sim-' + Math.random().toString(36).slice(2, 8);
    sessionInput.value = activeSource;
    connectSSE();
    loadMessages();
    loadSources();
  });

  connectSSE();
  loadMessages();
  loadSources();
})();
