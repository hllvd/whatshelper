(function () {
  const sourcesList = document.getElementById('sources-list');
  const stateSummary = document.getElementById('state-summary');
  const stateFacts = document.getElementById('state-facts');
  const stateSignals = document.getElementById('state-signals');
  const timeline = document.getElementById('timeline');
  const thinkingTitle = document.getElementById('thinking-title');
  const sseStatus = document.getElementById('sse-status');

  const TENANT = 'demo';
  let activeChannel = 'simulator';
  let activeSource = 'sim-user-1';
  let sse = null;

  function makeDetails(title, content) {
    const details = document.createElement('details');
    const summary = document.createElement('summary');
    summary.textContent = title;
    details.appendChild(summary);

    const pre = document.createElement('pre');
    pre.setAttribute('role', 'region');
    pre.setAttribute('aria-label', title);
    pre.setAttribute('tabindex', '0');

    const code = document.createElement('code');
    code.textContent = content || '(vazio)';
    pre.appendChild(code);
    details.appendChild(pre);
    return details;
  }

  function renderRunCard(trace) {
    const card = document.createElement('article');
    card.className = 'run-card';

    const header = document.createElement('div');
    header.className = 'run-header';
    const left = document.createElement('span');
    left.textContent = `Run ${trace.run_id} · Msg ${trace.message_id || '-'}`;
    const right = document.createElement('span');
    right.textContent = trace.created_at ? new Date(trace.created_at).toLocaleTimeString('pt-BR') : '';
    header.appendChild(left);
    header.appendChild(right);
    card.appendChild(header);

    const metrics = document.createElement('div');
    metrics.className = 'metrics-row';
    const aTokens = trace.analysis ? (trace.analysis.prompt_tokens + trace.analysis.completion_tokens) : 0;
    const rTokens = trace.reply ? (trace.reply.prompt_tokens + trace.reply.completion_tokens) : 0;
    metrics.textContent = `Pack: ${trace.pack_name || '-'} · Total tokens: ${trace.total_tokens || (aTokens + rTokens)} · Tempo total: ${trace.total_duration_ms || 0} ms`;
    card.appendChild(metrics);

    if (trace.facts_diff && trace.facts_diff.length > 0) {
      const ul = document.createElement('ul');
      ul.className = 'diff-list';
      for (const d of trace.facts_diff) {
        const li = document.createElement('li');
        li.className = 'diff-item diff-' + d.action;
        if (d.action === 'added') {
          li.textContent = `+ ${d.key}: ${d.new_value}`;
        } else if (d.action === 'updated') {
          li.textContent = `~ ${d.key}: ${d.old_value} -> ${d.new_value}`;
        } else {
          li.textContent = `- ${d.key}: ${d.old_value}`;
        }
        ul.appendChild(li);
      }
      card.appendChild(ul);
    }

    if (trace.analysis) {
      const a = trace.analysis;
      card.appendChild(makeDetails(
        `Análise (${a.status} · ${a.model} · ${a.duration_ms} ms · ${a.prompt_tokens}+${a.completion_tokens} tok)`,
        `=== SYSTEM ===\n${a.system_prompt}\n\n=== USER ===\n${a.user_prompt}`
      ));
      card.appendChild(makeDetails('Análise — Resposta Crua do LLM', a.raw_output));
      if (a.repair_raw_output) {
        card.appendChild(makeDetails('Análise — Tentativa de Repair', `${a.repair_prompt}\n\n=== RESPOSTA ===\n${a.repair_raw_output}`));
      }
    }

    if (trace.parsed_result) {
      card.appendChild(makeDetails('JSON Parseado e Validado', JSON.stringify(trace.parsed_result, null, 2)));
    }

    if (trace.reply) {
      const r = trace.reply;
      card.appendChild(makeDetails(
        `Resposta (${r.model} · ${r.duration_ms} ms · ${r.prompt_tokens}+${r.completion_tokens} tok)`,
        `=== SYSTEM ===\n${r.system_prompt}\n\n=== USER ===\n${r.user_prompt}`
      ));
      card.appendChild(makeDetails('Resposta Crua vs Final Sanitizada', `CRUA:\n${r.raw_output}\n\nFINAL:\n${trace.final_reply || r.raw_output}`));
    }

    return card;
  }

  async function loadState() {
    try {
      const res = await fetch(`/api/sources/${encodeURIComponent(activeChannel)}/${encodeURIComponent(activeSource)}/state?tenant=${TENANT}`);
      if (!res.ok) return;
      const st = await res.json();

      stateSummary.textContent = (st.summary && st.summary.text) ? st.summary.text : '(sem resumo)';

      stateFacts.textContent = '';
      for (const [k, v] of Object.entries(st.facts || {}).sort()) {
        const tr = document.createElement('tr');
        const tdK = document.createElement('td');
        tdK.textContent = k;
        const tdV = document.createElement('td');
        tdV.textContent = v;
        tr.appendChild(tdK);
        tr.appendChild(tdV);
        stateFacts.appendChild(tr);
      }

      stateSignals.textContent = '';
      for (const [k, v] of Object.entries(st.signals || {}).sort()) {
        const tr = document.createElement('tr');
        const tdK = document.createElement('td');
        tdK.textContent = k;
        const tdV = document.createElement('td');
        tdV.textContent = String(v);
        tr.appendChild(tdK);
        tr.appendChild(tdV);
        stateSignals.appendChild(tr);
      }
    } catch (err) {
      console.error('Erro ao carregar estado:', err);
    }
  }

  async function loadTimeline() {
    thinkingTitle.textContent = `Pensando: ${activeChannel}:${activeSource}`;
    try {
      const res = await fetch(`/api/sources/${encodeURIComponent(activeChannel)}/${encodeURIComponent(activeSource)}/runs?tenant=${TENANT}`);
      if (!res.ok) return;
      const data = await res.json();
      const runs = data.runs || [];

      timeline.textContent = '';
      if (runs.length === 0) {
        const empty = document.createElement('p');
        empty.textContent = 'Nenhum processamento registrado ainda para esta conversa.';
        timeline.appendChild(empty);
        return;
      }

      for (const run of runs) {
        if (!run.trace_blob_ref) continue;
        const trRes = await fetch(`/api/runs/${encodeURIComponent(run.id)}/trace?ref=${encodeURIComponent(run.trace_blob_ref)}`);
        if (!trRes.ok) continue;
        const trace = await trRes.json();
        timeline.appendChild(renderRunCard(trace));
      }
    } catch (err) {
      console.error('Erro ao carregar traces:', err);
    }
  }

  async function loadSources() {
    try {
      const res = await fetch(`/api/sources?tenant=${TENANT}`);
      if (!res.ok) return;
      const data = await res.json();
      const sources = data.sources || [];
      sourcesList.textContent = '';

      if (sources.length > 0 && !sources.some((s) => s.channel === activeChannel && s.source_key === activeSource)) {
        activeChannel = sources[0].channel;
        activeSource = sources[0].source_key;
        connectSSE();
        loadState();
        loadTimeline();
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
          connectSSE();
          loadState();
          loadTimeline();
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
    const url = `/api/events?tenant=${encodeURIComponent(TENANT)}`;
    sse = new EventSource(url);

    sse.onopen = () => {
      sseStatus.textContent = 'SSE ao vivo';
    };
    sse.onerror = () => {
      sseStatus.textContent = 'SSE reconectando...';
    };

    const refreshIfMatch = (ev) => {
      const data = JSON.parse(ev.data);
      if (data.channel === activeChannel && data.source_key === activeSource) {
        loadState();
        loadTimeline();
      }
      loadSources();
    };

    sse.addEventListener('reply.generated', refreshIfMatch);
    sse.addEventListener('pipeline.failed', refreshIfMatch);
    sse.addEventListener('analysis.started', (ev) => {
      const data = JSON.parse(ev.data);
      if (data.channel === activeChannel && data.source_key === activeSource) {
        sseStatus.textContent = 'Pensando...';
      }
    });
    sse.addEventListener('reply.sent', () => {
      sseStatus.textContent = 'SSE ao vivo';
    });
  }

  connectSSE();
  loadSources();
  loadState();
  loadTimeline();
})();
