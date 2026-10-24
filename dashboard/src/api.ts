// Typed client for the gateway's REST API. The session cookie is sent automatically;
// the X-Requested-With header satisfies the server's CSRF check on writes.

export interface User {
  id: string;
  email: string;
  name: string;
  role: 'admin' | 'user';
  disabled: boolean;
  is_admin: boolean;
  last_login_at?: string;
  created_at: string;
}

export interface AdminUser extends User {
  devices: number;
  messages: number;
  workflows: number;
}

export interface SimCard {
  slot_index: number;
  carrier_name: string;
  phone_number: string;
  signal_level: number;
  signal_dbm: number;
}

export interface Device {
  id: string;
  user_id: string;
  name: string;
  status: 'online' | 'offline' | string;
  battery_level: number;
  is_charging: boolean;
  app_version: string;
  last_seen_at: string;
  created_at: string;
  sim_cards?: SimCard[];
}

export interface Message {
  id: string;
  user_id: string;
  direction: 'inbound' | 'outbound';
  device_id: string;
  sim_slot: number;
  sender: string;
  recipient: string;
  body: string;
  status: string;
  idempotency_key: string;
  parts_count: number;
  error_message?: string;
  sent_at?: string;
  delivered_at?: string;
  created_at: string;
}

export interface Paged<T> {
  data: T[];
  total: number;
  limit: number;
  offset: number;
}

export interface Stats {
  online_devices: number;
  total_devices: number;
  total_inbound: number;
  total_outbound: number;
  total_failed: number;
  inbound_24h: number;
  outbound_24h: number;
  active_webhooks: number;
  active_workflows: number;
  total_workflows: number;
}

export interface Webhook {
  id: string;
  user_id: string;
  url: string;
  secret?: string;
  events: string[];
  is_active: boolean;
  created_at: string;
}

export interface WebhookDelivery {
  id: string;
  webhook_id: string;
  event_type: string;
  response_status: number;
  latency_ms: number;
  attempt: number;
  status: string;
  created_at: string;
}

export interface ApiToken {
  id: string;
  user_id: string;
  name: string;
  token_prefix: string;
  scopes: string[];
  last_used_at?: string;
  created_at: string;
}

export interface Provider {
  id: string;
  name: string;
  default_model: string;
  key_url: string;
  key_prefix: string;
  base_url_hint: string;
}

export interface AICredential {
  id: string;
  provider: string;
  label: string;
  base_url?: string;
  key_hint: string;
  created_at: string;
}

export interface WorkflowRun {
  id: string;
  workflow_id: string;
  trigger_message_id?: string;
  status: 'running' | 'success' | 'failed' | 'skipped';
  dry_run: boolean;
  steps: RunStep[];
  error?: string;
  started_at: string;
  finished_at?: string;
}

export interface RunStep {
  node_id: string;
  type: string;
  label: string;
  status: 'success' | 'failed' | 'simulated';
  output?: Record<string, string>;
  error?: string;
  duration_ms: number;
}

export interface GraphNode {
  id: string;
  type: string;
  position: { x: number; y: number };
  data: Record<string, unknown>;
}

export interface GraphEdge {
  id: string;
  source: string;
  target: string;
  sourceHandle?: string | null;
}

export interface Graph {
  nodes: GraphNode[];
  edges: GraphEdge[];
}

export interface Workflow {
  id: string;
  user_id: string;
  name: string;
  description: string;
  enabled: boolean;
  graph: Graph;
  problems: string[];
  last_run?: WorkflowRun;
  created_at: string;
  updated_at: string;
}

export interface OtaRelease {
  id: string;
  version: string;
  version_code: number;
  file_name: string;
  checksum_sha256: string;
  file_size: number;
  changelog: string;
  created_at: string;
}

export class ApiError extends Error {
  constructor(message: string, public status: number, public code?: string, public details?: unknown) {
    super(message);
  }
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers: Record<string, string> = { 'X-Requested-With': 'httpsms', ...(init.headers as Record<string, string>) };
  if (init.body && !(init.body instanceof FormData)) headers['Content-Type'] = 'application/json';

  const resp = await fetch(path, { ...init, headers, credentials: 'same-origin' });
  const text = await resp.text();
  let data: any = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    /* non-JSON error body */
  }
  if (!resp.ok) {
    if (resp.status === 401 && !path.startsWith('/api/v1/auth/')) {
      window.dispatchEvent(new CustomEvent('auth:expired'));
    }
    throw new ApiError(data?.error?.message || text.trim() || `HTTP ${resp.status}`, resp.status, data?.error?.code, data);
  }
  return data as T;
}

const body = (v: unknown) => JSON.stringify(v);

