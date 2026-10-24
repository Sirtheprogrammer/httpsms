export class TelemetrySocket {
  constructor() {
    this.ws = null;
    this.listeners = new Map();
    this.reconnectAttempts = 0;
    this.maxReconnectDelay = 10000;
    this.statusListeners = [];
  }

  connect() {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsUrl = `${protocol}//${window.location.host}/ws/dashboard`;

    try {
      this.ws = new WebSocket(wsUrl);

      this.ws.onopen = () => {
        this.reconnectAttempts = 0;
        this.notifyStatus(true);
      };

      this.ws.onmessage = (event) => {
        try {
          const payload = JSON.parse(event.data);
          if (payload.event) {
            this.emit(payload.event, payload.data);
          }
        } catch (e) {
          // Non-JSON or pong frame
        }
      };

      this.ws.onclose = () => {
        this.notifyStatus(false);
        this.scheduleReconnect();
      };

      this.ws.onerror = (err) => {
        console.warn('[Telemetry WS] Connection error:', err);
      };
    } catch (e) {
      this.scheduleReconnect();
    }
  }

  scheduleReconnect() {
    const delay = Math.min(1000 * Math.pow(1.5, this.reconnectAttempts), this.maxReconnectDelay);
    this.reconnectAttempts++;
    setTimeout(() => this.connect(), delay);
  }

  on(event, callback) {
    if (!this.listeners.has(event)) {
      this.listeners.set(event, []);
    }
    this.listeners.get(event).push(callback);
    return () => {
      const arr = this.listeners.get(event);
      if (arr) {
        this.listeners.set(event, arr.filter(cb => cb !== callback));
      }
    };
  }

  onStatusChange(callback) {
    this.statusListeners.push(callback);
  }

  notifyStatus(isOnline) {
    this.statusListeners.forEach(cb => cb(isOnline));
  }

  emit(event, data) {
    const callbacks = this.listeners.get(event) || [];
    callbacks.forEach(cb => {
      try {
        cb(data);
      } catch (e) {
        console.error(`[WS Handler Error] ${event}:`, e);
      }
    });
  }
}

export const socket = new TelemetrySocket();

