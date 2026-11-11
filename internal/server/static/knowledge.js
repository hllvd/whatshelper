(function () {
  const tenantInput = document.getElementById('knowledge-tenant');
  const reloadBtn = document.getElementById('reload-knowledge-btn');
  const filesList = document.getElementById('files-list');
  const newFilenameInput = document.getElementById('new-filename');
  const createFileBtn = document.getElementById('create-file-btn');
  const activeFileTitle = document.getElementById('active-file-title');
  const fileEditor = document.getElementById('file-editor');
  const saveFileBtn = document.getElementById('save-file-btn');
  const saveStatus = document.getElementById('save-status');
  const versionsList = document.getElementById('versions-list');

  const policyForm = document.getElementById('policy-form');
  const policyBotEnabled = document.getElementById('policy-bot-enabled');
  const policyProactive = document.getElementById('policy-proactive');
  const policyMode = document.getElementById('policy-mode');
  const policyStatus = document.getElementById('policy-status');

  let activeTenant = tenantInput.value.trim() || 'demo';
  let activeFile = '';
  let cachedPolicy = null;

  async function loadFiles() {
    const res = await fetch(`/api/knowledge?tenant=${encodeURIComponent(activeTenant)}`);
    if (!res.ok) return;
    const data = await res.json();
    const files = data.files || [];
    filesList.textContent = '';

    for (const f of files) {
      const li = document.createElement('li');
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.textContent = f.is_qa ? `${f.name} (Aprendido)` : f.name;
      if (f.name === activeFile) btn.className = 'active';
      btn.addEventListener('click', () => selectFile(f.name));
      li.appendChild(btn);
      filesList.appendChild(li);
    }

    if (!activeFile && files.length > 0) {
      await selectFile(files[0].name);
    }
  }

  async function selectFile(name) {
    activeFile = name;
    activeFileTitle.textContent = `${activeTenant}/knowledge/${name}`;
    const res = await fetch(`/api/knowledge/${encodeURIComponent(name)}?tenant=${encodeURIComponent(activeTenant)}`);
    if (res.ok) {
      const f = await res.json();
      fileEditor.value = f.content || '';
    } else {
      fileEditor.value = '';
    }
    saveStatus.textContent = 'Pronto';
    await loadVersions(name);
    await loadFiles();
  }

  async function loadVersions(name) {
    versionsList.textContent = '';
    const res = await fetch(`/api/knowledge/${encodeURIComponent(name)}/versions?tenant=${encodeURIComponent(activeTenant)}`);
    if (!res.ok) return;
    const data = await res.json();
    const versions = data.versions || [];
    if (versions.length === 0) {
      const p = document.createElement('p');
      p.style.fontSize = '0.82rem';
      p.textContent = 'Sem histórico de versões ainda.';
      versionsList.appendChild(p);
      return;
    }

    for (const v of versions) {
      const row = document.createElement('div');
      row.className = 'version-item';
      const label = document.createElement('span');
      const dateStr = v.last_modified ? new Date(v.last_modified).toLocaleString('pt-BR') : '';
      label.textContent = `${v.version_id}${v.is_latest ? ' (atual)' : ''} · ${v.size} bytes · ${dateStr}`;
      row.appendChild(label);

      const loadBtn = document.createElement('button');
      loadBtn.type = 'button';
      loadBtn.className = 'btn-sm';
      loadBtn.textContent = 'Carregar versão';
      loadBtn.addEventListener('click', async () => {
        const vr = await fetch(`/api/knowledge/${encodeURIComponent(name)}?tenant=${encodeURIComponent(activeTenant)}&version_id=${encodeURIComponent(v.version_id)}`);
        if (vr.ok) {
          const doc = await vr.json();
          fileEditor.value = doc.content || '';
          saveStatus.textContent = `Versão ${v.version_id} carregada (salve para restaurar)`;
        }
      });
      row.appendChild(loadBtn);
      versionsList.appendChild(row);
    }
  }

  async function loadPolicy() {
    const res = await fetch(`/api/tenants/${encodeURIComponent(activeTenant)}/policy`);
    if (!res.ok) return;
    cachedPolicy = await res.json();
    policyBotEnabled.checked = !!cachedPolicy.bot_enabled;
    policyProactive.checked = !!cachedPolicy.proactive_enabled;
    policyMode.value = cachedPolicy.mode || 'auto';
  }

  saveFileBtn.addEventListener('click', async () => {
    if (!activeFile) return;
    saveStatus.textContent = 'Salvando...';
    const res = await fetch(`/api/knowledge/${encodeURIComponent(activeFile)}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        tenant_id: activeTenant,
        content: fileEditor.value,
      }),
    });
    if (res.ok) {
      saveStatus.textContent = 'Salvo e cache invalidado';
      await loadVersions(activeFile);
    } else {
      saveStatus.textContent = 'Erro ao salvar';
    }
  });

  createFileBtn.addEventListener('click', async () => {
    let name = newFilenameInput.value.trim();
    if (!name) return;
    if (!name.toLowerCase().endsWith('.md')) name += '.md';
    newFilenameInput.value = '';
    activeFile = name;
    activeFileTitle.textContent = `${activeTenant}/knowledge/${name}`;
    fileEditor.value = '';
    saveStatus.textContent = 'Novo documento (salve no MinIO)';
  });

  policyForm.addEventListener('submit', async (ev) => {
    ev.preventDefault();
    if (!cachedPolicy) return;
    const updated = Object.assign({}, cachedPolicy, {
      bot_enabled: policyBotEnabled.checked,
      proactive_enabled: policyProactive.checked,
      mode: policyMode.value,
    });
    const res = await fetch(`/api/tenants/${encodeURIComponent(activeTenant)}/policy`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(updated),
    });
    if (res.ok) {
      cachedPolicy = await res.json();
      policyStatus.textContent = 'Política salva.';
    } else {
      policyStatus.textContent = 'Erro ao salvar política.';
    }
  });

  reloadBtn.addEventListener('click', () => {
    activeTenant = tenantInput.value.trim() || 'demo';
    activeFile = '';
    loadFiles();
    loadPolicy();
  });

  loadFiles();
  loadPolicy();
})();
