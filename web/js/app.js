import { api } from './api.js';
import { socket } from './ws.js';
import { analyzeSmsText } from './utils/gsm.js';
import { renderQRCode } from './utils/qr.js';

class GatewayDashboard {
  constructor() {
    this.currentView = 'feed';
    this.messages = [];
    this.devices = [];
    this.webhooks = [];
    this.tokens = [];
    this.selectedMessage = null;
    this.activeSearch = '';
    this.activeDirectionFilter = '';
    this.pendingPairDeviceId = null;
  }

  init() {
    this.bindNavigation();
    this.bindModals();
    this.bindInspector();
    this.bindComposer();
    this.bindWebSocket();
    this.loadInitialData();

    window.addEventListener('auth:required', () => this.openSettingsModal(true));

    // Setup periodic polling fallback (every 30s)
    setInterval(() => this.refreshTelemetry(), 30000);
  }

  bindNavigation() {
    document.querySelectorAll('.nav-item button').forEach(btn => {
      btn.addEventListener('click', (e) => {
        const view = btn.dataset.view;
        this.switchView(view);
      });
    });

    // Master token settings modal trigger
    document.getElementById('btn-open-settings')?.addEventListener('click', () => {
      this.openSettingsModal();
    });
  }

  switchView(view) {
    this.currentView = view;
    document.querySelectorAll('.nav-item').forEach(item => {
      item.classList.toggle('active', item.querySelector('button')?.dataset.view === view);
    });

    document.querySelectorAll('.view-section').forEach(sec => {
      sec.style.display = sec.id === `view-${view}` ? 'block' : 'none';
    });

    // Update Header Title
    const titles = {
      feed: 'Message Stream & Inbound Telemetry',
      composer: 'SMS Dispatcher Console',
      devices: 'Android Gateway Nodes & SIM Slots',
      webhooks: 'Webhooks & Event Dispatches',
      tokens: 'API Token Authorization Keys',
      ota: 'OTA Release Center & Updates'
    };
    const titleEl = document.getElementById('view-title');
    if (titleEl) titleEl.textContent = titles[view] || 'Console';

    // Load data for selected view
    switch (view) {
      case 'feed':
        this.loadMessages();
        break;
      case 'composer':
        this.refreshDeviceSelects();
        break;
      case 'devices':
        this.loadDevices();
        break;
      case 'webhooks':
        this.loadWebhooks();
        this.loadDeliveries();
        break;
      case 'tokens':
        this.loadTokens();
        break;
      case 'ota':
        this.loadOtaInfo();
        break;
    }
  }

  bindWebSocket() {
    socket.connect();

    const connDot = document.getElementById('conn-dot');
    const connStatus = document.getElementById('conn-status-text');
    const latencyEl = document.getElementById('conn-latency');

    socket.onStatusChange((isOnline, latency) => {
      if (connDot) {
        connDot.className = isOnline ? 'pulse-dot' : 'pulse-dot offline';
      }
      if (connStatus) {
        connStatus.textContent = isOnline ? 'Gateway Connected' : 'Disconnected';
      }
      if (latencyEl) {
        if (!isOnline) latencyEl.textContent = '--';
      }
    });

    // Measure real HTTP round-trip time to the gateway server.
    const measureLatency = async () => {
      const latency = await api.measureLatency();
      if (latencyEl) latencyEl.textContent = latency === null ? '--' : `${latency}ms`;
    };
    measureLatency();
    setInterval(measureLatency, 10000);

    // Live Inbound SMS Event
    socket.on('sms.received', (msg) => {
      this.prependMessageRow(msg);
      this.refreshTelemetry();
      this.showToast(`Incoming SMS from ${msg.sender}`, 'info');
    });

    // SMS Status Update Event
    socket.on('sms.status', (data) => {
      this.updateMessageRowStatus(data.message_id, data.status, data.error);
    });

    // Device Heartbeat Event
    socket.on('device.heartbeat', (device) => {
      this.updateDeviceInList(device);
      this.refreshTelemetry();
      if (device && device.id === this.pendingPairDeviceId) this.markPairingConnected();
    });

    // Device Status Change
    socket.on('device.status', (data) => {
      this.loadDevices();
      this.refreshTelemetry();
      if (data && data.status === 'online' && data.device_id === this.pendingPairDeviceId) {
        this.markPairingConnected();
      }
    });
  }

  async loadInitialData() {
    await this.refreshTelemetry();
    this.switchView('feed');
  }

