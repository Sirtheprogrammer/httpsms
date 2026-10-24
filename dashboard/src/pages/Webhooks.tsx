import { useState } from 'react';
import { api } from '../api';
import { Icon } from '../components/icons';
import { Topbar } from '../components/Layout';
import { CopyField, Empty, Modal, Spinner, StatusBadge, fmtTime, useAsync, useConfirm, useToast } from '../components/ui';

const EVENTS = [
  ['sms.received', 'An SMS arrives on a phone'],
  ['sms.sent', 'A phone handed an SMS to the carrier'],
  ['sms.delivered', 'The carrier confirmed delivery'],
  ['sms.failed', 'Sending failed'],
  ['device.heartbeat', 'Phone battery and signal updates'],
];

export function WebhooksPage() {
  const toast = useToast();
  const confirm = useConfirm();
  const hooks = useAsync(() => api.webhooks());
  const deliveries = useAsync(() => api.deliveries());
  const [adding, setAdding] = useState(false);
  const [created, setCreated] = useState<{ url: string; secret: string } | null>(null);

  const test = async (id: string) => {
    try {
      const d = await api.testWebhook(id);
      toast(d.status === 'success' ? `Delivered · HTTP ${d.response_status} in ${d.latency_ms} ms` : `Failed · HTTP ${d.response_status || 'no response'}`, d.status === 'success' ? 'success' : 'error');
      deliveries.reload();
    } catch (e) {
      toast((e as Error).message, 'error');
    }
  };
  const remove = async (id: string, url: string) => {
    if (!(await confirm({ title: 'Delete webhook?', body: url, confirmText: 'Delete', danger: true }))) return;
    await api.deleteWebhook(id).catch((e) => toast(e.message, 'error'));
    hooks.reload();
  };

  return (
    <>
      <Topbar title="Webhooks" />
      <div className="content stack" style={{ gap: 20 }}>
        <div className="page-head" style={{ marginBottom: 0 }}>
          <div>
            <h1>Webhooks</h1>
            <p>Get signed HTTP POSTs when SMS events happen. Verify the <code>X-Signature-SHA256</code> header with your secret.</p>
          </div>
          <button className="btn primary" onClick={() => setAdding(true)}><Icon name="plus" /> Add webhook</button>
        </div>

        <div className="card">
          {hooks.loading && !hooks.data ? <Spinner /> : hooks.data?.length === 0 ? (
            <Empty icon="webhook" title="No webhooks" action={<button className="btn primary" onClick={() => setAdding(true)}><Icon name="plus" /> Add webhook</button>}>
              Forward incoming SMS and delivery updates to your own app.
            </Empty>
          ) : (
            <div className="table-wrap">
              <table className="table">
                <thead><tr><th>Endpoint</th><th>Events</th><th>Status</th><th /></tr></thead>
                <tbody>
                  {hooks.data?.map((w) => (
                    <tr key={w.id}>
                      <td className="mono" style={{ wordBreak: 'break-all' }}>{w.url}</td>
                      <td><div className="row wrap" style={{ gap: 4 }}>{w.events.map((e) => <span key={e} className="badge">{e}</span>)}</div></td>
                      <td><StatusBadge status={w.is_active ? 'success' : 'offline'} /></td>
                      <td className="actions">
                        <button className="btn sm" onClick={() => test(w.id)}><Icon name="play" /> Test</button>{' '}
                        <button className="btn sm danger icon" onClick={() => remove(w.id, w.url)} aria-label="Delete"><Icon name="trash" /></button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>

        <div className="card">
          <div className="card-head"><h2>Recent deliveries</h2><button className="btn sm ghost" onClick={deliveries.reload}><Icon name="refresh" /> Refresh</button></div>
          {deliveries.data?.length === 0 ? <div className="card-body muted small">No deliveries yet.</div> : (
            <div className="table-wrap">
              <table className="table">
                <thead><tr><th>Time</th><th>Event</th><th>Result</th><th>Response</th><th>Attempt</th></tr></thead>
                <tbody>
                  {deliveries.data?.map((d) => (
                    <tr key={d.id}>
                      <td className="faint small">{fmtTime(d.created_at)}</td>
                      <td className="mono">{d.event_type}</td>
                      <td><StatusBadge status={d.status} /></td>
                      <td className="mono small">{d.response_status ? `HTTP ${d.response_status}` : '—'} · {d.latency_ms} ms</td>
                      <td className="small">{d.attempt}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </div>
      {adding && <AddWebhook onClose={() => setAdding(false)} onCreated={(w) => { setAdding(false); setCreated(w); hooks.reload(); }} />}
      {created && (
        <Modal title="Webhook created" onClose={() => setCreated(null)} footer={<button className="btn primary" onClick={() => setCreated(null)}>Done</button>}>
          <p className="muted">Use this secret to verify the <code>X-Signature-SHA256</code> HMAC on each request.</p>
          <CopyField value={created.secret} />
        </Modal>
      )}
    </>
  );
}

function AddWebhook({ onClose, onCreated }: { onClose: () => void; onCreated: (w: { url: string; secret: string }) => void }) {
  const toast = useToast();
  const [url, setUrl] = useState('');
  const [secret, setSecret] = useState('');
  const [events, setEvents] = useState<string[]>(['sms.received', 'sms.sent', 'sms.delivered', 'sms.failed']);
  const save = async () => {
    try {
      const w = await api.createWebhook({ url: url.trim(), secret: secret.trim() || undefined, events });
      onCreated({ url: w.url, secret: w.secret ?? '' });
    } catch (e) {
      toast((e as Error).message, 'error');
    }
  };
  return (
    <Modal title="Add webhook" onClose={onClose} footer={<><button className="btn" onClick={onClose}>Cancel</button><button className="btn primary" disabled={!url.trim() || events.length === 0} onClick={save}>Add webhook</button></>}>
      <div className="field">
        <label htmlFor="wurl">Endpoint URL</label>
        <input id="wurl" className="input mono" placeholder="https://example.com/sms-webhook" value={url} onChange={(e) => setUrl(e.target.value)} />
      </div>
      <div className="field">
        <label htmlFor="wsec">Signing secret</label>
        <input id="wsec" className="input mono" placeholder="Leave empty to generate one" value={secret} onChange={(e) => setSecret(e.target.value)} />
      </div>
      <div className="field">
        <span className="label">Events</span>
        {EVENTS.map(([v, d]) => (
          <label key={v} className="checkbox">
            <input type="checkbox" checked={events.includes(v)} onChange={(e) => setEvents((xs) => (e.target.checked ? [...xs, v] : xs.filter((x) => x !== v)))} />
            <span><span className="mono">{v}</span> <span className="faint">· {d}</span></span>
          </label>
        ))}
      </div>
    </Modal>
  );
}
