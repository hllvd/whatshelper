(function () {
  const tenantInput = document.getElementById('tenant-input');
  const sourcesList = document.getElementById('sources-list');
  const mermaidContainer = document.getElementById('mermaid-container');
  const mermaidRaw = document.getElementById('mermaid-raw');
  const validationBadge = document.getElementById('journey-validation-badge');
  const funnelSummary = document.getElementById('funnel-summary');
  const funnelStages = document.getElementById('funnel-stages');
  const funnelConversions = document.getElementById('funnel-conversions');
  const outboxFilter = document.getElementById('outbox-filter');
  const outboxTbody = document.getElementById('outbox-tbody');
  const whatifForm = document.getElementById('whatif-form');
  const whatifResult = document.getElementById('whatif-result').querySelector('code');

  let activeTenant = (tenantInput && tenantInput.value.trim()) || 'demo';
  let activeChannel = 'simulator';
  let activeSource = 'sim-user-1';

  if (window.mermaid) {
    window.mermaid.initialize({ startOnLoad: false, theme: 'neutral' });
  }

  async function loadSources() {
    const res = await fetch(`/api/sources?tenant=${encodeURIComponent(activeTenant)}`);
    if (!res.ok) return;
    const data = await res.json();
    const sources = data.sources || [];
    sourcesList.textContent = '';
    if (sources.length > 0 && !sources.some(s => s.channel === activeChannel && s.source_key === activeSource)) {
      activeChannel = sources[0].channel;
      activeSource = sources[0].source_key;
    }
    for (const s of sources) {
      const li = document.createElement('li');
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.textContent = `${s.channel}:${s.source_key}`;
      if (s.channel === activeChannel && s.source_key === activeSource) {
        btn.className = 'active';
      }
      btn.addEventListener('click', () => {
        activeChannel = s.channel;
        activeSource = s.source_key;
        loadJourney();
        loadSources();
      });
      li.appendChild(btn);
      sourcesList.appendChild(li);
    }
  }

  async function loadJourney() {
    const res = await fetch(`/api/sources/${encodeURIComponent(activeChannel)}/${encodeURIComponent(activeSource)}/journey?tenant=${encodeURIComponent(activeTenant)}`);
    if (!res.ok) return;
    const data = await res.json();

    const v = data.validation || {};
    validationBadge.textContent = v.valid
      ? `Válido (${v.pack || ''} v${v.version || '1'} · ${v.states_count || 0} estados)`
      : `Inválido/Ausente: ${v.message || ''}`;

    const code = data.mermaid || '';
    mermaidRaw.textContent = code;
    if (window.mermaid && code) {
      try {
        const id = 'mmd-' + Date.now();
        const rendered = await window.mermaid.render(id, code);
        mermaidContainer.innerHTML = rendered.svg;
      } catch (err) {
        mermaidContainer.textContent = code;
      }
    } else {
      mermaidContainer.textContent = code;
    }

    const wiState = document.getElementById('wi-state');
    if (wiState && !wiState.value) {
      wiState.value = (data.state && data.state.state) || (data.journey && data.journey.initial_state) || '';
    }
  }

  async function loadFunnelMetrics() {
    const res = await fetch(`/api/metrics/funnel?tenant=${encodeURIComponent(activeTenant)}`);
    if (!res.ok) return;
    const m = await res.json();
    funnelSummary.textContent = `Conversas: ${m.total_conversations || 0} · Estagnadas: ${m.stagnated_count || 0} · Abandonadas: ${m.abandoned_count || 0}`;

    funnelStages.textContent = '';
    for (const s of (m.stages || [])) {
      const tr = document.createElement('tr');
      tr.innerHTML = `<td>${s.stage}${s.terminal ? ' (terminal)' : ''}</td><td>${s.count}</td><td>${(s.avg_time_minutes || 0).toFixed(1)}</td>`;
      funnelStages.appendChild(tr);
    }

    funnelConversions.textContent = '';
    for (const c of (m.conversions || [])) {
      const tr = document.createElement('tr');
      tr.innerHTML = `<td>${c.from_state} -> ${c.to_state}</td><td>${c.count}</td><td>${(c.rate_pct || 0).toFixed(1)}%</td>`;
      funnelConversions.appendChild(tr);
    }
  }

  async function loadOutbox() {
    const status = outboxFilter.value;
    const res = await fetch(`/api/outbox?tenant=${encodeURIComponent(activeTenant)}&status=${encodeURIComponent(status)}`);
    if (!res.ok) return;
    const data = await res.json();
    outboxTbody.textContent = '';
    for (const it of (data.items || [])) {
      const tr = document.createElement('tr');
      const tdId = document.createElement('td'); tdId.textContent = it.id;
      const tdSrc = document.createElement('td'); tdSrc.textContent = `${it.channel}:${it.source_key}`;
      const tdAct = document.createElement('td'); tdAct.textContent = it.action_type;
      const tdSt = document.createElement('td'); tdSt.textContent = it.status;
      const tdAtt = document.createElement('td'); tdAtt.textContent = String(it.attempts || 0);
      const tdErr = document.createElement('td'); tdErr.textContent = it.last_error || '-';
      const tdBtn = document.createElement('td');
      if (it.status === 'failed' || it.status === 'dead' || it.status === 'pending') {
        const btn = document.createElement('button');
        btn.type = 'button';
        btn.className = 'btn-sm';
        btn.textContent = 'Reprocessar';
        btn.addEventListener('click', async () => {
          await fetch(`/api/outbox/${encodeURIComponent(it.channel)}/${encodeURIComponent(it.source_key)}/${encodeURIComponent(it.id)}/reprocess?tenant=${encodeURIComponent(activeTenant)}`, {
            method: 'POST',
          });
          loadOutbox();
        });
        tdBtn.appendChild(btn);
      }
      tr.append(tdId, tdSrc, tdAct, tdSt, tdAtt, tdErr, tdBtn);
      outboxTbody.appendChild(tr);
    }
  }

  whatifForm.addEventListener('submit', async (ev) => {
    ev.preventDefault();
    try {
      const facts = JSON.parse(document.getElementById('wi-facts').value || '{}');
      const signals = JSON.parse(document.getElementById('wi-signals').value || '{}');
      const dimensions = JSON.parse(document.getElementById('wi-dims').value || '{}');
      const res = await fetch('/api/journey/what-if', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          tenant_id: activeTenant,
          current_state: document.getElementById('wi-state').value.trim(),
          stagnation_count: Number(document.getElementById('wi-stagnation').value || 0),
          event_type: document.getElementById('wi-event').value,
          intent: document.getElementById('wi-intent').value.trim(),
          facts,
          signals,
          dimensions,
        }),
      });
      const data = await res.json();
      whatifResult.textContent = JSON.stringify(data, null, 2);
    } catch (err) {
      whatifResult.textContent = 'Erro: ' + err.message;
    }
  });

  document.getElementById('btn-refresh-outbox').addEventListener('click', loadOutbox);
  outboxFilter.addEventListener('change', loadOutbox);
  if (tenantInput) {
    tenantInput.addEventListener('change', () => {
      activeTenant = tenantInput.value.trim() || 'demo';
      loadSources().then(loadJourney);
      loadFunnelMetrics();
      loadOutbox();
    });
  }

  loadSources().then(loadJourney);
  loadFunnelMetrics();
  loadOutbox();
})();