  async refreshTelemetry() {
    try {
      const stats = await api.getSystemStats();
      document.getElementById('stat-online-devices').textContent = stats.online_devices;
      document.getElementById('stat-inbound-count').textContent = stats.total_inbound;
      document.getElementById('stat-outbound-count').textContent = stats.total_outbound;
      document.getElementById('stat-webhooks-count').textContent = stats.active_webhooks;
    } catch (e) {
      console.warn('Telemetry fetch error:', e);
    }
  }

  // --- MESSAGES FEED ---
  async loadMessages() {
    const tbody = document.getElementById('messages-table-body');
    if (!tbody) return;
    tbody.innerHTML = `<tr><td colspan="6" style="text-align:center; padding: 24px; color: var(--text-muted);">Loading message stream...</td></tr>`;

    try {
      const resp = await api.listMessages({
        direction: this.activeDirectionFilter,
        search: this.activeSearch,
        limit: 50,
      });

      this.messages = resp.data || [];
      if (this.messages.length === 0) {
        tbody.innerHTML = `<tr><td colspan="6" style="text-align:center; padding: 32px; color: var(--text-muted);">No messages found.</td></tr>`;
        return;
      }

      tbody.innerHTML = '';
      this.messages.forEach(msg => {
        tbody.appendChild(this.createMessageRow(msg));
      });
    } catch (e) {
      tbody.innerHTML = `<tr><td colspan="6" style="text-align:center; padding: 24px; color: var(--status-rose);">Failed to load messages: ${e.message}</td></tr>`;
    }
  }