export const api = {
  // Auth
  authStatus: () => request<{ has_users: boolean; registration_open: boolean }>('/api/v1/auth/status'),
  me: () => request<User>('/api/v1/auth/me'),
  login: (email: string, password: string) => request<User>('/api/v1/auth/login', { method: 'POST', body: body({ email, password }) }),
  register: (name: string, email: string, password: string) =>
    request<User>('/api/v1/auth/register', { method: 'POST', body: body({ name, email, password }) }),
  logout: () => request('/api/v1/auth/logout', { method: 'POST' }),
  updateMe: (name: string) => request<User>('/api/v1/auth/me', { method: 'PATCH', body: body({ name }) }),
  changePassword: (current_password: string, new_password: string) =>
    request('/api/v1/auth/password', { method: 'POST', body: body({ current_password, new_password }) }),

  // Overview & messages
  stats: () => request<Stats>('/api/v1/system/stats'),
  messages: (params: Record<string, string | number>) => {
    const q = new URLSearchParams(Object.entries(params).filter(([, v]) => v !== '' && v !== undefined).map(([k, v]) => [k, String(v)]));
    return request<Paged<Message>>(`/api/v1/messages?${q}`);
  },
  sendSms: (payload: { recipient: string; body: string; device_id?: string; sim_slot: number }) =>
    request<Message>('/api/v1/sms/send', { method: 'POST', body: body(payload) }),

  // Devices
  devices: () => request<Device[]>('/api/v1/devices'),
  pairDevice: (name: string) => request<{ device_id: string; name: string; token: string }>('/api/v1/devices/pair', { method: 'POST', body: body({ name }) }),
  renameDevice: (id: string, name: string) => request<Device>(`/api/v1/devices/${id}`, { method: 'PATCH', body: body({ name }) }),
  deleteDevice: (id: string) => request(`/api/v1/devices/${id}`, { method: 'DELETE' }),

  // Webhooks
  webhooks: () => request<Webhook[]>('/api/v1/webhooks'),
  createWebhook: (payload: { url: string; secret?: string; events: string[] }) =>
    request<Webhook>('/api/v1/webhooks', { method: 'POST', body: body(payload) }),
  deleteWebhook: (id: string) => request(`/api/v1/webhooks/${id}`, { method: 'DELETE' }),
  testWebhook: (id: string) => request<WebhookDelivery>(`/api/v1/webhooks/${id}/test`, { method: 'POST' }),
  deliveries: () => request<WebhookDelivery[]>('/api/v1/webhooks/deliveries?limit=30'),

  // Tokens
  tokens: () => request<ApiToken[]>('/api/v1/tokens'),
  createToken: (name: string, scopes: string[]) => request<{ token: ApiToken; key: string }>('/api/v1/tokens', { method: 'POST', body: body({ name, scopes }) }),
  deleteToken: (id: string) => request(`/api/v1/tokens/${id}`, { method: 'DELETE' }),

  // AI keys
  providers: () => request<Provider[]>('/api/v1/ai/providers'),
  credentials: () => request<AICredential[]>('/api/v1/ai/credentials'),
  createCredential: (payload: { provider: string; label: string; api_key: string; base_url?: string }) =>
    request<{ credential: AICredential; models: string[] }>('/api/v1/ai/credentials', { method: 'POST', body: body(payload) }),
  credentialModels: (id: string) => request<{ provider: string; models: string[] }>(`/api/v1/ai/credentials/${id}/models`),
  deleteCredential: (id: string) => request(`/api/v1/ai/credentials/${id}`, { method: 'DELETE' }),

  // Workflows
  workflows: () => request<Workflow[]>('/api/v1/workflows'),
  workflow: (id: string) => request<Workflow>(`/api/v1/workflows/${id}`),
  createWorkflow: (payload: { name: string; description?: string; graph?: Graph }) =>
    request<Workflow>('/api/v1/workflows', { method: 'POST', body: body(payload) }),
  updateWorkflow: (id: string, payload: Partial<Pick<Workflow, 'name' | 'description' | 'enabled' | 'graph'>>) =>
    request<Workflow>(`/api/v1/workflows/${id}`, { method: 'PUT', body: body(payload) }),
  deleteWorkflow: (id: string) => request(`/api/v1/workflows/${id}`, { method: 'DELETE' }),
  testWorkflow: (id: string, payload: { sender: string; body: string; graph: Graph }) =>
    request<{ matched: boolean; run?: WorkflowRun }>(`/api/v1/workflows/${id}/test`, { method: 'POST', body: body(payload) }),
  workflowRuns: (id?: string) => request<WorkflowRun[]>(id ? `/api/v1/workflows/${id}/runs?limit=30` : '/api/v1/workflow-runs?limit=30'),

  // Admin
  adminUsers: () => request<AdminUser[]>('/api/v1/admin/users'),
  adminUpdateUser: (id: string, payload: { role?: string; disabled?: boolean; password?: string }) =>
    request<User>(`/api/v1/admin/users/${id}`, { method: 'PATCH', body: body(payload) }),
  adminDeleteUser: (id: string) => request(`/api/v1/admin/users/${id}`, { method: 'DELETE' }),
  otaReleases: () => request<OtaRelease[]>('/api/v1/ota/releases'),
  uploadOta: (form: FormData) => request<OtaRelease>('/api/v1/ota/upload', { method: 'POST', body: form }),
};
