import { useState } from 'react';
import { api } from '../api';
import { Icon } from '../components/icons';
import { Topbar } from '../components/Layout';
import { CopyField, Empty, Modal, Spinner, timeAgo, useAsync, useConfirm, useToast } from '../components/ui';

const SCOPES = [
  ['sms:send', 'Send SMS'],
  ['sms:read', 'Read messages and stats'],
  ['devices:manage', 'Pair and manage phones'],
  ['webhooks:manage', 'Manage webhooks'],
  ['workflows:manage', 'Manage workflows and AI keys'],
];

export function TokensPage() {
  const toast = useToast();
  const confirm = useConfirm();
  const tokens = useAsync(() => api.tokens());
  const [adding, setAdding] = useState(false);
  const [key, setKey] = useState<string | null>(null);

  const revoke = async (id: string, name: string) => {
    if (!(await confirm({ title: `Revoke "${name}"?`, body: 'Apps using this token stop working immediately.', confirmText: 'Revoke', danger: true }))) return;
    await api.deleteToken(id).catch((e) => toast(e.message, 'error'));
    tokens.reload();
  };

  const origin = location.origin;
  return (
    <>
      <Topbar title="API tokens" />
      <div className="content stack" style={{ gap: 20 }}>
        <div className="page-head" style={{ marginBottom: 0 }}>
          <div>
            <h1>API tokens</h1>
            <p>Let your own apps send and read SMS. Tokens act as your account and only see your data.</p>
          </div>
          <button className="btn primary" onClick={() => setAdding(true)}><Icon name="plus" /> New token</button>
        </div>

        <div className="card">
          {tokens.loading && !tokens.data ? <Spinner /> : tokens.data?.length === 0 ? (
            <Empty icon="code" title="No API tokens">Create a token to call the REST API from your backend.</Empty>
          ) : (
            <div className="table-wrap">
              <table className="table">
                <thead><tr><th>Name</th><th>Token</th><th>Scopes</th><th>Last used</th><th /></tr></thead>
                <tbody>
                  {tokens.data?.map((t) => (
                    <tr key={t.id}>
                      <td style={{ fontWeight: 600 }}>{t.name}</td>
                      <td className="mono faint">{t.token_prefix}</td>
                      <td><div className="row wrap" style={{ gap: 4 }}>{t.scopes.map((s) => <span key={s} className="badge">{s === '*' ? 'full access' : s}</span>)}</div></td>
                      <td className="faint small">{timeAgo(t.last_used_at)}</td>
                      <td className="actions"><button className="btn sm danger" onClick={() => revoke(t.id, t.name)}>Revoke</button></td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>

        <div className="card">
          <div className="card-head"><h2>Quick start</h2></div>
          <div className="card-body">
            <pre className="mono" style={{ margin: 0, padding: 14, background: 'var(--surface-2)', borderRadius: 8, overflowX: 'auto', fontSize: 12.5 }}>{`curl -X POST ${origin}/api/v1/sms/send \\
  -H "Authorization: Bearer htsms_live_…" \\
  -H "Content-Type: application/json" \\
  -d '{"recipient": "+255712345678", "body": "Hello from HttpSMS"}'`}</pre>
          </div>
        </div>
      </div>
      {adding && <NewToken onClose={() => setAdding(false)} onCreated={(k) => { setAdding(false); setKey(k); tokens.reload(); }} />}
      {key && (
        <Modal title="Copy your token" description="It's stored hashed, so this is the only time you'll see it." onClose={() => setKey(null)} footer={<button className="btn primary" onClick={() => setKey(null)}>I've saved it</button>}>
          <CopyField value={key} />
        </Modal>
      )}
    </>
  );
}

function NewToken({ onClose, onCreated }: { onClose: () => void; onCreated: (key: string) => void }) {
  const toast = useToast();
  const [name, setName] = useState('');
  const [scopes, setScopes] = useState<string[]>(['sms:send', 'sms:read']);
  const save = async () => {
    try {
      const res = await api.createToken(name.trim() || 'API token', scopes);
      onCreated(res.key);
    } catch (e) {
      toast((e as Error).message, 'error');
    }
  };
  return (
    <Modal title="New API token" onClose={onClose} footer={<><button className="btn" onClick={onClose}>Cancel</button><button className="btn primary" onClick={save} disabled={scopes.length === 0}>Create token</button></>}>
      <div className="field">
        <label htmlFor="tname">Name</label>
        <input id="tname" className="input" placeholder="e.g. Production backend" value={name} onChange={(e) => setName(e.target.value)} autoFocus />
      </div>
      <div className="field">
        <span className="label">Permissions</span>
        {SCOPES.map(([v, d]) => (
          <label key={v} className="checkbox">
            <input type="checkbox" checked={scopes.includes(v)} onChange={(e) => setScopes((xs) => (e.target.checked ? [...xs, v] : xs.filter((x) => x !== v)))} />
            <span>{d} <span className="faint mono small">{v}</span></span>
          </label>
        ))}
      </div>
    </Modal>
  );
}
