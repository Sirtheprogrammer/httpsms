export class ApiClient {
  constructor() {
    this.token = localStorage.getItem('htsms_master_token') || 'htsms_admin_secret_change_me';
    this.baseUrl = window.location.origin;
  }

  setToken(token) {
    this.token = token;
    localStorage.setItem('htsms_master_token', token);
  }

  async request(path, options = {}) {
    const url = `${this.baseUrl}${path}`;
    const headers = {
      ...options.headers,
    };

    if (this.token) {
      headers['Authorization'] = `Bearer ${this.token}`;
    }

    if (!(options.body instanceof FormData)) {
      headers['Content-Type'] = 'application/json';
    }

    try {
      const resp = await fetch(url, {
        ...options,
        headers,
      });

      if (resp.status === 401) {
        window.dispatchEvent(new CustomEvent('auth:required'));
      }

      // Error responses from proxies or http.Error may not be JSON.
      const text = await resp.text();
      let data = null;
      try {
        data = text ? JSON.parse(text) : null;
      } catch {
        if (resp.ok) throw new Error('Server returned an invalid response');
      }
      if (!resp.ok) {
        throw new Error(data?.error?.message || text.trim() || `HTTP ${resp.status}`);
      }
      return data;
    } catch (err) {
      console.error(`[API Error] ${path}:`, err);
      throw err;
    }
  }

  // SMS
  sendSMS(payload) {
    return this.request('/api/v1/sms/send', {
      method: 'POST',
      body: JSON.stringify(payload),
    });
  }

  listMessages(params = {}) {
    const query = new URLSearchParams(params).toString();
    return this.request(`/api/v1/messages?${query}`);
  }

  getMessage(id) {
    return this.request(`/api/v1/messages/${id}`);
  }

  // Devices
  listDevices() {
    return this.request('/api/v1/devices');
  }

  pairDevice(name) {
    return this.request('/api/v1/devices/pair', {
      method: 'POST',
      body: JSON.stringify({ name }),
    });
  }

  deleteDevice(id) {
    return this.request(`/api/v1/devices/${id}`, {
      method: 'DELETE',
    });
  }

  // Webhooks
  listWebhooks() {
    return this.request('/api/v1/webhooks');
  }

  createWebhook(payload) {
    return this.request('/api/v1/webhooks', {
      method: 'POST',
      body: JSON.stringify(payload),
    });
  }

  testWebhook(id) {
    return this.request(`/api/v1/webhooks/${id}/test`, {
      method: 'POST',
    });
  }

  deleteWebhook(id) {
    return this.request(`/api/v1/webhooks/${id}`, {
      method: 'DELETE',
    });
  }

  listWebhookDeliveries(webhookId = '') {
    const q = webhookId ? `?webhook_id=${webhookId}` : '';
    return this.request(`/api/v1/webhooks/deliveries${q}`);
  }

  // Tokens
  listTokens() {
    return this.request('/api/v1/tokens');
  }

  createToken(payload) {
    return this.request('/api/v1/tokens', {
      method: 'POST',
      body: JSON.stringify(payload),
    });
  }

  deleteToken(id) {
    return this.request(`/api/v1/tokens/${id}`, {
      method: 'DELETE',
    });
  }

  // OTA
  checkOtaUpdate() {
    return this.request('/api/v1/ota/check?target=android');
  }

  uploadOtaRelease(formData) {
    return this.request('/api/v1/ota/upload', {
      method: 'POST',
      body: formData,
    });
  }

  // Round-trip time to the public health endpoint, or null when unreachable.
  async measureLatency() {
    const start = performance.now();
    try {
      const resp = await fetch(`${this.baseUrl}/api/v1/health`, { cache: 'no-store' });
      if (!resp.ok) return null;
      return Math.round(performance.now() - start);
    } catch {
      return null;
    }
  }

  // Stats
  getSystemStats() {
    return this.request('/api/v1/system/stats');
  }
}

export const api = new ApiClient();

