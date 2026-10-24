/**
 * HttpSMS Gateway TypeScript / Node.js SDK
 * Official type-safe client library for interacting with HttpSMS Gateway.
 * Zero-dependency: uses modern global fetch and standard Web Crypto / Node crypto.
 */

export type MessageDirection = 'inbound' | 'outbound';
export type MessageStatus = 'pending' | 'queued' | 'dispatched' | 'delivered' | 'failed' | 'received';

export interface SimCard {
  id: string;
  device_id: string;
  slot_index: number;
  sub_id: number;
  carrier_name: string;
  phone_number: string;
  country_iso: string;
  signal_level: number;
  signal_dbm: number;
  is_active: boolean;
}

export interface Device {
  id: string;
  user_id?: string;
  name: string;
  status: 'online' | 'offline';
  battery_level: number;
  is_charging: boolean;
  app_version: string;
  app_version_code: number;
  last_seen_at: string;
  created_at: string;
  sim_cards: SimCard[];
}

export interface Message {
  id: string;
  device_id?: string;
  direction: MessageDirection;
  sender?: string;
  recipient?: string;
  body: string;
  sim_slot: number;
  status: MessageStatus;
  parts_count: number;
  idempotency_key?: string;
  error_message?: string;
  created_at: string;
  updated_at: string;
}

export interface MessagesListResponse {
  data: Message[];
  limit: number;
  offset: number;
  total: number;
}

export interface Webhook {
  id: string;
  url: string;
  events: string[];
  status: 'active' | 'disabled';
  secret?: string;
  failure_count: number;
  created_at: string;
}

export interface SystemStats {
  online_devices: number;
  total_devices: number;
  total_inbound: number;
  total_outbound: number;
  inbound_24h: number;
  outbound_24h: number;
  total_failed: number;
  active_webhooks: number;
  total_workflows: number;
  active_workflows: number;
  timestamp: string;
}

export interface SendSmsParams {
  recipient: string;
  body: string;
  sim_slot?: number;
  idempotency_key?: string;
  device_id?: string;
}

export interface ListMessagesParams {
  direction?: MessageDirection;
  search?: string;
  limit?: number;
  offset?: number;
}

export interface CreateWebhookParams {
  url: string;
  events: string[];
  secret?: string;
}

export class HttpSmsError extends Error {
  constructor(
    message: string,
    public statusCode?: number,
    public responseBody?: string
  ) {
    super(message);
    this.name = 'HttpSmsError';
  }
}

export class HttpSmsAuthError extends HttpSmsError {
  constructor(message: string, statusCode?: number, responseBody?: string) {
    super(message, statusCode, responseBody);
    this.name = 'HttpSmsAuthError';
  }
}

export class HttpSmsNotFoundError extends HttpSmsError {
  constructor(message: string, statusCode?: number, responseBody?: string) {
    super(message, statusCode, responseBody);
    this.name = 'HttpSmsNotFoundError';
  }
}

export interface HttpSmsClientConfig {
  apiKey?: string;
  baseUrl?: string;
  timeoutMs?: number;
}

export class HttpSmsClient {
  private apiKey: string;
  private baseUrl: string;
  private timeoutMs: number;

  public readonly sms: {
    send: (params: SendSmsParams) => Promise<Message>;
    list: (params?: ListMessagesParams) => Promise<MessagesListResponse>;
    get: (id: string) => Promise<Message>;
  };

  public readonly devices: {
    list: () => Promise<Device[]>;
  };

  public readonly webhooks: {
    create: (params: CreateWebhookParams) => Promise<Webhook>;
    list: () => Promise<Webhook[]>;
    test: (id: string) => Promise<Record<string, unknown>>;
    verifySignature: (payload: string | Uint8Array, signatureHeader: string, secret: string) => Promise<boolean>;
  };

  public readonly system: {
    getStats: () => Promise<SystemStats>;
  };

