import type { Graph, GraphNode } from '../api';
import type { IconName } from '../components/icons';

export type NodeKind = 'trigger' | 'ai_agent' | 'condition' | 'send_sms' | 'http_request' | 'delay';

export interface NodeMeta {
  kind: NodeKind;
  title: string;
  description: string;
  icon: IconName;
  tone: 'blue' | 'violet' | 'amber' | 'green' | 'accent' | 'red';
  idPrefix: string;
  defaults: Record<string, unknown>;
  /** Variables this node exposes to later steps as {{id.key}}. */
  outputs: string[];
}

export const NODE_META: Record<NodeKind, NodeMeta> = {
  trigger: {
    kind: 'trigger', title: 'Incoming SMS', description: 'Starts the workflow when a phone receives an SMS',
    icon: 'inbox', tone: 'blue', idPrefix: 'trigger', defaults: { label: 'Incoming SMS', match: 'any' }, outputs: [],
  },
  ai_agent: {
    kind: 'ai_agent', title: 'AI agent', description: 'Ask an AI model to write a reply or decide something',
    icon: 'sparkles', tone: 'violet', idPrefix: 'ai',
    defaults: {
      label: 'AI agent', credentialId: '', model: '', maxTokens: 400, memory: true, memoryTurns: 10,
      systemPrompt: 'You are a helpful assistant replying by SMS. Keep replies short and plain-text (under 300 characters). No markdown.',
      prompt: '{{trigger.body}}',
    },
    outputs: ['text', 'model'],
  },
  condition: {
    kind: 'condition', title: 'Condition', description: 'Branch on text: true or false path',
    icon: 'branch', tone: 'amber', idPrefix: 'if', defaults: { label: 'Condition', value: '{{trigger.body}}', operator: 'contains', compare: '' }, outputs: ['result'],
  },
  send_sms: {
    kind: 'send_sms', title: 'Send SMS', description: 'Reply to the sender or text another number',
    icon: 'send', tone: 'green', idPrefix: 'sms', defaults: { label: 'Send SMS', to: '{{trigger.sender}}', body: '' }, outputs: ['message_id'],
  },
  http_request: {
    kind: 'http_request', title: 'HTTP request', description: 'Call your own API or another service',
    icon: 'globe', tone: 'accent', idPrefix: 'http',
    defaults: { label: 'HTTP request', method: 'POST', url: 'https://', headers: '', body: '{\n  "from": "{{trigger.sender}}",\n  "text": "{{trigger.body}}"\n}' },
    outputs: ['status', 'body'],
  },
  delay: {
    kind: 'delay', title: 'Wait', description: 'Pause before the next step (up to 5 minutes)',
    icon: 'clock', tone: 'red', idPrefix: 'wait', defaults: { label: 'Wait', seconds: 10 }, outputs: [],
  },
};

export const PALETTE: NodeKind[] = ['ai_agent', 'condition', 'send_sms', 'http_request', 'delay'];

export const OPERATORS: [string, string][] = [
  ['contains', 'contains'],
  ['not_contains', 'does not contain'],
  ['equals', 'equals'],
  ['starts_with', 'starts with'],
  ['regex', 'matches regex'],
  ['is_empty', 'is empty'],
  ['not_empty', 'is not empty'],
];

export const TRIGGER_MATCH: [string, string][] = [
  ['any', 'Any message'],
  ['contains', 'Message contains'],
  ['starts_with', 'Message starts with'],
  ['equals', 'Message is exactly'],
  ['regex', 'Message matches regex'],
];

const s = (v: unknown) => (typeof v === 'string' ? v : v == null ? '' : String(v));
const clip = (v: string, n = 80) => (v.length > n ? v.slice(0, n) + '…' : v);

export function summarize(kind: string, data: Record<string, unknown>, providerOf?: (credId: string) => string | undefined): string {
  switch (kind) {
    case 'trigger': {
      const m = TRIGGER_MATCH.find(([v]) => v === s(data.match));
      const base = !data.match || data.match === 'any' ? 'Any incoming SMS' : `${m?.[1] ?? s(data.match)} "${s(data.pattern)}"`;
      return data.senderFilter ? `${base} from ${s(data.senderFilter)}` : base;
    }
    case 'ai_agent': {
      const provider = providerOf?.(s(data.credentialId));
      if (!data.credentialId) return 'Choose an API key and model';
      return `${provider ?? 'AI'} · ${s(data.model) || 'no model'}${data.memory ? ' · remembers chat' : ''}`;
    }
    case 'condition': {
      const op = OPERATORS.find(([v]) => v === s(data.operator))?.[1] ?? s(data.operator);
      return `${clip(s(data.value), 30)} ${op}${['is_empty', 'not_empty'].includes(s(data.operator)) ? '' : ` "${clip(s(data.compare), 30)}"`}`;
    }
    case 'send_sms':
      return `To ${s(data.to) === '{{trigger.sender}}' || !data.to ? 'the sender' : s(data.to)}: ${clip(s(data.body)) || '(empty)'}`;
    case 'http_request':
      return `${s(data.method) || 'POST'} ${clip(s(data.url), 60)}`;
    case 'delay':
      return `Wait ${s(data.seconds) || 0} seconds`;
  }
  return '';
}

export function nextId(kind: NodeKind, nodes: { id: string }[]): string {
  const prefix = NODE_META[kind].idPrefix;
  let n = 1;
  while (nodes.some((x) => x.id === `${prefix}_${n}`)) n++;
  return `${prefix}_${n}`;
}

