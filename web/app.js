// Alocal GUI Frontend — Vanilla JS, design-system driven
const API = '/api';
const app = {
  state: {
    models: [],
    localModels: [],
    hardware: null,
    selectedModel: null,
    activeSection: 'models',
    settings: { context: 8192, gpuLayers: -1, temperature: 0.7 },
    chatHistory: [],
  },

  init() {
    this.bindNav();
    this.bindSearch();
    this.bindChat();
    this.loadHardware();
    this.loadModels();
    this.loadLocalModels();
    this.loadSettings();
    this.checkFirstBoot();
    window.addEventListener('beforeunload', () => this.saveSettings());
  },

  /* ---------- Navigation ---------- */
  bindNav() {
    document.querySelectorAll('.nav-item').forEach(el => {
      el.addEventListener('click', () => {
        const section = el.dataset.section;
        this.switchSection(section);
      });
    });
  },

  switchSection(section) {
    document.querySelectorAll('.nav-item').forEach(el => {
      el.classList.toggle('active', el.dataset.section === section);
    });
    document.querySelectorAll('.section').forEach(el => {
      el.style.display = el.id === `section-${section}` ? 'block' : 'none';
    });
    this.state.activeSection = section;
    if (section === 'chat') this.refreshChatModels();
    if (section === 'hardware') this.loadHardware();
  },

  /* ---------- Models ---------- */
  bindSearch() {
    const search = document.getElementById('model-search');
    search.addEventListener('input', (e) => {
      clearTimeout(this.searchTimer);
      this.searchTimer = setTimeout(() => this.searchModels(e.target.value), 250);
    });
  },

  async loadModels() {
    try {
      const res = await fetch(`${API}/models`);
      const data = await res.json();
      this.state.models = data.models;
      this.renderModels(this.state.models);
    } catch (e) {
      this.toast('Failed to load models: ' + e.message);
    }
  },

  async searchModels(q) {
    try {
      const res = await fetch(`${API}/models/search?q=${encodeURIComponent(q)}`);
      const data = await res.json();
      this.renderModels(data.models);
    } catch (e) {
      this.toast('Search failed: ' + e.message);
    }
  },

  renderModels(models) {
    const grid = document.getElementById('model-grid');
    const empty = document.getElementById('empty-state');
    grid.innerHTML = '';
    if (models.length === 0) {
      empty.style.display = 'block';
      return;
    }
    empty.style.display = 'none';
    models.forEach(m => {
      const card = document.createElement('div');
      card.className = 'model-card';
      card.onclick = (e) => {
        if (e.target.closest('.download-row')) return;
        this.openModelDetail(m);
      };
      const repo = m.Runtimes ? m.Runtimes.LlamaCpp : m.ID;
      const file = this.guessFileName(m);
      const isDownloaded = this.state.localModels.some(lm => lm.ID === m.ID);
      card.innerHTML = `
        <h3>${this.escape(m.DisplayName || m.ID)}</h3>
        <div class="publisher">${this.escape(m.Family || 'unknown')}</div>
        <div class="meta">
          <span>${m.ParamsB ? m.ParamsB.toFixed(1) + 'B' : '—'}</span>
          <span>${m.FileSizeGB ? m.FileSizeGB.toFixed(2) + ' GB' : '—'}</span>
          <span>${m.MaxContext ? (m.MaxContext/1024) + 'K ctx' : '—'}</span>
        </div>
        <div class="fit-badge unknown">Unknown fit</div>
        <div class="download-row">
          ${isDownloaded
            ? `<button class="btn btn-primary" onclick="event.stopPropagation(); app.loadLocal('${m.ID}')">Load for Chat</button>`
            : `<button class="btn btn-primary" onclick="event.stopPropagation(); app.downloadModel('${this.escape(repo)}', '${this.escape(file)}')">Download</button>`
          }
        </div>
      `;
      grid.appendChild(card);
    });
  },

  guessFileName(m) {
    const base = (m.DisplayName || m.ID).toLowerCase().replace(/[^a-z0-9]+/g, '-');
    const quant = m.Quant || 'Q4_K_M';
    return base + '-' + quant + '.gguf';
  },

  async loadLocalModels() {
    try {
      const res = await fetch(`${API}/models/local`);
      const data = await res.json();
      this.state.localModels = data.models || [];
      const select = document.getElementById('chat-model-select');
      select.innerHTML = '<option value="">Select a model…</option>';
      (data.models || []).forEach(m => {
        const opt = document.createElement('option');
        opt.value = m.ID;
        opt.textContent = m.ID + ' (' + (m.SizeBytes ? (m.SizeBytes/1e9).toFixed(1) + ' GB' : '? GB') + ')';
        select.appendChild(opt);
      });
    } catch (e) {
      console.error(e);
    }
  },

  openModelDetail(m) {
    this.state.selectedModel = m;
    const body = document.getElementById('modal-body');
    const isDownloaded = this.state.localModels.some(lm => lm.ID === m.ID);
    const repo = m.Runtimes ? m.Runtimes.LlamaCpp : m.ID;
    body.innerHTML = `
      <div style="margin-bottom:16px">
        <div style="font-size:12px;color:var(--color-muted);text-transform:uppercase;letter-spacing:1px">Model</div>
        <div style="font-size:18px;font-weight:600">${this.escape(m.DisplayName || m.ID)}</div>
        <div style="font-size:13px;color:var(--color-muted)">${this.escape(repo)}</div>
      </div>
      <div style="display:grid;grid-template-columns:1fr 1fr;gap:12px;margin-bottom:16px">
        <div><div style="font-size:12px;color:var(--color-muted)">Family</div><div>${this.escape(m.Family || '—')}</div></div>
        <div><div style="font-size:12px;color:var(--color-muted)">Parameters</div><div>${m.ParamsB ? m.ParamsB.toFixed(1) + 'B' : '—'}</div></div>
        <div><div style="font-size:12px;color:var(--color-muted)">File Size</div><div>${m.FileSizeGB ? m.FileSizeGB.toFixed(2) + ' GB' : '—'}</div></div>
        <div><div style="font-size:12px;color:var(--color-muted)">Context</div><div>${m.MaxContext ? m.MaxContext + ' tokens' : '—'}</div></div>
      </div>
      <div style="margin-bottom:16px">
        <div style="font-size:12px;color:var(--color-muted)">Quantization</div>
        <div style="font-size:13px">${this.escape(m.Quant || '—')}</div>
      </div>
      <div style="margin-bottom:16px">
        <div style="font-size:12px;color:var(--color-muted)">License</div><div style="font-size:13px">${this.escape(m.License || '—')}</div>
      </div>
      <div style="margin-bottom:16px">
        <div style="font-size:12px;color:var(--color-muted)">Tasks</div><div style="font-size:13px">${(m.Tasks || []).join(', ') || '—'}</div>
      </div>
    `;
    const btn = document.getElementById('modal-download-btn');
    if (isDownloaded) {
      btn.textContent = 'Load for Chat';
      btn.onclick = () => { this.loadLocal(m.ID); this.closeModal(); };
    } else {
      btn.textContent = 'Download Model';
      btn.onclick = () => { this.downloadModel(repo, this.guessFileName(m)); this.closeModal(); };
    }
    document.getElementById('model-modal').style.display = 'flex';
  },

  closeModal() {
    document.getElementById('model-modal').style.display = 'none';
    this.state.selectedModel = null;
  },

  async downloadModel(repo, file) {
    try {
      const res = await fetch(`${API}/models/download`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ repo, file }),
      });
      const ds = await res.json();
      this.toast('Download started: ' + file);
      this.pollDownload(repo, file);
      this.loadModels();
      this.loadLocalModels();
    } catch (e) {
      this.toast('Download failed: ' + e.message);
    }
  },

  async pollDownload(repo, file) {
    const interval = setInterval(async () => {
      try {
        const res = await fetch(`${API}/models/download/status?repo=${encodeURIComponent(repo)}&file=${encodeURIComponent(file)}`);
        const ds = await res.json();
        if (ds.Status === 'completed') {
          clearInterval(interval);
          this.toast('Download complete: ' + ds.File);
          this.loadModels();
          this.loadLocalModels();
        } else if (ds.Status === 'error') {
          clearInterval(interval);
          this.toast('Download error: ' + (ds.Error || 'unknown'));
        }
      } catch (e) {
        clearInterval(interval);
      }
    }, 1000);
    setTimeout(() => clearInterval(interval), 300000);
  },

  async loadLocal(id) {
    try {
      const res = await fetch(`${API}/chat/load`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ model_id: id }),
      });
      if (!res.ok) throw new Error('load failed');
      const data = await res.json();
      this.toast('Loaded: ' + data.model);
      this.switchSection('chat');
    } catch (e) {
      this.toast('Failed to load model: ' + e.message);
    }
  },

  refreshModels() { this.loadModels(); },

  /* ---------- Hardware & Recommendations ---------- */
  async loadHardware() {
    try {
      const res = await fetch(`${API}/hardware`);
      const hw = await res.json();
      this.state.hardware = hw;
      const container = document.getElementById('hardware-data');
      const status = document.getElementById('hw-status');
      const dot = document.querySelector('#status-indicator .dot');
      const text = document.getElementById('status-text');
      status.textContent = 'Detected';
      dot.className = 'dot ready';
      text.textContent = 'Hardware ready';
      let html = '';
      if (hw.CPU && hw.CPU.Model) html += `<div class="hw-row"><span class="hw-label">CPU:</span><span>${this.escape(hw.CPU.Model)}</span></div>`;
      if (hw.RAM && hw.RAM.TotalGB) html += `<div class="hw-row"><span class="hw-label">RAM:</span><span>${hw.RAM.TotalGB.toFixed(1)} GB</span></div>`;
      if (hw.GPUs && hw.GPUs.length) {
        hw.GPUs.forEach((g, i) => {
          html += `<div class="hw-row"><span class="hw-label">GPU ${i+1}:</span><span>${this.escape(g.Name || g.Vendor)} ${g.VRAMTotalGB ? g.VRAMTotalGB + ' GB' : ''}</span></div>`;
        });
      }
      container.innerHTML = html;
    } catch (e) {
      document.getElementById('hw-status').textContent = 'Unavailable';
      document.querySelector('#status-indicator .dot').className = 'dot';
      document.getElementById('status-text').textContent = 'Hardware scan failed';
    }
  },

  async checkFirstBoot() {
    try {
      const res = await fetch(`${API}/models/local`);
      const data = await res.json();
      if ((data.count || 0) === 0) {
        setTimeout(() => this.loadRecommendation(), 1000);
      }
    } catch (e) {}
  },

  async loadRecommendation() {
    try {
      const res = await fetch(`${API}/recommend`);
      const data = await res.json();
      if (!data.Recommendations || data.Recommendations.length === 0) return;
      const rec = data.Recommendations[0];
      const body = document.getElementById('rec-body');
      body.innerHTML = `
        <div style="margin-bottom:16px">
          <div style="font-size:12px;color:var(--color-muted)">Based on your hardware (${data.Hardware})</div>
          <div style="font-size:18px;font-weight:600">${this.escape(rec.Display)}</div>
          <div style="font-size:13px;color:var(--color-muted)">${this.escape(rec.Repo)} — ${rec.File} (${rec.Quant})</div>
          <div style="font-size:12px;color:var(--color-muted);margin-top:8px">${rec.SizeGB.toFixed(1)} GB · Score ${rec.Score.toFixed(1)}</div>
        </div>
        <div style="background:rgba(124,58,237,0.1);border:1px solid var(--color-border);border-radius:8px;padding:12px;font-size:13px;color:var(--color-foreground)">
          ${this.escape(rec.Reason)}
        </div>
      `;
      document.getElementById('rec-modal').style.display = 'flex';
    } catch (e) {
      console.error(e);
    }
  },

  openRecommendation() {
    this.loadRecommendation();
  },

  closeRecModal() {
    document.getElementById('rec-modal').style.display = 'none';
  },

  async downloadRecommended() {
    this.closeRecModal();
    try {
      const res = await fetch(`${API}/recommend`);
      const data = await res.json();
      if (!data.Recommendations || data.Recommendations.length === 0) {
        this.toast('No recommendations available');
        return;
      }
      const rec = data.Recommendations[0];
      if (!rec.Repo || !rec.File) {
        this.toast('No downloadable file for recommended model');
        return;
      }
      this.downloadModel(rec.Repo, rec.File);
      setTimeout(() => this.switchSection('chat'), 1500);
    } catch (e) {
      this.toast('Failed to get recommendation: ' + e.message);
    }
  },

  /* ---------- Chat ---------- */
  bindChat() {
    const input = document.getElementById('chat-input');
    input.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault();
        this.chatSend();
      }
    });
    input.addEventListener('input', () => {
      input.style.height = 'auto';
      input.style.height = input.scrollHeight + 'px';
    });
    document.getElementById('chat-model-select').addEventListener('change', (e) => {
      if (e.target.value) this.loadLocal(e.target.value);
    });
  },

  async refreshChatModels() {
    await this.loadLocalModels();
  },

  async chatSend() {
    const input = document.getElementById('chat-input');
    const text = input.value.trim();
    if (!text) return;
    const modelSelect = document.getElementById('chat-model-select');
    const modelId = modelSelect.value;

    this.addMessage('user', text);
    input.value = '';
    input.style.height = 'auto';

    const sysMsg = document.getElementById('welcome-msg');
    if (sysMsg) sysMsg.style.display = 'none';

    const assistant = this.addMessage('assistant', '', true);

    try {
      const res = await fetch(`${API}/chat/stream`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          message: text,
          model_id: modelId || undefined,
          system: '',
          temperature: parseFloat(document.getElementById('setting-temperature')?.value || 0.7),
        }),
      });

      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      let buffer = '';

      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true });
        const lines = buffer.split('\n');
        buffer = lines.pop() || '';
        for (const line of lines) {
          if (line.startsWith('data: ')) {
            try {
              const payload = JSON.parse(line.slice(6));
              if (payload.tokens !== undefined) {
                this.toast('Complete — ' + payload.tokens + ' tokens');
              }
            } catch (_) {}
          } else if (line.startsWith('data:') && !line.startsWith('data: {')) {
            const content = line.slice(5).replace(/\\n/g, '\n');
            assistant.querySelector('.msg-content').textContent += content;
            assistant.scrollTop = assistant.scrollHeight;
          }
        }
      }
    } catch (e) {
      this.toast('Chat error: ' + e.message);
    }
  },

  addMessage(role, content, streaming = false) {
    const container = document.getElementById('chat-messages');
    const div = document.createElement('div');
    div.className = 'msg msg-' + role;
    const inner = document.createElement('div');
    inner.className = 'msg-content';
    inner.textContent = content;
    div.appendChild(inner);
    if (streaming) {
      const cur = document.createElement('span');
      cur.className = 'cursor';
      div.appendChild(cur);
    }
    container.appendChild(div);
    container.scrollTop = container.scrollHeight;
    return div;
  },

  /* ---------- Settings ---------- */
  loadSettings() {
    try {
      const raw = localStorage.getItem('alocal-settings');
      if (raw) this.state.settings = JSON.parse(raw);
      document.getElementById('setting-context').value = this.state.settings.context;
      document.getElementById('setting-gpu-layers').value = this.state.settings.gpuLayers;
      document.getElementById('setting-temperature').value = this.state.settings.temperature;
    } catch (_) {}
  },

  saveSettings() {
    this.state.settings = {
      context: parseInt(document.getElementById('setting-context').value || 8192),
      gpuLayers: parseInt(document.getElementById('setting-gpu-layers').value || -1),
      temperature: parseFloat(document.getElementById('setting-temperature').value || 0.7),
    };
    try { localStorage.setItem('alocal-settings', JSON.stringify(this.state.settings)); } catch (_) {}
  },

  /* ---------- Utilities ---------- */
  escape(s) {
    const div = document.createElement('div');
    div.textContent = s ?? '';
    return div.innerHTML;
  },

  toast(msg) {
    const t = document.getElementById('toast');
    t.textContent = msg;
    t.classList.add('show');
    setTimeout(() => t.classList.remove('show'), 3000);
  },
};

document.addEventListener('DOMContentLoaded', () => app.init());
