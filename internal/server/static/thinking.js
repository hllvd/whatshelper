(function () {
  const sourcesList = document.getElementById('sources-list');
  const stateSummary = document.getElementById('state-summary');
  const stateFacts = document.getElementById('state-facts');
  const stateSignals = document.getElementById('state-signals');
  const timeline = document.getElementById('timeline');
  const llmConfigCard = document.getElementById('llm-config-card');
  const journeyCard = document.getElementById('journey-card');
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

    if (trace.analysis_truncation || trace.reply_truncation) {
      const aT = trace.analysis_truncation;
      const rT = trace.reply_truncation;
      const fmt = (label, t) => t ? `${label}: ${t.used_tokens}/${t.budget_tokens} tok${t.truncated ? ` (cortados: ${(t.dropped_items || []).map(d => d.block + ':' + d.item_id).join(', ')})` : ''}` : '';
      const lines = [fmt('Análise', aT), fmt('Resposta', rT)].filter(Boolean).join('\n');
      card.appendChild(makeDetails('Orçamento de Contexto (CTX-05)', lines));
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
        loadJourneyCard();
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
          loadJourneyCard();
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
        loadJourneyCard();
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

  async function loadJourneyCard() {
    if (!journeyCard) return;
    try {
      const res = await fetch(`/api/sources/${encodeURIComponent(activeChannel)}/${encodeURIComponent(activeSource)}/journey?tenant=${encodeURIComponent(TENANT)}`);
      if (!res.ok) return;
      const data = await res.json();
      journeyCard.textContent = '';

      const hdr = document.createElement('div');
      hdr.className = 'run-header';
      const st = data.state || {};
      const left = document.createElement('strong');
      left.textContent = `Jornada (FSM) · Estado atual: ${st.state || (data.journey && data.journey.initial_state) || '-'}`;
      const right = document.createElement('span');
      right.textContent = `Estagnação: ${st.stagnation_count || 0} · Turnos no estado: ${st.turns_in_state || 0}`;
      hdr.appendChild(left);
      hdr.appendChild(right);
      journeyCard.appendChild(hdr);

      if (st.dimensions && Object.keys(st.dimensions).length > 0) {
        const dims = document.createElement('div');
        dims.className = 'metrics-row';
        dims.textContent = 'Dimensões: ' + Object.entries(st.dimensions).map(([k, v]) => `${k}=${v}`).join(' · ');
        journeyCard.appendChild(dims);
      }

      const transitions = data.transitions || [];
      if (transitions.length > 0) {
        const trLines = transitions.slice(0, 5).map(t =>
          `${t.from_state || '-'} -> ${t.to_state} (${t.event_type}) · regra=${t.rule_id || '-'} · ${t.reason || ''}`
        ).join('\n');
        journeyCard.appendChild(makeDetails(`Transições recentes (${transitions.length})`, trLines));
      }

      const outbox = data.outbox || [];
      if (outbox.length > 0) {
        const obLines = outbox.slice(0, 8).map(o =>
          `[${o.status}] ${o.action_type} (${o.id}) · tentativas=${o.attempts}${o.last_error ? ' · erro: ' + o.last_error : ''}`
        ).join('\n');
        journeyCard.appendChild(makeDetails(`Outbox & Ações (${outbox.length})`, obLines));
      }
    } catch (err) {
      console.error('Erro ao carregar jornada:', err);
    }
  }

  async function loadLLMConfigCard() {
    if (!llmConfigCard) return;
    try {
      const [presetsRes, cfgRes] = await Promise.all([
        fetch('/api/llm/presets'),
        fetch(`/api/llm/config?tenant=${encodeURIComponent(TENANT)}`)
      ]);
      if (!presetsRes.ok || !cfgRes.ok) return;
      const presetsData = await presetsRes.json();
      const cfgData = await cfgRes.json();

      const activeProviders = presetsData.active_providers || [];
      const presets = presetsData.presets || [];
      const effective = cfgData.effective || {};
      const stages = [
        { key: 'analysis', label: 'Análise / Thinking' },
        { key: 'response', label: 'Resposta' },
        { key: 'nudge', label: 'Nudge (Follow-up)' },
        { key: 'vision', label: 'Visão (Imagens)' },
        { key: 'audio', label: 'Áudio (Transcrição)' }
      ];
      const effortOptions = [
        { value: '', label: 'Effort: padrão' },
        { value: 'none', label: 'Effort: none (sem thinking)' },
        { value: 'minimal', label: 'Effort: minimal' },
        { value: 'low', label: 'Effort: low' },
        { value: 'medium', label: 'Effort: medium' },
        { value: 'high', label: 'Effort: high' }
      ];

      llmConfigCard.textContent = '';
      const hdr = document.createElement('div');
      hdr.className = 'run-header';
      const title = document.createElement('strong');
      title.textContent = 'Modelos LLM (On the Fly)';
      const statusSpan = document.createElement('span');
      statusSpan.style.fontSize = '0.82rem';
      statusSpan.textContent = `Provedor padrão: ${cfgData.default_provider || '-'}`;
      hdr.appendChild(title);
      hdr.appendChild(statusSpan);
      llmConfigCard.appendChild(hdr);

      const grid = document.createElement('div');
      grid.style.display = 'grid';
      grid.style.gridTemplateColumns = 'repeat(auto-fit, minmax(220px, 1fr))';
      grid.style.gap = '0.5rem';
      grid.style.marginTop = '0.5rem';

      const controls = {};
      stages.forEach(st => {
        const box = document.createElement('div');
        box.style.display = 'flex';
        box.style.flexDirection = 'column';
        box.style.gap = '0.25rem';

        const lbl = document.createElement('label');
        lbl.textContent = st.label;
        lbl.style.fontSize = '0.8rem';
        lbl.style.fontWeight = '600';

        const provSel = document.createElement('select');
        activeProviders.forEach(p => {
          const opt = document.createElement('option');
          opt.value = p;
          opt.textContent = p;
          provSel.appendChild(opt);
        });
        const cur = effective[st.key] || {};
        if (cur.provider) provSel.value = cur.provider;

        const listId = `llm-presets-${st.key}`;
        const dl = document.createElement('datalist');
        dl.id = listId;
        const populatePresets = () => {
          dl.textContent = '';
          presets.filter(p => p.provider === provSel.value).forEach(p => {
            const opt = document.createElement('option');
            opt.value = p.model;
            opt.label = p.label;
            dl.appendChild(opt);
          });
        };
        populatePresets();
        provSel.addEventListener('change', populatePresets);

        const modelInput = document.createElement('input');
        modelInput.type = 'text';
        modelInput.setAttribute('list', listId);
        modelInput.value = cur.model || '';
        modelInput.placeholder = 'Modelo (preset ou custom)';

        const effortSel = document.createElement('select');
        effortOptions.forEach(eo => {
          const opt = document.createElement('option');
          opt.value = eo.value;
          opt.textContent = eo.label;
          effortSel.appendChild(opt);
        });
        effortSel.value = cur.reasoning_effort || '';

        box.appendChild(lbl);
        box.appendChild(provSel);
        box.appendChild(modelInput);
        box.appendChild(effortSel);
        box.appendChild(dl);
        grid.appendChild(box);
        controls[st.key] = { provSel, modelInput, effortSel };
      });
      llmConfigCard.appendChild(grid);

      const actions = document.createElement('div');
      actions.style.display = 'flex';
      actions.style.alignItems = 'center';
      actions.style.gap = '0.6rem';
      actions.style.marginTop = '0.6rem';

      const saveBtn = document.createElement('button');
      saveBtn.type = 'button';
      saveBtn.textContent = 'Aplicar Modelos';
      const testBtn = document.createElement('button');
      testBtn.type = 'button';
      testBtn.textContent = 'Testar LLM';
      const feedback = document.createElement('span');
      feedback.style.fontSize = '0.82rem';

      saveBtn.addEventListener('click', async () => {
        const overrides = {};
        stages.forEach(st => {
          overrides[st.key] = {
            provider: controls[st.key].provSel.value,
            model: controls[st.key].modelInput.value.trim(),
            reasoning_effort: controls[st.key].effortSel.value
          };
        });
        saveBtn.disabled = true;
        feedback.textContent = 'Salvando...';
        try {
          const putRes = await fetch(`/api/llm/config?tenant=${encodeURIComponent(TENANT)}`, {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ overrides })
          });
          if (!putRes.ok) {
            const errJson = await putRes.json().catch(() => ({}));
            feedback.textContent = errJson.error || 'Erro ao salvar';
          } else {
            feedback.textContent = 'Modelos atualizados em tempo real!';
          }
        } catch (e) {
          feedback.textContent = 'Falha de rede ao salvar';
        } finally {
          saveBtn.disabled = false;
        }
      });

      testBtn.addEventListener('click', async () => {
        const targetCtrl = controls.response || controls.analysis;
        const provider = targetCtrl ? targetCtrl.provSel.value : '';
        const model = targetCtrl ? targetCtrl.modelInput.value.trim() : '';
        const reasoningEffort = targetCtrl ? targetCtrl.effortSel.value : '';
        testBtn.disabled = true;
        feedback.style.color = '';
        feedback.textContent = `Testando ${provider || 'LLM'}${model ? ' (' + model + ')' : ''}...`;
        try {
          const res = await fetch('/api/llm/test', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
              tenant: TENANT,
              stage: 'response',
              provider,
              model,
              reasoning_effort: reasoningEffort
            })
          });
          const data = await res.json().catch(() => ({}));
          if (!res.ok) {
            feedback.style.color = '#dc2626';
            feedback.textContent = `❌ ${data.error || 'Erro HTTP ' + res.status}`;
            return;
          }
          if (data.ok) {
            feedback.style.color = '#16a34a';
            const snippet = (data.content || '').trim().slice(0, 80);
            feedback.textContent = `✅ OK (${data.provider}/${data.model} · ${data.duration_ms}ms): "${snippet}"`;
          } else {
            feedback.style.color = '#dc2626';
            feedback.textContent = `❌ Falhou (${data.provider || '-'}/${data.model || 'sem modelo'} · ${data.duration_ms}ms): ${data.error || 'erro desconhecido'}`;
          }
        } catch (e) {
          feedback.style.color = '#dc2626';
          feedback.textContent = '❌ Falha de rede ao testar LLM';
        } finally {
          testBtn.disabled = false;
        }
      });

      actions.appendChild(saveBtn);
      actions.appendChild(testBtn);
      actions.appendChild(feedback);
      llmConfigCard.appendChild(actions);
    } catch (err) {
      console.error('Erro ao carregar modelos LLM:', err);
    }
  }

  connectSSE();
  loadSources();
  loadState();
  loadLLMConfigCard();
  loadJourneyCard();
  loadTimeline();
})();