  constructor(config: HttpSmsClientConfig = {}) {
    this.apiKey = config.apiKey || (typeof process !== 'undefined' ? process.env.HTTPSMS_API_KEY || '' : '');
    const defaultUrl = 'http://localhost:8080';
    this.baseUrl = (config.baseUrl || (typeof process !== 'undefined' ? process.env.HTTPSMS_BASE_URL || defaultUrl : defaultUrl)).replace(/\/+$/, '');
    this.timeoutMs = config.timeoutMs || 15000;

    this.sms = {
      send: (params: SendSmsParams) => this.request<Message>('POST', '/sms/send', params),
      list: (params?: ListMessagesParams) => {
        const queryParams = new URLSearchParams();
        if (params?.direction) queryParams.set('direction', params.direction);
        if (params?.search) queryParams.set('search', params.search);
        if (params?.limit !== undefined) queryParams.set('limit', String(params.limit));
        if (params?.offset !== undefined) queryParams.set('offset', String(params.offset));
        const queryStr = queryParams.toString();
        const path = queryStr ? `/messages?${queryStr}` : '/messages';
        return this.request<MessagesListResponse>('GET', path);
      },
      get: (id: string) => this.request<Message>('GET', `/messages/${encodeURIComponent(id)}`),
    };

    this.devices = {
      list: () => this.request<Device[]>('GET', '/devices'),
    };

    this.webhooks = {
      create: (params: CreateWebhookParams) => this.request<Webhook>('POST', '/webhooks', params),
      list: () => this.request<Webhook[]>('GET', '/webhooks'),
      test: (id: string) => this.request<Record<string, unknown>>('POST', `/webhooks/${encodeURIComponent(id)}/test`),
      verifySignature: HttpSmsClient.verifyWebhookSignature,
    };

    this.system = {
      getStats: () => this.request<SystemStats>('GET', '/system/stats'),
    };
  }

  private async request<T>(method: string, path: string, body?: unknown): Promise<T> {
    const url = `${this.baseUrl}/api/v1${path}`;
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), this.timeoutMs);

    const headers: Record<string, string> = {
      'Authorization': `Bearer ${this.apiKey}`,
      'User-Agent': 'HttpSMS-TypeScript-SDK/1.0.0',
    };

    if (body !== undefined) {
      headers['Content-Type'] = 'application/json';
    }

    try {
      const response = await fetch(url, {
        method,
        headers,
        body: body !== undefined ? JSON.stringify(body) : undefined,
        signal: controller.signal,
      });

      const text = await response.text();
      if (!response.ok) {
        if (response.status === 401 || response.status === 403) {
          throw new HttpSmsAuthError(`HttpSMS Auth Error (${response.status}): ${text}`, response.status, text);
        } else if (response.status === 404) {
          throw new HttpSmsNotFoundError(`HttpSMS Not Found (404): ${text}`, response.status, text);
        } else {
          throw new HttpSmsError(`HttpSMS Error (${response.status}): ${text}`, response.status, text);
        }
      }

      return text.trim() ? (JSON.parse(text) as T) : ({} as T);
    } catch (err: any) {
      if (err instanceof HttpSmsError) throw err;
      if (err.name === 'AbortError') {
        throw new HttpSmsError(`HttpSMS Request timed out after ${this.timeoutMs}ms`);
      }
      throw new HttpSmsError(`HttpSMS Request failed: ${err.message || String(err)}`);
    } finally {
      clearTimeout(timer);
    }
  }

  /**
   * Verifies an incoming webhook HMAC-SHA256 signature using standard Web Crypto.
   */
  public static async verifyWebhookSignature(
    payload: string | Uint8Array,
    signatureHeader: string,
    secret: string
  ): Promise<boolean> {
    if (!signatureHeader || !secret) return false;

    const enc = new TextEncoder();
    const dataBytes = typeof payload === 'string' ? enc.encode(payload) : payload;
    const keyBytes = enc.encode(secret);

    // If Node.js crypto is available
    if (typeof process !== 'undefined' && (process.versions?.node || process.versions?.bun)) {
      try {
        const crypto = await import('node:crypto');
        const computed = crypto.createHmac('sha256', secret).update(dataBytes).digest('hex');
        return crypto.timingSafeEqual(Buffer.from(signatureHeader), Buffer.from(computed));
      } catch {
        // Fall back to Web Crypto
      }
    }

    // Web Crypto API fallback
    try {
      const key = await crypto.subtle.importKey(
        'raw',
        keyBytes,
        { name: 'HMAC', hash: 'SHA-256' },
        false,
        ['sign']
      );
      const signatureBuffer = await crypto.subtle.sign('HMAC', key, dataBytes);
      const computedHex = Array.from(new Uint8Array(signatureBuffer))
        .map(b => b.toString(16).padStart(2, '0'))
        .join('');
      return computedHex === signatureHeader.trim().toLowerCase();
    } catch {
      return false;
    }
  }
}

