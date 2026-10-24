import { Link } from 'react-router-dom';
import { api } from '../api';
import { useAuth } from '../auth';
import { Icon, type IconName } from '../components/icons';
import { Topbar } from '../components/Layout';
import { Empty, StatusBadge, fmtTime, timeAgo, useAsync } from '../components/ui';
import { useLive } from '../live';

function Stat({ icon, tone, label, value, sub }: { icon: IconName; tone: string; label: string; value: number | string; sub?: string }) {
  return (
    <div className="card stat">
      <div className="label">
        <span className="stat-icon" style={{ background: `var(--${tone}-soft)`, color: `var(--${tone})` }}><Icon name={icon} /></span>
        {label}
      </div>
      <div className="value">{value}</div>
      {sub && <div className="sub">{sub}</div>}
    </div>
  );
}

export function OverviewPage() {
  const { user } = useAuth();
  const stats = useAsync(() => api.stats());
  const recent = useAsync(() => api.messages({ limit: 8 }));
  const devices = useAsync(() => api.devices());
  const runs = useAsync(() => api.workflowRuns());

  useLive(['sms.received', 'sms.status', 'message.dispatched'], () => {
    stats.reload();
    recent.reload();
  });
  useLive(['device.status', 'device.heartbeat'], () => {
    stats.reload();
    devices.reload();
  });
  useLive('workflow.run', () => runs.reload());

  const s = stats.data;
  const noPhones = devices.data && devices.data.length === 0;

  return (
    <>
      <Topbar title="Overview" />
      <div className="content stack" style={{ gap: 20 }}>
        <div className="page-head" style={{ marginBottom: 0 }}>
          <div>
            <h1>Hi {user?.name?.split(' ')[0]}</h1>
            <p>{user?.is_admin ? 'You are viewing data for all users as an administrator.' : "Here's what's happening on your gateway."}</p>
          </div>
          <div className="row">
            <Link className="btn" to="/workflows"><Icon name="flow" /> Workflows</Link>
            <Link className="btn primary" to="/send"><Icon name="send" /> Send SMS</Link>
          </div>
        </div>

        {noPhones && (
          <div className="card card-body row wrap" style={{ justifyContent: 'space-between', background: 'var(--accent-soft)', borderColor: 'transparent' }}>
            <div className="row">
              <Icon name="phone" width={22} height={22} style={{ color: 'var(--accent-text)' }} />
              <div>
                <h3>Pair your first phone</h3>
                <p className="muted small">Install the HttpSMS Gateway app and scan a QR code to start sending and receiving SMS.</p>
              </div>
            </div>
            <Link to="/devices" className="btn primary">Pair a phone</Link>
          </div>
        )}

        <div className="grid cols-4">
          <Stat icon="phone" tone="green" label="Phones online" value={s ? `${s.online_devices}/${s.total_devices}` : '—'} sub="connected right now" />
          <Stat icon="inbox" tone="blue" label="Received" value={s?.total_inbound ?? '—'} sub={s ? `${s.inbound_24h} in the last 24 h` : undefined} />
          <Stat icon="send" tone="violet" label="Sent" value={s?.total_outbound ?? '—'} sub={s ? `${s.outbound_24h} in the last 24 h${s.total_failed ? ` · ${s.total_failed} failed` : ''}` : undefined} />
          <Stat icon="flow" tone="amber" label="Active workflows" value={s?.active_workflows ?? '—'} sub={s ? `${s.total_workflows} total` : undefined} />
        </div>

        <div className="grid cols-2">
          <div className="card">
            <div className="card-head">
              <h2>Recent messages</h2>
              <Link to="/messages" className="small">View all</Link>
            </div>
            {recent.data && recent.data.data.length === 0 ? (
              <Empty icon="inbox" title="No messages yet">Messages your phones send and receive will appear here.</Empty>
            ) : (
              <div className="table-wrap">
                <table className="table">
                  <tbody>
                    {recent.data?.data.map((m) => (
                      <tr key={m.id}>
                        <td style={{ width: 28 }}>
                          <Icon name={m.direction === 'inbound' ? 'inbox' : 'send'} width={16} height={16} style={{ color: m.direction === 'inbound' ? 'var(--blue)' : 'var(--violet)' }} />
                        </td>
                        <td>
                          <div className="mono" style={{ fontWeight: 600 }}>{m.direction === 'inbound' ? m.sender : m.recipient}</div>
                          <div className="muted small truncate" style={{ maxWidth: 320 }}>{m.body}</div>
                        </td>
                        <td className="actions">
                          <StatusBadge status={m.status} />
                          <div className="faint small">{fmtTime(m.created_at)}</div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </div>

          <div className="stack">
            <div className="card">
              <div className="card-head">
                <h2>Phones</h2>
                <Link to="/devices" className="small">Manage</Link>
              </div>
              <div className="card-body stack" style={{ gap: 10 }}>
                {devices.data?.length === 0 && <p className="muted small">No phones paired yet.</p>}
                {devices.data?.slice(0, 4).map((d) => (
                  <div key={d.id} className="row">
                    <span className={`dot ${d.status === 'online' ? 'green' : ''}`} />
                    <div className="grow">
                      <div style={{ fontWeight: 600 }}>{d.name}</div>
                      <div className="faint small">{d.status === 'online' ? `Battery ${d.battery_level}%${d.is_charging ? ' · charging' : ''}` : `Last seen ${timeAgo(d.last_seen_at)}`}</div>
                    </div>
                    <StatusBadge status={d.status} />
                  </div>
                ))}
              </div>
            </div>

            <div className="card">
              <div className="card-head">
                <h2>Workflow activity</h2>
                <Link to="/workflows" className="small">Workflows</Link>
              </div>
              <div className="card-body stack" style={{ gap: 10 }}>
                {runs.data?.filter((r) => !r.dry_run).length === 0 && <p className="muted small">No workflow has run yet.</p>}
                {runs.data?.filter((r) => !r.dry_run).slice(0, 5).map((r) => (
                  <div key={r.id} className="row">
                    <Icon name="zap" width={16} height={16} style={{ color: 'var(--amber)' }} />
                    <div className="grow small">
                      {r.steps?.length ?? 0} step{r.steps?.length === 1 ? '' : 's'}
                      {r.error && <span className="faint"> · {r.error}</span>}
                    </div>
                    <StatusBadge status={r.status} />
                    <span className="faint small">{timeAgo(r.started_at)}</span>
                  </div>
                ))}
              </div>
            </div>
          </div>
        </div>
      </div>
    </>
  );
}
