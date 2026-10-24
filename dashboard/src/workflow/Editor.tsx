import { useCallback, useEffect, useMemo, useRef, useState, type DragEvent } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import {
  ReactFlow, ReactFlowProvider, Background, Controls, MiniMap, Handle, Position, addEdge,
  useNodesState, useEdgesState, useReactFlow,
  type Connection, type Edge, type Node, type NodeProps,
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import { api, type AICredential, type Graph, type Provider, type WorkflowRun } from '../api';
import { Icon } from '../components/icons';
import { Topbar } from '../components/Layout';
import { Modal, Spinner, StatusBadge, Toggle, timeAgo, useAsync, useToast } from '../components/ui';
import { useLive } from '../live';
import { AddKeyModal } from '../pages/AIKeys';
import { NODE_META, OPERATORS, PALETTE, TRIGGER_MATCH, availableVars, nextId, summarize, type NodeKind } from './nodes';

type Data = Record<string, unknown>;
type FNode = Node<Data>;

// Shared lookups for node cards (provider names for AI nodes, server-reported problems).
interface EditorCtxValue { providerOf: (credId: string) => string | undefined }
let editorCtx: EditorCtxValue = { providerOf: () => undefined };

function FlowNode({ id, type, data, selected }: NodeProps<FNode>) {
  const meta = NODE_META[type as NodeKind];
  if (!meta) return null;
  const missing = (type === 'ai_agent' && (!data.credentialId || !data.model)) || (type === 'send_sms' && !String(data.body ?? '').trim());
  return (
    <div className={`wf-node ${selected ? 'selected' : ''} ${missing ? 'problem' : ''}`}>
      {type !== 'trigger' && <Handle type="target" position={Position.Left} />}
      <div className="wf-node-head">
        <span className="node-icon" style={{ background: `var(--${meta.tone}-soft)`, color: `var(--${meta.tone === 'accent' ? 'accent' : meta.tone})` }}>
          <Icon name={meta.icon} />
        </span>
        <div style={{ minWidth: 0, flex: 1 }}>
          <div className="kind">{meta.title}</div>
          <div className="title truncate">{String(data.label || meta.title)}</div>
        </div>
        <span className="wf-node-id">{id}</span>
      </div>
      <div className="wf-node-body">{summarize(type, data, editorCtx.providerOf)}</div>
      {type === 'condition' ? (
        <>
          <div className="wf-branches"><span /><span className="t">true</span></div>
          <div className="wf-branches"><span /><span className="f">false</span></div>
          <Handle id="true" type="source" position={Position.Right} className="true-handle" style={{ top: '62%' }} />
          <Handle id="false" type="source" position={Position.Right} className="false-handle" style={{ top: '84%' }} />
        </>
      ) : (
        <Handle type="source" position={Position.Right} />
      )}
    </div>
  );
}

const nodeTypes = Object.fromEntries(Object.keys(NODE_META).map((k) => [k, FlowNode]));

function toFlow(g: Graph): { nodes: FNode[]; edges: Edge[] } {
  return {
    nodes: g.nodes.map((n) => ({ id: n.id, type: n.type, position: n.position ?? { x: 0, y: 0 }, data: n.data ?? {}, deletable: n.type !== 'trigger' })),
    edges: g.edges.map((e, i) => ({ id: e.id || `e${i}`, source: e.source, target: e.target, sourceHandle: e.sourceHandle ?? undefined, animated: false })),
  };
}

function toGraph(nodes: FNode[], edges: Edge[]): Graph {
  return {
    nodes: nodes.map((n) => ({ id: n.id, type: n.type!, position: { x: Math.round(n.position.x), y: Math.round(n.position.y) }, data: n.data })),
    edges: edges.map((e) => ({ id: e.id, source: e.source, target: e.target, sourceHandle: e.sourceHandle ?? null })),
  };
}

export function WorkflowEditorPage() {
  return (
    <ReactFlowProvider>
      <Editor />
    </ReactFlowProvider>
  );
}

function Editor() {
  const { id = '' } = useParams();
  const toast = useToast();
  const navigate = useNavigate();
  const flow = useReactFlow();
  const wrapper = useRef<HTMLDivElement>(null);

  const [nodes, setNodes, onNodesChange] = useNodesState<FNode>([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([]);
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [enabled, setEnabled] = useState(false);
  const [problems, setProblems] = useState<string[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [dirty, setDirty] = useState(false);
  const [saving, setSaving] = useState(false);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [testing, setTesting] = useState(false);
  const [viewRun, setViewRun] = useState<WorkflowRun | null>(null);

  const creds = useAsync(() => api.credentials());
  const providers = useAsync(() => api.providers());
  const runs = useAsync(() => api.workflowRuns(id), [id]);
  useLive('workflow.run', (d) => d?.workflow_id === id && runs.reload());

  const providerName = useCallback(
    (credId: string) => {
      const c = creds.data?.find((x) => x.id === credId);
      return c ? providers.data?.find((p) => p.id === c.provider)?.name ?? c.provider : undefined;
    },
    [creds.data, providers.data],
  );
  editorCtx = { providerOf: providerName };

  useEffect(() => {
    api.workflow(id).then((wf) => {
      const f = toFlow(wf.graph);
      setNodes(f.nodes);
      setEdges(f.edges);
      setName(wf.name);
      setDescription(wf.description);
      setEnabled(wf.enabled);
      setProblems(wf.problems);
      setLoaded(true);
      window.setTimeout(() => flow.fitView({ padding: 0.25, maxZoom: 1 }), 50);
    }).catch((e) => {
      toast(e.message, 'error');
      navigate('/workflows');
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id]);

  // Warn before leaving with unsaved changes.
  useEffect(() => {
    const h = (e: BeforeUnloadEvent) => { if (dirty) { e.preventDefault(); e.returnValue = ''; } };
    window.addEventListener('beforeunload', h);
    return () => window.removeEventListener('beforeunload', h);
  }, [dirty]);

  const graph = useMemo(() => toGraph(nodes, edges), [nodes, edges]);
  const selected = nodes.find((n) => n.id === selectedId) ?? null;

  const save = useCallback(async (opts?: { enabled?: boolean }) => {
    setSaving(true);
    try {
      const wf = await api.updateWorkflow(id, { name, description, graph, enabled: opts?.enabled ?? enabled });
      setProblems(wf.problems);
      setEnabled(wf.enabled);
      setDirty(false);
      toast(opts?.enabled === true ? 'Workflow is live' : opts?.enabled === false ? 'Workflow paused' : 'Saved', 'success');
      return true;
    } catch (e: any) {
      if (e?.details?.problems) {
        setProblems(e.details.problems);
        toast('Fix the highlighted problems before turning it on', 'error');
      } else toast(e.message, 'error');
      return false;
    } finally {
      setSaving(false);
    }
  }, [id, name, description, graph, enabled, toast]);

  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key === 's') {
        e.preventDefault();
        save();
      }
    };
    window.addEventListener('keydown', h);
    return () => window.removeEventListener('keydown', h);
  }, [save]);

  const onConnect = useCallback((c: Connection) => {
    setEdges((es) => addEdge({ ...c, id: `e_${c.source}_${c.sourceHandle ?? 'out'}_${c.target}` }, es.filter((e) => !(e.source === c.source && (e.sourceHandle ?? null) === (c.sourceHandle ?? null) && e.target === c.target))));
    setDirty(true);
  }, [setEdges]);

  const addNode = (kind: NodeKind, position?: { x: number; y: number }) => {
    const nid = nextId(kind, nodes);
    let pos = position;
    if (!pos) {
      const anchor = nodes.find((n) => n.id === selectedId) ?? nodes[nodes.length - 1];
      pos = anchor ? { x: anchor.position.x + 320, y: anchor.position.y + (nodes.length % 2 ? 40 : -40) } : { x: 100, y: 100 };
    }
    const node: FNode = { id: nid, type: kind, position: pos, data: { ...NODE_META[kind].defaults } };
    if (kind === 'ai_agent' && creds.data?.length) {
      node.data.credentialId = creds.data[0].id;
      const prov = providers.data?.find((p) => p.id === creds.data![0].provider);
      if (prov?.default_model) node.data.model = prov.default_model;
    }
    setNodes((ns) => [...ns.map((n) => ({ ...n, selected: false })), { ...node, selected: true }]);
    // Auto-connect from the selected node so building a chain is click, click, click.
    if (selectedId && selected && selected.type !== 'condition') {
      setEdges((es) => [...es, { id: `e_${selectedId}_${nid}`, source: selectedId, target: nid }]);
    }
    setSelectedId(nid);
    setDirty(true);
  };

  const onDrop = (e: DragEvent) => {
    e.preventDefault();
    const kind = e.dataTransfer.getData('application/x-node') as NodeKind;
    if (!kind) return;
    addNode(kind, flow.screenToFlowPosition({ x: e.clientX - 120, y: e.clientY - 30 }));
  };

  const updateData = (patch: Data) => {
    if (!selectedId) return;
    setNodes((ns) => ns.map((n) => (n.id === selectedId ? { ...n, data: { ...n.data, ...patch } } : n)));
    setDirty(true);
  };

  const deleteSelected = () => {
    if (!selected || selected.type === 'trigger') return;
    setNodes((ns) => ns.filter((n) => n.id !== selected.id));
    setEdges((es) => es.filter((e) => e.source !== selected.id && e.target !== selected.id));
    setSelectedId(null);
    setDirty(true);
  };

  const colorMode = (document.documentElement.getAttribute('data-theme') as 'light' | 'dark' | null) ?? 'system';

  if (!loaded) return <><Topbar title="Workflow" /><Spinner /></>;

  return (
    <>
      <Topbar
        back={<Link to="/workflows" className="btn ghost sm icon" aria-label="Back to workflows"><Icon name="arrowLeft" /></Link>}
        title={<input className="wf-topbar-name" value={name} onChange={(e) => { setName(e.target.value); setDirty(true); }} aria-label="Workflow name" />}
      >
        {dirty && <span className="faint small hide-sm">Unsaved changes</span>}
        <div className="row small" style={{ gap: 8 }}>
          <span className="hide-sm">{enabled ? 'Live' : 'Off'}</span>
          <Toggle checked={enabled} onChange={(v) => save({ enabled: v })} label={enabled ? 'Turn off' : 'Turn on'} />
        </div>
        <button className="btn sm" onClick={() => setTesting(true)}><Icon name="play" /> Test</button>
        <button className="btn sm primary" onClick={() => save()} disabled={saving || !dirty}><Icon name="save" /> {saving ? 'Saving…' : 'Save'}</button>
      </Topbar>

      <div className="content wide">
        <div className="wf-editor">
          <aside className="wf-palette">
            <div className="faint small hide-sm palette-text" style={{ padding: '0 4px 4px' }}>Click or drag to add a step</div>
            {PALETTE.map((k) => {
              const m = NODE_META[k];
              return (
                <button key={k} className="palette-item" draggable title={m.description}
                  onDragStart={(e) => e.dataTransfer.setData('application/x-node', k)} onClick={() => addNode(k)}>
                  <span className="node-icon" style={{ background: `var(--${m.tone}-soft)`, color: `var(--${m.tone})` }}><Icon name={m.icon} /></span>
                  <span className="palette-text">
                    <div style={{ fontWeight: 600 }}>{m.title}</div>
                    <div className="desc">{m.description}</div>
                  </span>
                </button>
              );
            })}
          </aside>

          <div className="wf-canvas" ref={wrapper} onDragOver={(e) => e.preventDefault()} onDrop={onDrop}>
            <ReactFlow
              nodes={nodes}
              edges={edges}
              nodeTypes={nodeTypes}
              onNodesChange={(c) => { onNodesChange(c); if (c.some((x) => x.type !== 'select' && x.type !== 'dimensions')) setDirty(true); }}
              onEdgesChange={(c) => { onEdgesChange(c); if (c.some((x) => x.type === 'remove')) setDirty(true); }}
              onConnect={onConnect}
              onSelectionChange={({ nodes: sel }) => setSelectedId(sel[0]?.id ?? null)}
              onPaneClick={() => setSelectedId(null)}
              colorMode={colorMode}
              deleteKeyCode={['Backspace', 'Delete']}
              defaultEdgeOptions={{ type: 'smoothstep' }}
              fitView
              proOptions={{ hideAttribution: true }}
            >
              <Background gap={20} size={1.2} />
              <Controls showInteractive={false} />
              <MiniMap pannable zoomable className="hide-sm" nodeColor="var(--accent-fill)" nodeStrokeColor="var(--on-accent)" maskColor="rgba(0, 0, 0, 0.25)" bgColor="var(--surface)" />
            </ReactFlow>
          </div>

          <aside className="wf-inspector">
            {selected ? (
              <NodeInspector
                node={selected}
                graph={graph}
                creds={creds.data ?? []}
                providers={providers.data ?? []}
                onChange={updateData}
                onDelete={deleteSelected}
                onKeyAdded={() => creds.reload()}
              />
            ) : (
              <WorkflowPanel
                description={description}
                setDescription={(v) => { setDescription(v); setDirty(true); }}
                problems={problems}
                runs={runs.data ?? []}
                onOpenRun={setViewRun}
              />
            )}
          </aside>
        </div>
      </div>

      {testing && <TestModal workflowId={id} graph={graph} onClose={() => setTesting(false)} onRan={() => runs.reload()} />}
      {viewRun && (
        <Modal title="Run details" description={`${new Date(viewRun.started_at).toLocaleString()}${viewRun.dry_run ? ' · test run' : ''}`} onClose={() => setViewRun(null)} size="lg">
          <RunSteps run={viewRun} />
        </Modal>
      )}
    </>
  );
}

// ---------- Inspector ----------

function TemplateField({ label, value, onChange, vars, rows = 3, mono, placeholder, hint }: {
  label: string; value: string; onChange: (v: string) => void; vars: string[]; rows?: number; mono?: boolean; placeholder?: string; hint?: string;
}) {
  const ref = useRef<HTMLTextAreaElement>(null);
  const insert = (v: string) => {
    const el = ref.current;
    const token = `{{${v}}}`;
    if (!el) return onChange(value + token);
    const start = el.selectionStart ?? value.length;
    const end = el.selectionEnd ?? value.length;
    onChange(value.slice(0, start) + token + value.slice(end));
    window.requestAnimationFrame(() => {
      el.focus();
      el.setSelectionRange(start + token.length, start + token.length);
    });
  };
  return (
    <div className="field">
      <label>{label}</label>
      <textarea ref={ref} className={`textarea ${mono ? 'mono' : ''}`} rows={rows} value={value} placeholder={placeholder} onChange={(e) => onChange(e.target.value)} />
      {hint && <span className="hint">{hint}</span>}
      <div className="var-chips">
        {vars.map((v) => <button key={v} type="button" className="var-chip" onClick={() => insert(v)} title={`Insert {{${v}}}`}>{v}</button>)}
      </div>
    </div>
  );
}

function NodeInspector({ node, graph, creds, providers, onChange, onDelete, onKeyAdded }: {
  node: FNode; graph: Graph; creds: AICredential[]; providers: Provider[];
  onChange: (p: Data) => void; onDelete: () => void; onKeyAdded: () => void;
}) {
  const meta = NODE_META[node.type as NodeKind];
  const d = node.data;
  const s = (k: string) => (d[k] == null ? '' : String(d[k]));
  const vars = useMemo(() => availableVars(graph, node.id), [graph, node.id]);

  return (
    <>
      <div className="row">
        <span className="node-icon" style={{ background: `var(--${meta.tone}-soft)`, color: `var(--${meta.tone})` }}><Icon name={meta.icon} /></span>
        <div className="grow">
          <h2>{meta.title}</h2>
          <div className="faint small">{meta.description}</div>
        </div>
      </div>
      <div className="field">
        <label htmlFor="nlabel">Step name</label>
        <input id="nlabel" className="input" value={s('label')} onChange={(e) => onChange({ label: e.target.value })} />
        <span className="hint">Reference its output as <code>{`{{${node.id}.${meta.outputs[0] ?? 'text'}}}`}</code></span>
      </div>

      {node.type === 'trigger' && (
        <>
          <div className="field">
            <label htmlFor="tmatch">Run when</label>
            <select id="tmatch" className="select" value={s('match') || 'any'} onChange={(e) => onChange({ match: e.target.value })}>
              {TRIGGER_MATCH.map(([v, l]) => <option key={v} value={v}>{l}</option>)}
            </select>
          </div>
          {s('match') && s('match') !== 'any' && (
            <div className="field">
              <label htmlFor="tpat">{s('match') === 'regex' ? 'Regular expression' : 'Text'}</label>
              <input id="tpat" className="input mono" value={s('pattern')} onChange={(e) => onChange({ pattern: e.target.value })} placeholder={s('match') === 'regex' ? '^(STOP|UNSUBSCRIBE)' : 'HELP'} />
              {s('match') !== 'regex' && (
                <label className="checkbox"><input type="checkbox" checked={!!d.caseSensitive} onChange={(e) => onChange({ caseSensitive: e.target.checked })} />Case sensitive</label>
              )}
            </div>
          )}
          <div className="field">
            <label htmlFor="tsender">Only from numbers containing</label>
            <input id="tsender" className="input mono" value={s('senderFilter')} onChange={(e) => onChange({ senderFilter: e.target.value })} placeholder="Leave empty for any sender" />
          </div>
          <div className="callout small"><Icon name="info" />A workflow pauses for a sender after 10 runs in 10 minutes, so two auto-replying phones can't loop forever.</div>
        </>
      )}

      {node.type === 'ai_agent' && <AIInspector data={d} onChange={onChange} creds={creds} providers={providers} vars={vars} onKeyAdded={onKeyAdded} />}

      {node.type === 'condition' && (
        <>
          <TemplateField label="Value to check" value={s('value')} onChange={(v) => onChange({ value: v })} vars={vars} rows={2} mono />
          <div className="field">
            <label htmlFor="cop">Operator</label>
            <select id="cop" className="select" value={s('operator') || 'contains'} onChange={(e) => onChange({ operator: e.target.value })}>
              {OPERATORS.map(([v, l]) => <option key={v} value={v}>{l}</option>)}
            </select>
          </div>
          {!['is_empty', 'not_empty'].includes(s('operator')) && (
            <div className="field">
              <label htmlFor="ccmp">Compare with</label>
              <input id="ccmp" className="input mono" value={s('compare')} onChange={(e) => onChange({ compare: e.target.value })} />
              <label className="checkbox"><input type="checkbox" checked={!!d.caseSensitive} onChange={(e) => onChange({ caseSensitive: e.target.checked })} />Case sensitive</label>
            </div>
          )}
          <p className="hint">Connect the green handle for the <b>true</b> path and the red one for <b>false</b>.</p>
        </>
      )}

      {node.type === 'send_sms' && (
        <>
          <div className="field">
            <label htmlFor="sto">To</label>
            <input id="sto" className="input mono" value={s('to')} onChange={(e) => onChange({ to: e.target.value })} placeholder="{{trigger.sender}}" />
            <span className="hint"><code>{'{{trigger.sender}}'}</code> replies to whoever texted. Sent from the phone that received it.</span>
          </div>
          <TemplateField label="Message" value={s('body')} onChange={(v) => onChange({ body: v })} vars={vars} rows={5} placeholder="Thanks for your message!" />
        </>
      )}

      {node.type === 'http_request' && (
        <>
          <div className="form-grid" style={{ gridTemplateColumns: '100px 1fr' }}>
            <div className="field">
              <label htmlFor="hm">Method</label>
              <select id="hm" className="select" value={s('method') || 'POST'} onChange={(e) => onChange({ method: e.target.value })}>
                {['GET', 'POST', 'PUT', 'PATCH', 'DELETE'].map((m) => <option key={m}>{m}</option>)}
              </select>
            </div>
            <div className="field">
              <label htmlFor="hu">URL</label>
              <input id="hu" className="input mono" value={s('url')} onChange={(e) => onChange({ url: e.target.value })} />
            </div>
          </div>
          <TemplateField label="Headers (one per line)" value={s('headers')} onChange={(v) => onChange({ headers: v })} vars={[]} rows={2} mono placeholder="Authorization: Bearer …" />
          {s('method') !== 'GET' && <TemplateField label="JSON body" value={s('body')} onChange={(v) => onChange({ body: v })} vars={vars} rows={6} mono />}
          <label className="checkbox"><input type="checkbox" checked={!!d.continueOnError} onChange={(e) => onChange({ continueOnError: e.target.checked })} />Continue if the server returns an error</label>
        </>
      )}

      {node.type === 'delay' && (
        <div className="field">
          <label htmlFor="dsec">Seconds</label>
          <input id="dsec" className="input" type="number" min={1} max={300} value={s('seconds')} onChange={(e) => onChange({ seconds: Number(e.target.value) })} />
          <span className="hint">Up to 300 seconds. Useful to make auto-replies feel less instant.</span>
        </div>
      )}

      {node.type !== 'trigger' && (
        <button className="btn danger" onClick={onDelete} style={{ marginTop: 'auto' }}><Icon name="trash" /> Delete step</button>
      )}
    </>
  );
}

const modelCache = new Map<string, string[]>();

function AIInspector({ data, onChange, creds, providers, vars, onKeyAdded }: {
  data: Data; onChange: (p: Data) => void; creds: AICredential[]; providers: Provider[]; vars: string[]; onKeyAdded: () => void;
}) {
  const s = (k: string) => (data[k] == null ? '' : String(data[k]));
  const credId = s('credentialId');
  const cred = creds.find((c) => c.id === credId);
  const [models, setModels] = useState<string[]>(modelCache.get(credId) ?? []);
  const [loadingModels, setLoadingModels] = useState(false);
  const [modelError, setModelError] = useState('');
  const [adding, setAdding] = useState(false);

  useEffect(() => {
    if (!credId) return;
    if (modelCache.has(credId)) {
      setModels(modelCache.get(credId)!);
      return;
    }
    setLoadingModels(true);
    setModelError('');
    api.credentialModels(credId)
      .then((r) => { modelCache.set(credId, r.models); setModels(r.models); })
      .catch((e) => setModelError(e.message))
      .finally(() => setLoadingModels(false));
  }, [credId]);

  const pickCred = (id: string) => {
    const c = creds.find((x) => x.id === id);
    const def = providers.find((p) => p.id === c?.provider)?.default_model ?? '';
    onChange({ credentialId: id, model: def });
  };

  return (
    <>
      {creds.length === 0 ? (
        <div className="callout warn">
          <Icon name="key" />
          <div>
            Add an API key from OpenAI, Anthropic, Gemini or DeepSeek to use AI steps.
            <div style={{ marginTop: 8 }}><button className="btn sm primary" onClick={() => setAdding(true)}><Icon name="plus" /> Add API key</button></div>
          </div>
        </div>
      ) : (
        <div className="field">
          <label htmlFor="acred">API key</label>
          <div className="row">
            <select id="acred" className="select grow" value={credId} onChange={(e) => pickCred(e.target.value)}>
              <option value="">Choose a key…</option>
              {creds.map((c) => <option key={c.id} value={c.id}>{c.label} ({providers.find((p) => p.id === c.provider)?.name ?? c.provider})</option>)}
            </select>
            <button className="btn icon" onClick={() => setAdding(true)} title="Add another key"><Icon name="plus" /></button>
          </div>
        </div>
      )}
      {cred && (
        <div className="field">
          <label htmlFor="amodel">Model {loadingModels && <span className="spinner" style={{ display: 'inline-block', width: 12, height: 12, verticalAlign: 'middle' }} />}</label>
          <input id="amodel" className="input mono" list="model-options" value={s('model')} onChange={(e) => onChange({ model: e.target.value })} placeholder="Type or pick a model" />
          <datalist id="model-options">{models.map((m) => <option key={m} value={m} />)}</datalist>
          {modelError ? <span className="hint error">{modelError}</span> : <span className="hint">{models.length ? `${models.length} models available on this key` : 'Enter the model ID'}</span>}
        </div>
      )}
      <TemplateField label="Instructions (system prompt)" value={s('systemPrompt')} onChange={(v) => onChange({ systemPrompt: v })} vars={vars} rows={5}
        hint="Who the agent is and how it should reply. SMS replies should be short and plain text." />
      <TemplateField label="Prompt" value={s('prompt')} onChange={(v) => onChange({ prompt: v })} vars={vars} rows={3} />
      <div className="form-grid">
        <div className="field">
          <label htmlFor="amax">Max output tokens</label>
          <input id="amax" className="input" type="number" min={16} max={8000} value={s('maxTokens')} onChange={(e) => onChange({ maxTokens: Number(e.target.value) })} />
        </div>
        <div className="field">
          <label htmlFor="aturns">Memory (messages)</label>
          <input id="aturns" className="input" type="number" min={0} max={50} value={s('memoryTurns')} disabled={!data.memory} onChange={(e) => onChange({ memoryTurns: Number(e.target.value) })} />
        </div>
      </div>
      <label className="checkbox">
        <input type="checkbox" checked={!!data.memory} onChange={(e) => onChange({ memory: e.target.checked })} />
        <span>Remember the conversation<div className="faint small">Sends earlier SMS with this contact so the agent has context.</div></span>
      </label>
      {adding && providers.length > 0 && (
        <AddKeyModal providers={providers} onClose={() => setAdding(false)} onAdded={(newId) => { onKeyAdded(); window.setTimeout(() => pickCred(newId), 300); }} />
      )}
    </>
  );
}

function WorkflowPanel({ description, setDescription, problems, runs, onOpenRun }: {
  description: string; setDescription: (v: string) => void; problems: string[]; runs: WorkflowRun[]; onOpenRun: (r: WorkflowRun) => void;
}) {
  return (
    <>
      <div>
        <h2>Workflow</h2>
        <p className="faint small" style={{ marginTop: 4 }}>Select a step to edit it. Drag between handles to connect steps.</p>
      </div>
      <div className="field">
        <label htmlFor="wdesc">Description</label>
        <textarea id="wdesc" className="textarea" rows={2} value={description} onChange={(e) => setDescription(e.target.value)} placeholder="What does this workflow do?" />
      </div>
      {problems.length > 0 ? (
        <div className="callout warn">
          <Icon name="alert" />
          <div>
            <b>Before turning it on</b>
            <ul style={{ margin: '6px 0 0', paddingLeft: 18 }}>{problems.map((p) => <li key={p}>{p}</li>)}</ul>
            <div className="small" style={{ marginTop: 4 }}>Saved state; save again after fixing.</div>
          </div>
        </div>
      ) : (
        <div className="callout success"><Icon name="check" />Ready to go live.</div>
      )}
      <div>
        <h3 style={{ marginBottom: 8 }}>Recent runs</h3>
        {runs.length === 0 ? <p className="faint small">No runs yet. Use <b>Test</b> to try it with a sample SMS.</p> : (
          <div className="stack" style={{ gap: 6 }}>
            {runs.map((r) => (
              <button key={r.id} className="palette-item" style={{ cursor: 'pointer', justifyContent: 'space-between' }} onClick={() => onOpenRun(r)}>
                <span className="small">{timeAgo(r.started_at)}{r.dry_run ? ' · test' : ''}<div className="faint truncate" style={{ maxWidth: 170 }}>{r.error || `${r.steps?.length ?? 0} step${r.steps?.length === 1 ? '' : 's'}`}</div></span>
                <StatusBadge status={r.status} />
              </button>
            ))}
          </div>
        )}
      </div>
    </>
  );
}

// ---------- Testing ----------

function TestModal({ workflowId, graph, onClose, onRan }: { workflowId: string; graph: Graph; onClose: () => void; onRan: () => void }) {
  const [sender, setSender] = useState('+255700000001');
  const [body, setBody] = useState('Hi, what are your opening hours?');
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<{ matched: boolean; run?: WorkflowRun } | null>(null);
  const [error, setError] = useState('');

  const run = async () => {
    setBusy(true);
    setError('');
    setResult(null);
    try {
      setResult(await api.testWorkflow(workflowId, { sender, body, graph }));
      onRan();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal title="Test workflow" description="Runs your current canvas (saved or not) with a sample SMS. AI steps really call the provider; SMS and non-GET HTTP steps are only simulated." onClose={onClose} size="lg"
      footer={<><button className="btn" onClick={onClose}>Close</button><button className="btn primary" onClick={run} disabled={busy}><Icon name="play" /> {busy ? 'Running…' : 'Run test'}</button></>}>
      <div className="form-grid" style={{ gridTemplateColumns: '200px 1fr' }}>
        <div className="field">
          <label htmlFor="tsnd">From</label>
          <input id="tsnd" className="input mono" value={sender} onChange={(e) => setSender(e.target.value)} />
        </div>
        <div className="field">
          <label htmlFor="tbody">Incoming message</label>
          <input id="tbody" className="input" value={body} onChange={(e) => setBody(e.target.value)} onKeyDown={(e) => e.key === 'Enter' && run()} />
        </div>
      </div>
      {error && <div className="callout error"><Icon name="alert" />{error}</div>}
      {busy && <Spinner />}
      {result && !result.matched && <div className="callout warn"><Icon name="info" />The trigger's filter doesn't match this message, so the workflow would not run.</div>}
      {result?.run && <RunSteps run={result.run} />}
    </Modal>
  );
}

function RunSteps({ run }: { run: WorkflowRun }) {
  return (
    <div>
      <div className="row" style={{ marginBottom: 6 }}>
        <StatusBadge status={run.status} />
        {run.error && <span className="small" style={{ color: 'var(--red)' }}>{run.error}</span>}
      </div>
      {(run.steps ?? []).length === 0 && <p className="faint small">No steps ran. Is the trigger connected to anything?</p>}
      {(run.steps ?? []).map((s, i) => {
        const meta = NODE_META[s.type as NodeKind];
        const out = s.output ?? {};
        const main = out.text ?? out.body ?? out.result ?? '';
        return (
          <div key={i} className="run-step">
            <span className="node-icon" style={{ background: `var(--${meta?.tone ?? 'accent'}-soft)`, color: `var(--${meta?.tone ?? 'accent'})` }}><Icon name={meta?.icon ?? 'zap'} /></span>
            <div className="grow" style={{ minWidth: 0 }}>
              <div className="row" style={{ justifyContent: 'space-between' }}>
                <b>{s.label}</b>
                <span className="row small faint" style={{ gap: 6 }}>{s.duration_ms} ms <StatusBadge status={s.status} /></span>
              </div>
              {s.error && <div className="small" style={{ color: 'var(--red)' }}>{s.error}</div>}
              {s.type === 'send_sms' && <div className="small muted">{s.status === 'simulated' ? 'Would send' : 'Sent'} to <span className="mono">{out.to}</span></div>}
              {s.type === 'condition' && <div className="small muted">Took the <b>{out.result}</b> path</div>}
              {s.type === 'ai_agent' && out.model && <div className="small faint">{out.model} · {out.input_tokens} in / {out.output_tokens} out tokens</div>}
              {s.type === 'http_request' && out.status && <div className="small faint">HTTP {out.status}</div>}
              {main && <pre>{main}</pre>}
            </div>
          </div>
        );
      })}
    </div>
  );
}
