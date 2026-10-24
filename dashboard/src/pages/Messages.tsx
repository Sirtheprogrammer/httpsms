import { useEffect, useState } from 'react';
import { api, type Message } from '../api';
import { Icon } from '../components/icons';
import { Topbar } from '../components/Layout';
import { CopyField, Drawer, Empty, Spinner, StatusBadge, fmtDateTime, fmtTime, useAsync, useToast } from '../components/ui';
import { useLive } from '../live';

const PAGE = 50;

export function MessagesPage() {
  const [search, setSearch] = useState('');
  const [query, setQuery] = useState('');
  const [direction, setDirection] = useState('');
  const [limit, setLimit] = useState(PAGE);
  const [open, setOpen] = useState<Message | null>(null);

  useEffect(() => {
    const t = window.setTimeout(() => setQuery(search.trim()), 300);
    return () => window.clearTimeout(t);
  }, [search]);

  const list = useAsync(() => api.messages({ search: query, direction, limit }), [query, direction, limit]);
  useLive(['sms.received', 'sms.status', 'message.dispatched'], () => list.reload());

  const rows = list.data?.data ?? [];
  const total = list.data?.total ?? 0;

  return (
    <>
      <Topbar title="Messages" />
      <div className="content">
        <div className="page-head">
          <div>
            <h1>Messages</h1>
            <p>{total ? `${total.toLocaleString()} messages` : 'Everything your phones sent and received.'}</p>
          </div>
        </div>

        <div className="row wrap" style={{ marginBottom: 14 }}>
          <div style={{ position: 'relative', flex: '1 1 260px', maxWidth: 420 }}>
            <Icon name="search" width={16} height={16} style={{ position: 'absolute', left: 11, top: 11, color: 'var(--text-3)' }} />
            <input className="input" style={{ paddingLeft: 34 }} placeholder="Search number, text or ID" value={search} onChange={(e) => setSearch(e.target.value)} />
          </div>
          <div className="tabs" style={{ flex: '0 0 auto' }}>
            {[['', 'All'], ['inbound', 'Received'], ['outbound', 'Sent']].map(([v, l]) => (
              <button key={v} className={direction === v ? 'active' : ''} onClick={() => { setDirection(v); setLimit(PAGE); }}>{l}</button>
            ))}
          </div>
        </div>

        <div className="card">
          {list.loading && !list.data ? (
            <Spinner />
          ) : list.error ? (
            <div className="card-body"><div className="callout error"><Icon name="alert" />{list.error}</div></div>
          ) : rows.length === 0 ? (
            <Empty icon="inbox" title={query || direction ? 'No matching messages' : 'No messages yet'}>
              {query || direction ? 'Try a different search or filter.' : 'Incoming SMS on your paired phones and messages you send will show up here.'}
            </Empty>
          ) : (
            <div className="table-wrap">
              <table className="table">
                <thead>
                  <tr>
                    <th style={{ width: 120 }}>Time</th>
                    <th style={{ width: 170 }}>Contact</th>
                    <th>Message</th>
                    <th style={{ width: 110 }}>Status</th>
                  </tr>
                </thead>
                <tbody>
                  {rows.map((m) => (
                    <tr key={m.id} className="clickable" onClick={() => setOpen(m)}>
                      <td className="faint small" style={{ whiteSpace: 'nowrap' }}>{fmtTime(m.created_at)}</td>
                      <td>
                        <div className="row" style={{ gap: 8 }}>
                          <Icon name={m.direction === 'inbound' ? 'inbox' : 'send'} width={15} height={15} style={{ color: m.direction === 'inbound' ? 'var(--blue)' : 'var(--violet)', flexShrink: 0 }} />
                          <span className="mono" style={{ fontWeight: 600 }}>{m.direction === 'inbound' ? m.sender : m.recipient}</span>
                        </div>
                      </td>
                      <td><div className="truncate muted" style={{ maxWidth: 520 }}>{m.body}</div></td>
                      <td><StatusBadge status={m.status} /></td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          {rows.length < total && (
            <div className="card-foot" style={{ justifyContent: 'center' }}>
              <button className="btn" onClick={() => setLimit((l) => l + PAGE)}>Load more</button>
            </div>
          )}
        </div>
      </div>
      {open && <MessageDrawer message={open} onClose={() => setOpen(null)} />}
    </>
  );
}

function MessageDrawer({ message, onClose }: { message: Message; onClose: () => void }) {
  const toast = useToast();
  const contact = message.direction === 'inbound' ? message.sender : message.recipient;
  const thread = useAsync(() => api.messages({ contact, limit: 30 }), [contact]);
  useLive(['sms.received', 'sms.status'], () => thread.reload());
  const [reply, setReply] = useState('');
  const [sending, setSending] = useState(false);

  const send = async () => {
    setSending(true);
    try {
      await api.sendSms({ recipient: contact, body: reply, device_id: message.device_id || undefined, sim_slot: message.sim_slot });
      setReply('');
      thread.reload();
      toast('Reply queued', 'success');
    } catch (e) {
      toast((e as Error).message, 'error');
    } finally {
      setSending(false);
    }
  };

  return (
    <Drawer title={<span className="mono">{contact}</span>} onClose={onClose}>
      <dl className="kv">
        <dt>Status</dt><dd><StatusBadge status={message.status} /></dd>
        <dt>Direction</dt><dd>{message.direction === 'inbound' ? 'Received' : 'Sent'}</dd>
        <dt>SIM</dt><dd>SIM {message.sim_slot + 1} · {message.parts_count} part{message.parts_count === 1 ? '' : 's'}</dd>
        <dt>Time</dt><dd>{fmtDateTime(message.created_at)}</dd>
        {message.delivered_at && (<><dt>Delivered</dt><dd>{fmtDateTime(message.delivered_at)}</dd></>)}
        {message.error_message && (<><dt>Error</dt><dd style={{ color: 'var(--red)' }}>{message.error_message}</dd></>)}
      </dl>
      <div className="field">
        <span className="label">Message ID</span>
        <CopyField value={message.id} />
      </div>

      <div>
        <h3 style={{ marginBottom: 10 }}>Conversation</h3>
        <div className="thread">
          {thread.loading && !thread.data ? <Spinner /> : [...(thread.data?.data ?? [])].reverse().map((m) => (
            <div key={m.id} className={`bubble ${m.direction === 'outbound' ? 'out' : ''}`} style={m.id === message.id ? { outline: '2px solid var(--accent)', outlineOffset: 2 } : undefined}>
              {m.body}
              <div style={{ fontSize: 11, opacity: 0.7, marginTop: 4 }}>{fmtTime(m.created_at)}{m.direction === 'outbound' ? ` · ${m.status}` : ''}</div>
            </div>
          ))}
        </div>
      </div>

      <div className="field">
        <label htmlFor="reply">Reply</label>
        <textarea id="reply" className="textarea" rows={3} value={reply} onChange={(e) => setReply(e.target.value)} placeholder={`Message ${contact}`} />
        <div className="row" style={{ justifyContent: 'flex-end' }}>
          <button className="btn primary" disabled={!reply.trim() || sending} onClick={send}><Icon name="send" /> Send</button>
        </div>
      </div>
    </Drawer>
  );
}