  createMessageRow(msg) {
    const tr = document.createElement('tr');
    tr.id = `msg-row-${msg.id}`;
    tr.dataset.id = msg.id;

    const timeStr = new Date(msg.created_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
    const isOutbound = msg.direction === 'outbound';
    const directionBadge = isOutbound
      ? `<span class="status-badge" style="background:var(--accent-blue-subtle); color:var(--accent-blue);">OUT</span>`
      : `<span class="status-badge" style="background:var(--status-emerald-subtle); color:var(--status-emerald);">IN</span>`;

    const status = msg.status || 'unknown';
    const statusBadge = `<span class="status-badge badge-${status}" id="status-badge-${msg.id}">${status.toUpperCase()}</span>`;
    const party = isOutbound ? `To: ${msg.recipient}` : `From: ${msg.sender}`;
    const body = msg.body || '';
    const truncatedBody = body.length > 55 ? body.substring(0, 55) + '…' : body;

    tr.innerHTML = `
      <td class="mono text-muted" style="font-size:12px;">${timeStr}</td>
      <td>${directionBadge}</td>
      <td class="mono" style="font-weight:600;">${this.escapeHtml(party)}</td>
      <td style="color:var(--text-secondary); max-width: 320px;">${this.escapeHtml(truncatedBody)}</td>
      <td class="mono" style="font-size:11.5px; color:var(--text-muted);">SIM ${msg.sim_slot + 1} (${msg.parts_count}p)</td>
      <td>${statusBadge}</td>
    `;

    tr.addEventListener('click', () => {
      this.openInspector(msg);
    });

    return tr;
  }

  prependMessageRow(msg) {
    const tbody = document.getElementById('messages-table-body');
    if (!tbody) return;

    // Check if duplicate row already rendered
    if (document.getElementById(`msg-row-${msg.id}`)) return;

    this.messages.unshift(msg);
    const row = this.createMessageRow(msg);
    row.style.background = 'var(--bg-elevated)';
    tbody.prepend(row);

    // Fade background smoothly
    setTimeout(() => {
      row.style.transition = 'background 1.5s ease';
      row.style.background = '';
    }, 500);
  }

  updateMessageRowStatus(msgId, status, errorMsg) {
    const badge = document.getElementById(`status-badge-${msgId}`);
    if (badge) {
      badge.className = `status-badge badge-${status}`;
      badge.textContent = status.toUpperCase();
    }
    const msg = this.messages.find(m => m.id === msgId);
    if (msg) {
      msg.status = status;
      if (errorMsg) msg.error_message = errorMsg;
      if (this.selectedMessage && this.selectedMessage.id === msgId) {
        this.openInspector(msg);
      }
    }
  }

  // --- INSPECTOR DRAWER ---
  bindInspector() {
    const drawer = document.getElementById('inspector-drawer');
    const closeBtn = document.getElementById('btn-close-drawer');
    closeBtn?.addEventListener('click', () => {
      drawer.classList.remove('open');
    });

    document.getElementById('btn-copy-json')?.addEventListener('click', () => {
      if (this.selectedMessage) {
        this.copyText(JSON.stringify(this.selectedMessage, null, 2));
      }
    });
  }

  openInspector(msg) {
    this.selectedMessage = msg;
    const drawer = document.getElementById('inspector-drawer');
    document.getElementById('inspect-id').textContent = msg.id;
    document.getElementById('inspect-direction').textContent = msg.direction.toUpperCase();
    document.getElementById('inspect-sender').textContent = msg.sender || '--';
    document.getElementById('inspect-recipient').textContent = msg.recipient || '--';
    document.getElementById('inspect-status').textContent = msg.status.toUpperCase();
    document.getElementById('inspect-sim').textContent = `Slot ${msg.sim_slot} (Parts: ${msg.parts_count})`;
    document.getElementById('inspect-idempotency').textContent = msg.idempotency_key || 'None';
    document.getElementById('inspect-created').textContent = new Date(msg.created_at).toLocaleString();
    document.getElementById('inspect-body').textContent = msg.body;
    document.getElementById('inspect-json').textContent = JSON.stringify(msg, null, 2);

    const errorContainer = document.getElementById('inspect-error-container');
    if (msg.error_message) {
      errorContainer.style.display = 'block';
      document.getElementById('inspect-error').textContent = msg.error_message;
    } else {
      errorContainer.style.display = 'none';
    }

    drawer.classList.add('open');
  }

  // --- COMPOSER ---
  bindComposer() {
    const bodyInput = document.getElementById('compose-body');
    const counterPill = document.getElementById('compose-char-counter');
    const sendBtn = document.getElementById('btn-send-sms');

    bodyInput?.addEventListener('input', () => {
      const stats = analyzeSmsText(bodyInput.value);
      counterPill.textContent = `${stats.length} / ${stats.maxPerPart} chars · ${stats.parts} part(s) (${stats.encoding})`;
      counterPill.classList.toggle('unicode-warning', stats.encoding.includes('Unicode'));
    });

    sendBtn?.addEventListener('click', async () => {
      const recipient = document.getElementById('compose-recipient').value.trim();
      const body = document.getElementById('compose-body').value.trim();
      const simSlot = parseInt(document.getElementById('compose-sim-slot').value, 10) || 0;
      const deviceId = document.getElementById('compose-device-id').value;

      if (!recipient || !body) {
        this.showToast('Recipient and body are required', 'error');
        return;
      }

      sendBtn.disabled = true;
      sendBtn.textContent = 'Dispatching...';

      try {
        const resp = await api.sendSMS({
          recipient,
          body,
          sim_slot: simSlot,
          device_id: deviceId || undefined,
        });

        this.showToast('SMS dispatched to Android Gateway', 'success');
        document.getElementById('compose-body').value = '';
        bodyInput.dispatchEvent(new Event('input'));
        this.refreshTelemetry();
        this.switchView('feed');
      } catch (e) {
        this.showToast(`Send failed: ${e.message}`, 'error');
      } finally {
        sendBtn.disabled = false;
        sendBtn.textContent = 'Dispatch SMS';
      }
    });

    // Feed Search & Filter
    document.getElementById('feed-search')?.addEventListener('input', (e) => {
      this.activeSearch = e.target.value.trim();
      this.loadMessages();
    });

    document.getElementById('feed-direction-filter')?.addEventListener('change', (e) => {
      this.activeDirectionFilter = e.target.value;
      this.loadMessages();
    });
  }

  async refreshDeviceSelects() {
    await this.loadDevices();
    const select = document.getElementById('compose-device-id');
    if (!select) return;

    select.innerHTML = '<option value="">Auto-Route (Any Online Device)</option>';
    this.devices.forEach(d => {
      const opt = document.createElement('option');
      opt.value = d.id;
      opt.textContent = `${d.name} (${(d.status || 'offline').toUpperCase()}) - Batt: ${d.battery_level}%`;
      select.appendChild(opt);
    });
  }

  // --- DEVICES VIEW ---
  async loadDevices() {
    const container = document.getElementById('devices-card-grid');
    if (!container) return;

    try {
      this.devices = (await api.listDevices()) || [];
      if (this.devices.length === 0) {
        container.innerHTML = `
          <div style="grid-column: 1/-1; text-align: center; padding: 48px; background: var(--bg-surface); border: 1px dashed var(--border-subtle); border-radius: 8px;">
            <p style="color:var(--text-secondary); margin-bottom: 16px;">No Android Gateway phones are currently paired.</p>
            <button class="btn btn-primary" id="btn-pair-first">Pair Android Device</button>
          </div>
        `;
        document.getElementById('btn-pair-first')?.addEventListener('click', () => this.openPairModal());
        return;
      }

      container.innerHTML = '';
      this.devices.forEach(dev => {
        container.appendChild(this.createDeviceCard(dev));
      });
    } catch (e) {
      container.innerHTML = `<p style="color:var(--status-rose)">Error loading devices: ${e.message}</p>`;
    }
  }

  createDeviceCard(dev) {
    const card = document.createElement('div');
    card.className = 'terminal-card';
    card.id = `device-card-${dev.id}`;

    const status = dev.status || 'offline';
    const isOnline = status === 'online';
    const statusBadge = `<span class="status-badge badge-${status}">${status.toUpperCase()}</span>`;
    const chargingIcon = dev.is_charging ? ' ⚡' : '';
    const lastSeenStr = this.formatLastSeen(dev.last_seen_at);

    let simHtml = '';
    if (dev.sim_cards && dev.sim_cards.length > 0) {
      simHtml = dev.sim_cards.map(s => `
        <div style="background:var(--bg-app); border:1px solid var(--border-subtle); padding:10px; border-radius:6px; margin-bottom:8px;">
          <div style="display:flex; justify-content:space-between; align-items:center; margin-bottom:4px;">
            <span style="font-weight:600; font-size:12px;">SIM Slot ${s.slot_index + 1}: ${this.escapeHtml(s.carrier_name || 'Carrier')}</span>
            <div class="signal-meter">
              <span class="signal-bar ${s.signal_level >= 1 ? 'active' : ''}"></span>
              <span class="signal-bar ${s.signal_level >= 2 ? 'active' : ''}"></span>
              <span class="signal-bar ${s.signal_level >= 3 ? 'active' : ''}"></span>
              <span class="signal-bar ${s.signal_level >= 4 ? 'active' : ''}"></span>
            </div>
          </div>
          <div style="display:flex; justify-content:space-between; font-size:11.5px; color:var(--text-muted);" class="mono">
            <span>${this.escapeHtml(s.phone_number || 'No number')}</span>
            <span>${s.signal_dbm || 0} dBm</span>
          </div>
        </div>
      `).join('');
    } else {
      simHtml = `<p style="color:var(--text-muted); font-size:12px;">No active SIM detected</p>`;
    }

    card.innerHTML = `
      <div class="terminal-card-header">
        <div class="terminal-card-title">
          <span class="pulse-dot ${isOnline ? '' : 'offline'}"></span>
          <span>${this.escapeHtml(dev.name)}</span>
        </div>
        ${statusBadge}
      </div>
      <div class="terminal-card-body">
        <div style="display:grid; grid-template-columns: 1fr 1fr; gap:12px; margin-bottom:16px;">
          <div style="background:var(--bg-app); padding:8px 12px; border-radius:6px; border:1px solid var(--border-subtle);">
            <div style="font-size:11px; color:var(--text-muted);">Battery</div>
            <div class="mono" style="font-weight:600; font-size:14px;">${lastSeenStr === 'Never' ? '--' : `${dev.battery_level}%${chargingIcon}`}</div>
          </div>
          <div style="background:var(--bg-app); padding:8px 12px; border-radius:6px; border:1px solid var(--border-subtle);">
            <div style="font-size:11px; color:var(--text-muted);">App Version</div>
            <div class="mono" style="font-weight:600; font-size:14px;">${this.escapeHtml(dev.app_version || '--')}</div>
          </div>
        </div>

        <div style="font-size:11.5px; font-weight:600; color:var(--text-secondary); margin-bottom:8px;">Cellular SIM Modules</div>
        ${simHtml}

        <div style="display:flex; justify-content:space-between; align-items:center; margin-top:14px; padding-top:10px; border-top:1px solid var(--border-subtle); font-size:11px; color:var(--text-muted);">
          <span>Last Heartbeat: <span class="mono">${lastSeenStr}</span></span>
          <button class="btn btn-danger btn-sm" data-delete-device="${dev.id}">Unpair</button>
        </div>
      </div>
    `;

    card.querySelector(`[data-delete-device="${dev.id}"]`)?.addEventListener('click', async () => {
      if (confirm(`Unpair gateway device "${dev.name}"?`)) {
        try {
          await api.deleteDevice(dev.id);
          this.showToast('Device unpaired', 'info');
        } catch (e) {
          this.showToast(`Unpair failed: ${e.message}`, 'error');
        }
        this.loadDevices();
        this.refreshTelemetry();
      }
    });

    return card;
  }

  updateDeviceInList(device) {
    const idx = this.devices.findIndex(d => d.id === device.id);
    if (idx !== -1) {
      this.devices[idx] = device;
    } else {
      this.devices.push(device);
    }
    if (this.currentView === 'devices') {
      this.loadDevices();
    }
  }

  // --- WEBHOOKS VIEW ---
  async loadWebhooks() {
    const tbody = document.getElementById('webhooks-table-body');
    if (!tbody) return;

    try {
      this.webhooks = (await api.listWebhooks()) || [];
      if (this.webhooks.length === 0) {
        tbody.innerHTML = `<tr><td colspan="5" style="text-align:center; padding:24px; color:var(--text-muted);">No webhooks registered.</td></tr>`;
        return;
      }

      tbody.innerHTML = '';
      this.webhooks.forEach(wh => {
        const tr = document.createElement('tr');
        const eventsList = (wh.events || []).map(e => `<span class="brand-badge" style="margin-right:4px;">${e}</span>`).join('');
        tr.innerHTML = `
          <td class="mono" style="font-weight:600;">${this.escapeHtml(wh.url)}</td>
          <td>${eventsList}</td>
          <td class="mono text-muted" style="font-size:11.5px;">${wh.secret ? 'HMAC-SHA256' : 'None'}</td>
          <td><span class="status-badge badge-${wh.is_active ? 'success' : 'failed'}">${wh.is_active ? 'ACTIVE' : 'INACTIVE'}</span></td>
          <td style="text-align:right;">
            <button class="btn btn-secondary btn-sm" data-test-wh="${wh.id}">Test Ping</button>
            <button class="btn btn-danger btn-sm" data-delete-wh="${wh.id}">Delete</button>
          </td>
        `;

        tr.querySelector(`[data-test-wh="${wh.id}"]`)?.addEventListener('click', async () => {
          this.showToast('Sending test ping...', 'info');
          try {
            const res = await api.testWebhook(wh.id);
            this.showToast(`Ping completed: HTTP ${res.response_status} (${res.latency_ms}ms)`, res.status === 'success' ? 'success' : 'error');
            this.loadDeliveries();
          } catch (e) {
            this.showToast(`Test failed: ${e.message}`, 'error');
          }
        });

        tr.querySelector(`[data-delete-wh="${wh.id}"]`)?.addEventListener('click', async () => {
          if (confirm('Delete this webhook endpoint?')) {
            await api.deleteWebhook(wh.id);
            this.showToast('Webhook deleted', 'info');
            this.loadWebhooks();
            this.refreshTelemetry();
          }
        });

        tbody.appendChild(tr);
      });
    } catch (e) {
      tbody.innerHTML = `<tr><td colspan="5" style="color:var(--status-rose); text-align:center;">Error: ${e.message}</td></tr>`;
    }
  }

  async loadDeliveries() {
    const tbody = document.getElementById('deliveries-table-body');
    if (!tbody) return;

    try {
      const deliveries = (await api.listWebhookDeliveries()) || [];
      if (deliveries.length === 0) {
        tbody.innerHTML = `<tr><td colspan="5" style="text-align:center; padding:18px; color:var(--text-muted);">No deliveries recorded.</td></tr>`;
        return;
      }

      tbody.innerHTML = '';
      deliveries.slice(0, 20).forEach(d => {
        const tr = document.createElement('tr');
        const timeStr = new Date(d.created_at).toLocaleTimeString();
        tr.innerHTML = `
          <td class="mono text-muted" style="font-size:11.5px;">${timeStr}</td>
          <td class="mono">${d.event_type}</td>
          <td><span class="status-badge badge-${d.status}">${d.status.toUpperCase()}</span></td>
          <td class="mono">${d.response_status ? `HTTP ${d.response_status}` : 'ERR'}</td>
          <td class="mono text-muted">${d.latency_ms}ms (Att: ${d.attempt})</td>
        `;
        tbody.appendChild(tr);
      });
    } catch (e) {
      console.warn('Error fetching deliveries:', e);
    }
  }

  // --- TOKENS VIEW ---
  async loadTokens() {
    const tbody = document.getElementById('tokens-table-body');
    if (!tbody) return;

    try {
      this.tokens = (await api.listTokens()) || [];
      if (this.tokens.length === 0) {
        tbody.innerHTML = `<tr><td colspan="5" style="text-align:center; padding:24px; color:var(--text-muted);">No API tokens generated.</td></tr>`;
        return;
      }

      tbody.innerHTML = '';
      this.tokens.forEach(tok => {
        const tr = document.createElement('tr');
        const scopesBadges = (tok.scopes || []).map(s => `<span class="brand-badge" style="margin-right:4px;">${s}</span>`).join('');
        const lastUsedStr = tok.last_used_at ? new Date(tok.last_used_at).toLocaleDateString() : 'Never';

        tr.innerHTML = `
          <td style="font-weight:600;">${this.escapeHtml(tok.name)}</td>
          <td class="mono" style="color:var(--text-secondary);">${tok.token_prefix}</td>
          <td>${scopesBadges}</td>
          <td class="mono text-muted" style="font-size:12px;">${lastUsedStr}</td>
          <td style="text-align:right;">
            <button class="btn btn-danger btn-sm" data-delete-tok="${tok.id}">Revoke</button>
          </td>
        `;

        tr.querySelector(`[data-delete-tok="${tok.id}"]`)?.addEventListener('click', async () => {
          if (confirm(`Revoke API Token "${tok.name}"?`)) {
            await api.deleteToken(tok.id);
            this.showToast('Token revoked', 'info');
            this.loadTokens();
          }
        });

        tbody.appendChild(tr);
      });
    } catch (e) {
      tbody.innerHTML = `<tr><td colspan="5" style="color:var(--status-rose); text-align:center;">Error: ${e.message}</td></tr>`;
    }
  }

  // --- OTA VIEW ---
  async loadOtaInfo() {
    const statusEl = document.getElementById('ota-status-card');
    try {
      const res = await api.checkOtaUpdate();
      if (res.release) {
        statusEl.innerHTML = `
          <div style="display:flex; justify-content:space-between; align-items:center; margin-bottom:12px;">
            <span style="font-weight:600;">Latest Android Release</span>
            <span class="status-badge badge-success">VERSION ${res.release.version}</span>
          </div>
          <div class="drawer-field-group">
            <div class="drawer-field-label">File Checksum (SHA-256)</div>
            <div class="mono text-muted" style="font-size:11.5px;">${res.release.checksum_sha256}</div>
          </div>
          <div class="drawer-field-group">
            <div class="drawer-field-label">File Size</div>
            <div class="mono">${(res.release.file_size / (1024 * 1024)).toFixed(2)} MB</div>
          </div>
          <div class="drawer-field-group">
            <div class="drawer-field-label">Changelog</div>
            <pre class="code-preview">${this.escapeHtml(res.release.changelog || 'No notes provided')}</pre>
          </div>
          <a href="${res.download_url}" class="btn btn-secondary btn-sm" target="_blank" download>Download APK</a>
        `;
      } else {
        statusEl.innerHTML = `<p style="color:var(--text-muted);">No APK releases uploaded yet.</p>`;
      }
    } catch (e) {
      statusEl.innerHTML = `<p style="color:var(--status-rose);">Error: ${e.message}</p>`;
    }
  }

  // --- MODALS ---
  bindModals() {
    // Generic close buttons, backdrop clicks and Escape
    document.querySelectorAll('[data-close-modal]').forEach(btn => {
      btn.addEventListener('click', () => this.closeModal(btn.dataset.closeModal));
    });
    document.querySelectorAll('.modal-overlay').forEach(overlay => {
      overlay.addEventListener('click', (e) => {
        if (e.target === overlay) this.closeModal(overlay.id);
      });
    });
    document.addEventListener('keydown', (e) => {
      if (e.key === 'Escape') {
        document.querySelectorAll('.modal-overlay.open').forEach(m => this.closeModal(m.id));
      }
    });

    // Copy-to-clipboard buttons
    document.querySelectorAll('[data-copy-target]').forEach(btn => {
      btn.addEventListener('click', () => {
        const text = document.getElementById(btn.dataset.copyTarget)?.textContent || '';
        this.copyText(text);
      });
    });

    // Settings Modal
    document.getElementById('btn-save-settings')?.addEventListener('click', () => {
      const token = document.getElementById('settings-token-input').value.trim();
      if (!token) {
        this.showToast('Admin token is required', 'error');
        return;
      }
      api.setToken(token);
      this.closeModal('settings-modal');
      this.showToast('Settings saved', 'success');
      this.refreshTelemetry();
      this.switchView(this.currentView);
    });

    // Pair Modal
    document.getElementById('btn-open-pair-modal')?.addEventListener('click', () => this.openPairModal());
    document.getElementById('btn-close-pair-modal')?.addEventListener('click', () => this.closeModal('pair-modal'));
    let pairUrlTimer = null;
    document.getElementById('pair-server-input')?.addEventListener('input', () => {
      clearTimeout(pairUrlTimer);
      pairUrlTimer = setTimeout(() => this.renderPairingCode(), 250);
    });

    // Webhook Modal
    document.getElementById('btn-open-webhook-modal')?.addEventListener('click', () => {
      document.getElementById('webhook-modal').classList.add('open');
    });
    document.getElementById('btn-close-webhook-modal')?.addEventListener('click', () => {
      document.getElementById('webhook-modal').classList.remove('open');
    });
    document.getElementById('btn-save-webhook')?.addEventListener('click', async () => {
      const url = document.getElementById('webhook-url-input').value.trim();
      const secret = document.getElementById('webhook-secret-input').value.trim();
      const events = Array.from(document.querySelectorAll('.webhook-event-checkbox:checked')).map(cb => cb.value);

      if (!url) {
        this.showToast('Webhook URL is required', 'error');
        return;
      }

      try {
        await api.createWebhook({ url, secret, events: events.length ? events : ['*'] });
        this.showToast('Webhook registered successfully', 'success');
        document.getElementById('webhook-modal').classList.remove('open');
        this.loadWebhooks();
        this.refreshTelemetry();
      } catch (e) {
        this.showToast(`Error: ${e.message}`, 'error');
      }
    });

    // Token Modal
    document.getElementById('btn-open-token-modal')?.addEventListener('click', () => {
      document.getElementById('token-modal').classList.add('open');
    });
    document.getElementById('btn-close-token-modal')?.addEventListener('click', () => {
      document.getElementById('token-modal').classList.remove('open');
    });
    document.getElementById('btn-save-token')?.addEventListener('click', async () => {
      const name = document.getElementById('token-name-input').value.trim();
      const scopes = Array.from(document.querySelectorAll('.token-scope-checkbox:checked')).map(cb => cb.value);

      try {
        const resp = await api.createToken({ name, scopes: scopes.length ? scopes : ['*'] });
        document.getElementById('token-modal').classList.remove('open');
        this.showTokenResultModal(resp.key);
        this.loadTokens();
      } catch (e) {
        this.showToast(`Error: ${e.message}`, 'error');
      }
    });

    // OTA Upload Form
    document.getElementById('btn-upload-ota')?.addEventListener('click', async () => {
      const fileInput = document.getElementById('ota-file-input');
      const versionInput = document.getElementById('ota-version-input').value.trim();
      const versionCode = document.getElementById('ota-code-input').value.trim();
      const changelog = document.getElementById('ota-changelog-input').value.trim();

      if (!fileInput.files || fileInput.files.length === 0) {
        this.showToast('Select an APK file to upload', 'error');
        return;
      }

      const formData = new FormData();
      formData.append('file', fileInput.files[0]);
      formData.append('version', versionInput || '1.0.1');
      formData.append('version_code', versionCode || '2');
      formData.append('changelog', changelog);
      formData.append('target', 'android');

      this.showToast('Uploading release...', 'info');
      try {
        await api.uploadOtaRelease(formData);
        this.showToast('New APK release published and broadcasted!', 'success');
        this.loadOtaInfo();
      } catch (e) {
        this.showToast(`Upload failed: ${e.message}`, 'error');
      }
    });
  }

  closeModal(id) {
    document.getElementById(id)?.classList.remove('open');
    if (id === 'pair-modal') {
      this.pendingPairDeviceId = null;
      this.pairing = null;
    }
  }

  openSettingsModal(authFailed = false) {
    const modal = document.getElementById('settings-modal');
    if (!modal || (authFailed && modal.classList.contains('open'))) return;
    document.getElementById('settings-auth-notice').style.display = authFailed ? 'block' : 'none';
    document.getElementById('settings-token-input').value = authFailed ? '' : api.token;
    modal.classList.add('open');
    document.getElementById('settings-token-input').focus();
  }

  defaultPairServerUrl() {
    const saved = localStorage.getItem('htsms_pair_server_url');
    return saved || window.location.origin;
  }

  async openPairModal() {
    const modal = document.getElementById('pair-modal');
    modal.classList.add('open');

    const serverInput = document.getElementById('pair-server-input');
    serverInput.value = this.defaultPairServerUrl();
    this.setPairStatus('waiting', 'Generating pairing code…');

    try {
      const res = await api.pairDevice('Android Gateway Device');
      this.pairing = res;
      this.pendingPairDeviceId = res.device_id;
      this.renderPairingCode();
      this.setPairStatus('waiting', 'Waiting for the phone to connect…');
      this.loadDevices();
    } catch (e) {
      this.setPairStatus('error', `Could not create pairing code: ${e.message}`);
    }
  }

  renderPairingCode() {
    if (!this.pairing) return;
    const serverUrl = document.getElementById('pair-server-input').value.trim().replace(/\/+$/, '');
    const hint = document.getElementById('pair-server-hint');

    let host = '';
    try {
      host = new URL(serverUrl).hostname;
    } catch {
      hint.textContent = 'Enter a full URL such as http://192.168.1.20:8080';
      hint.classList.add('warning');
      return;
    }

    if (['localhost', '127.0.0.1', '::1', '0.0.0.0'].includes(host)) {
      hint.textContent = `"${host}" points at the phone itself. Use this computer's LAN IP or public domain instead.`;
      hint.classList.add('warning');
    } else {
      hint.textContent = 'The phone must be able to reach this address.';
      hint.classList.remove('warning');
      localStorage.setItem('htsms_pair_server_url', serverUrl);
    }

    const payload = JSON.stringify({
      v: 1,
      server_url: serverUrl,
      device_id: this.pairing.device_id,
      token: this.pairing.token,
    });

    try {
      renderQRCode(document.getElementById('pair-qr-canvas'), payload);
    } catch (e) {
      this.setPairStatus('error', `QR render failed: ${e.message}`);
    }
    document.getElementById('pair-token-text').textContent = this.pairing.token;
    document.getElementById('pair-server-text').textContent = serverUrl;
  }

  setPairStatus(state, text) {
    const box = document.getElementById('pair-status');
    if (!box) return;
    box.classList.toggle('connected', state === 'connected');
    const dot = box.querySelector('.pulse-dot');
    dot.className = 'pulse-dot' + (state === 'waiting' ? ' waiting' : state === 'error' ? ' offline' : '');
    document.getElementById('pair-status-text').textContent = text;
  }

  markPairingConnected() {
    this.pendingPairDeviceId = null;
    this.setPairStatus('connected', 'Phone connected! You can close this window.');
    this.showToast('Android gateway paired and online', 'success');
    this.loadDevices();
  }

  showTokenResultModal(rawKey) {
    document.getElementById('token-result-text').textContent = rawKey;
    document.getElementById('token-result-modal').classList.add('open');
  }

  async copyText(text) {
    try {
      await navigator.clipboard.writeText(text);
      this.showToast('Copied to clipboard', 'success');
    } catch {
      // Clipboard API needs HTTPS or localhost; fall back to a hidden textarea.
      const ta = document.createElement('textarea');
      ta.value = text;
      document.body.appendChild(ta);
      ta.select();
      const ok = document.execCommand('copy');
      ta.remove();
      this.showToast(ok ? 'Copied to clipboard' : 'Copy failed, select the text manually', ok ? 'success' : 'error');
    }
  }

  formatLastSeen(value) {
    const t = value ? new Date(value) : null;
    // Go's zero time (year 1) means the phone has never connected.
    if (!t || isNaN(t) || t.getFullYear() < 2000) return 'Never';
    const secs = Math.round((Date.now() - t.getTime()) / 1000);
    if (secs < 60) return 'just now';
    if (secs < 3600) return `${Math.floor(secs / 60)} min ago`;
    if (secs < 86400) return `${Math.floor(secs / 3600)} h ago`;
    return t.toLocaleString();
  }

  showToast(message, type = 'info') {
    const container = document.getElementById('toast-container');
    if (!container) return;

    const toast = document.createElement('div');
    toast.style.cssText = `
      background: var(--bg-surface);
      border: 1px solid var(--border-active);
      color: var(--text-primary);
      padding: 10px 16px;
      border-radius: 6px;
      font-size: 13px;
      box-shadow: 0 4px 14px rgba(0,0,0,0.4);
      display: flex;
      align-items: center;
      gap: 10px;
      margin-top: 8px;
      animation: fadeIn 0.2s ease;
    `;

    const iconColor = type === 'success' ? 'var(--status-emerald)' : type === 'error' ? 'var(--status-rose)' : 'var(--accent-blue)';
    toast.innerHTML = `<span style="width:8px; height:8px; border-radius:50%; background:${iconColor};"></span><span>${this.escapeHtml(message)}</span>`;

    container.appendChild(toast);
    setTimeout(() => {
      toast.style.opacity = '0';
      toast.style.transition = 'opacity 0.3s ease';
      setTimeout(() => toast.remove(), 300);
    }, 4000);
  }

  escapeHtml(str) {
    if (!str) return '';
    return str.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
  }
}

window.addEventListener('DOMContentLoaded', () => {
  const dashboard = new GatewayDashboard();
  dashboard.init();
});

