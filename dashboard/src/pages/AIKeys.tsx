import { useState } from 'react';
import { api, type Provider } from '../api';
import { Icon } from '../components/icons';
import { Topbar } from '../components/Layout';
import { Empty, Modal, Spinner, timeAgo, useAsync, useConfirm, useToast } from '../components/ui';

export const providerTone: Record<string, string> = { openai: 'green', anthropic: 'amber', gemini: 'blue', deepseek: 'violet' };

export function AIKeysPage() {
  const toast = useToast();
  const confirm = useConfirm();
  const creds = useAsync(() => api.credentials());
  const providers = useAsync(() => api.providers());
  const [adding, setAdding] = useState(false);

  const remove = async (id: string, label: string) => {
    if (!(await confirm({ title: `Delete "${label}"?`, body: 'Workflows using this key will fail until you pick another one.', confirmText: 'Delete', danger: true }))) return;
    try {
      await api.deleteCredential(id);
      creds.reload();
    } catch (e) {
      toast((e as Error).message, 'error');
    }
  };

  const name = (id: string) => providers.data?.find((p) => p.id === id)?.name ?? id;

  return (
    <>
      <Topbar title="AI keys" />
      <div className="content">
        <div className="page-head">
          <div>
            <h1>AI provider keys</h1>
            <p>Bring your own keys for the AI steps in your workflows. Usage is billed by the provider to your account.</p>
          </div>
          <button className="btn primary" onClick={() => setAdding(true)}><Icon name="plus" /> Add key</button>
        </div>

        <div className="callout" style={{ marginBottom: 16 }}>
          <Icon name="shield" />
          Keys are encrypted on the server and never shown again after you save them. Only you can use your keys; administrators can't see them.
        </div>

        <div className="card">
          {creds.loading && !creds.data ? <Spinner /> : creds.data?.length === 0 ? (
            <Empty icon="sparkles" title="No API keys yet" action={<button className="btn primary" onClick={() => setAdding(true)}><Icon name="plus" /> Add your first key</button>}>
              Add a key from OpenAI, Anthropic, Google Gemini or DeepSeek to use AI agents in workflows.
            </Empty>
          ) : (
            <div className="table-wrap">
              <table className="table">
                <thead><tr><th>Name</th><th>Provider</th><th>Key</th><th>Added</th><th /></tr></thead>
                <tbody>
                  {creds.data?.map((c) => (
                    <tr key={c.id}>
                      <td style={{ fontWeight: 600 }}>{c.label}{c.base_url && <div className="faint small mono">{c.base_url}</div>}</td>
                      <td><span className={`badge ${providerTone[c.provider] ?? ''}`}>{name(c.provider)}</span></td>
                      <td className="mono faint">{c.key_hint}</td>
                      <td className="faint small">{timeAgo(c.created_at)}</td>
                      <td className="actions"><button className="btn sm danger" onClick={() => remove(c.id, c.label)}><Icon name="trash" /> Delete</button></td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </div>
      {adding && providers.data && <AddKeyModal providers={providers.data} onClose={() => { setAdding(false); creds.reload(); }} />}
    </>
  );
}

export function AddKeyModal({ providers, onClose, onAdded }: { providers: Provider[]; onClose: () => void; onAdded?: (id: string) => void }) {
  const toast = useToast();
  const [provider, setProvider] = useState(providers[0]?.id ?? 'openai');
  const [label, setLabel] = useState('');
  const [key, setKey] = useState('');
  const [baseUrl, setBaseUrl] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const info = providers.find((p) => p.id === provider);

  const save = async () => {
    setBusy(true);
    setError('');
    try {
      const res = await api.createCredential({ provider, label: label.trim() || `${info?.name} key`, api_key: key.trim(), base_url: baseUrl.trim() || undefined });
      toast(`Key verified · ${res.models.length} models available`, 'success');
      onAdded?.(res.credential.id);
      onClose();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      title="Add an API key"
      description="We check the key with the provider before saving it."
      onClose={onClose}
      footer={<><button className="btn" onClick={onClose}>Cancel</button><button className="btn primary" onClick={save} disabled={busy || !key.trim()}>{busy ? 'Checking key…' : 'Verify & save'}</button></>}
    >
      {error && <div className="callout error"><Icon name="alert" />{error}</div>}
      <div className="field">
        <span className="label">Provider</span>
        <div className="grid" style={{ gridTemplateColumns: 'repeat(auto-fit, minmax(110px, 1fr))', gap: 8 }}>
          {providers.map((p) => (
            <button key={p.id} type="button" className={`btn ${provider === p.id ? 'primary' : ''}`} onClick={() => setProvider(p.id)}>{p.name}</button>
          ))}
        </div>
      </div>
      <div className="field">
        <label htmlFor="klabel">Name</label>
        <input id="klabel" className="input" placeholder={`${info?.name ?? ''} key`} value={label} onChange={(e) => setLabel(e.target.value)} />
      </div>
      <div className="field">
        <label htmlFor="kkey">API key</label>
        <input id="kkey" className="input mono" type="password" autoComplete="off" placeholder={info?.key_prefix ? `${info.key_prefix}…` : ''} value={key} onChange={(e) => setKey(e.target.value)} />
        {info?.key_url && <span className="hint">Get one at <a href={info.key_url} target="_blank" rel="noreferrer">{new URL(info.key_url).host} <Icon name="external" width={11} height={11} /></a></span>}
      </div>
      <details>
        <summary className="small muted" style={{ cursor: 'pointer' }}>Advanced: custom base URL</summary>
        <div className="field" style={{ marginTop: 10 }}>
          <input className="input mono" placeholder={info?.base_url_hint || 'Leave empty for the official endpoint'} value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} />
          <span className="hint">For proxies or OpenAI-compatible servers. Leave empty to use the provider's official API.</span>
        </div>
      </details>
    </Modal>
  );
}