/** Variables available to `nodeId`: trigger fields plus outputs of every upstream node. */
export function availableVars(graph: { nodes: GraphNode[]; edges: { source: string; target: string }[] }, nodeId: string): string[] {
  const vars = ['trigger.body', 'trigger.sender', 'trigger.sim_slot', 'trigger.received_at'];
  const seen = new Set<string>();
  const queue = [nodeId];
  while (queue.length) {
    const id = queue.shift()!;
    for (const e of graph.edges) {
      if (e.target === id && !seen.has(e.source)) {
        seen.add(e.source);
        queue.push(e.source);
      }
    }
  }
  for (const n of graph.nodes) {
    if (!seen.has(n.id)) continue;
    const meta = NODE_META[n.type as NodeKind];
    meta?.outputs.forEach((o) => vars.push(`${n.id}.${o}`));
  }
  return vars;
}

export type TemplateId = 'ai_reply' | 'keyword' | 'triage' | 'forward' | 'blank';

export const TEMPLATES: { id: TemplateId; title: string; description: string; icon: IconName; graph: Graph }[] = [
  {
    id: 'ai_reply',
    title: 'AI auto-reply agent',
    description: 'An AI model answers every incoming SMS, remembering the conversation with each contact.',
    icon: 'sparkles',
    graph: {
      nodes: [
        { id: 'trigger', type: 'trigger', position: { x: 40, y: 140 }, data: { ...NODE_META.trigger.defaults } },
        { id: 'ai_1', type: 'ai_agent', position: { x: 360, y: 120 }, data: { ...NODE_META.ai_agent.defaults, label: 'Write reply' } },
        { id: 'sms_1', type: 'send_sms', position: { x: 680, y: 140 }, data: { ...NODE_META.send_sms.defaults, label: 'Reply', body: '{{ai_1.text}}' } },
      ],
      edges: [
        { id: 'e1', source: 'trigger', target: 'ai_1' },
        { id: 'e2', source: 'ai_1', target: 'sms_1' },
      ],
    },
  },
  {
    id: 'keyword',
    title: 'Keyword auto-responder',
    description: 'Reply with a fixed message when an SMS starts with a keyword such as PRICE or HELP.',
    icon: 'message',
    graph: {
      nodes: [
        { id: 'trigger', type: 'trigger', position: { x: 40, y: 140 }, data: { label: 'Keyword HELP', match: 'starts_with', pattern: 'HELP' } },
        { id: 'sms_1', type: 'send_sms', position: { x: 380, y: 140 }, data: { ...NODE_META.send_sms.defaults, label: 'Help reply', body: 'Thanks for reaching out! Reply PRICE for prices or call us on +255 700 000 000.' } },
      ],
      edges: [{ id: 'e1', source: 'trigger', target: 'sms_1' }],
    },
  },
  {
    id: 'triage',
    title: 'AI triage + escalation',
    description: 'AI labels each SMS as urgent or not. Urgent ones go to your API; others get an acknowledgement.',
    icon: 'branch',
    graph: {
      nodes: [
        { id: 'trigger', type: 'trigger', position: { x: 20, y: 180 }, data: { ...NODE_META.trigger.defaults } },
        {
          id: 'ai_1', type: 'ai_agent', position: { x: 320, y: 160 },
          data: { ...NODE_META.ai_agent.defaults, label: 'Classify', memory: false, maxTokens: 10, systemPrompt: 'Classify the customer SMS. Answer with exactly one word: URGENT or NORMAL.' },
        },
        { id: 'if_1', type: 'condition', position: { x: 620, y: 175 }, data: { label: 'Is urgent?', value: '{{ai_1.text}}', operator: 'contains', compare: 'URGENT' } },
        { id: 'http_1', type: 'http_request', position: { x: 920, y: 60 }, data: { ...NODE_META.http_request.defaults, label: 'Alert my API', url: 'https://example.com/alerts' } },
        { id: 'sms_1', type: 'send_sms', position: { x: 920, y: 280 }, data: { ...NODE_META.send_sms.defaults, label: 'Acknowledge', body: "Thanks, we've received your message and will reply soon." } },
      ],
      edges: [
        { id: 'e1', source: 'trigger', target: 'ai_1' },
        { id: 'e2', source: 'ai_1', target: 'if_1' },
        { id: 'e3', source: 'if_1', sourceHandle: 'true', target: 'http_1' },
        { id: 'e4', source: 'if_1', sourceHandle: 'false', target: 'sms_1' },
      ],
    },
  },
  {
    id: 'forward',
    title: 'Forward SMS to an API',
    description: 'POST every incoming SMS as JSON to your own endpoint.',
    icon: 'globe',
    graph: {
      nodes: [
        { id: 'trigger', type: 'trigger', position: { x: 40, y: 140 }, data: { ...NODE_META.trigger.defaults } },
        { id: 'http_1', type: 'http_request', position: { x: 380, y: 140 }, data: { ...NODE_META.http_request.defaults, label: 'Forward', url: 'https://example.com/inbound-sms' } },
      ],
      edges: [{ id: 'e1', source: 'trigger', target: 'http_1' }],
    },
  },
  {
    id: 'blank',
    title: 'Start from scratch',
    description: 'An empty canvas with just the SMS trigger.',
    icon: 'plus',
    graph: { nodes: [{ id: 'trigger', type: 'trigger', position: { x: 80, y: 160 }, data: { ...NODE_META.trigger.defaults } }], edges: [] },
  },
];
