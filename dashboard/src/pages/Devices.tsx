import { useEffect, useMemo, useState } from 'react';
import { api, type Device } from '../api';
import { useAuth } from '../auth';
import { Icon } from '../components/icons';
import { Topbar } from '../components/Layout';
import { CopyField, Empty, Modal, QRCode, Spinner, StatusBadge, timeAgo, useAsync, useConfirm, useToast } from '../components/ui';
import { useLive } from '../live';

export function DevicesPage() {
  const { user } = useAuth();
  const toast = useToast();
  const confirm = useConfirm();
  const devices = useAsync(() => api.devices());
  const [pairing, setPairing] = useState(false);
  const [renaming, setRenaming] = useState<Device | null>(null);
  useLive(['device.status', 'device.heartbeat'], () => devices.reload());

  const unpair = async (d: Device) => {
    if (!(await confirm({ title: `Unpair "${d.name}"?`, body: 'The phone disconnects and must be paired again to send or receive SMS.', confirmText: 'Unpair', danger: true }))) return;
    try {
      await api.deleteDevice(d.id);
      toast('Phone unpaired', 'success');
      devices.reload();
    } catch (e) {
      toast((e as Error).message, 'error');
    }
  };

  return (
    <>
      <Topbar title="Phones" />
      <div className="content">
        <div className="page-head">
          <div>
            <h1>Phones</h1>
            <p>Android phones running the HttpSMS Gateway app send and receive SMS for you.</p>
          </div>
          <button className="btn primary" onClick={() => setPairing(true)}><Icon name="qr" /> Pair a phone</button>
        </div>

        {devices.loading && !devices.data ? <Spinner /> : devices.data?.length === 0 ? (
          <div className="card">
            <Empty icon="phone" title="No phones paired" action={<button className="btn primary" onClick={() => setPairing(true)}><Icon name="qr" /> Pair a phone</button>}>
              Install the HttpSMS Gateway app on an Android phone with a SIM card, then scan a pairing QR code.
            </Empty>
          </div>
        ) : (
          <div className="grid cols-3">
            {devices.data?.map((d) => (
              <div key={d.id} className="card device-card">
                <div className="card-head">
                  <div className="row" style={{ minWidth: 0 }}>
                    <span className={`dot ${d.status === 'online' ? 'green' : ''}`} />
                    <h3 className="truncate">{d.name}</h3>
                  </div>
                  <StatusBadge status={d.status} />
                </div>
                <div className="card-body stack">
                  <div className="metrics">
                    <div className="metric"><div className="k">Battery</div><div className="v">{d.last_seen_at && new Date(d.last_seen_at).getFullYear() > 2000 ? `${d.battery_level}%${d.is_charging ? ' ⚡' : ''}` : '—'}</div></div>
                    <div className="metric"><div className="k">App</div><div className="v">{d.app_version || '—'}</div></div>
                    <div className="metric"><div className="k">Last seen</div><div className="v small">{d.status === 'online' ? 'now' : timeAgo(d.last_seen_at)}</div></div>
                  </div>
                  <div className="sims">
                    {(d.sim_cards ?? []).length === 0 ? (
                      <p className="faint small">{d.status === 'online' ? 'No SIM info reported (check phone permissions).' : 'SIM details appear once the phone connects.'}</p>
                    ) : d.sim_cards!.map((s) => (
                      <div key={s.slot_index} className="sim">
                        <div style={{ minWidth: 0 }}>
                          <div style={{ fontWeight: 600, fontSize: 13 }}>SIM {s.slot_index + 1} · {s.carrier_name || 'Carrier'}</div>
                          <div className="mono faint">{s.phone_number || 'number hidden'}</div>
                        </div>
                        <div className="row" style={{ gap: 6 }}>
                          <span className="faint small">{s.signal_dbm ? `${s.signal_dbm} dBm` : ''}</span>
                          <span className="bars">{[1, 2, 3, 4].map((n) => <i key={n} className={s.signal_level >= n ? 'on' : ''} />)}</span>
                        </div>
                      </div>
                    ))}
                  </div>
                  {user?.is_admin && d.user_id !== user.id && <span className="badge">Owned by another user</span>}
                </div>
                <div className="card-foot">
                  <button className="btn sm" onClick={() => setRenaming(d)}><Icon name="edit" /> Rename</button>
                  <button className="btn sm danger" onClick={() => unpair(d)}><Icon name="trash" /> Unpair</button>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>
      {pairing && <PairModal onClose={() => { setPairing(false); devices.reload(); }} />}
      {renaming && <RenameModal device={renaming} onClose={() => { setRenaming(null); devices.reload(); }} />}
    </>
  );
}

function RenameModal({ device, onClose }: { device: Device; onClose: () => void }) {
  const toast = useToast();
  const [name, setName] = useState(device.name);
  const save = async () => {
    try {
      await api.renameDevice(device.id, name);
      onClose();
    } catch (e) {
      toast((e as Error).message, 'error');
    }
  };
  return (
    <Modal title="Rename phone" onClose={onClose} footer={<><button className="btn" onClick={onClose}>Cancel</button><button className="btn primary" onClick={save} disabled={!name.trim()}>Save</button></>}>
      <div className="field">
        <label htmlFor="dname">Name</label>
        <input id="dname" className="input" value={name} onChange={(e) => setName(e.target.value)} autoFocus onKeyDown={(e) => e.key === 'Enter' && save()} />
      </div>
    </Modal>
  );
}

const LOCAL_HOSTS = ['localhost', '127.0.0.1', '::1', '[::1]', '0.0.0.0'];

function PairModal({ onClose }: { onClose: () => void }) {
  const [name, setName] = useState('');
  const [pairing, setPairing] = useState<{ device_id: string; token: string } | null>(null);
  const [error, setError] = useState('');
  const [connected, setConnected] = useState(false);
  const [server, setServer] = useState(() => {
    try {
      return localStorage.getItem('pair_server') || location.origin;
    } catch {
      return location.origin;
    }
  });

  const host = useMemo(() => {
    try {
      return new URL(server).hostname;
    } catch {
      return '';
    }
  }, [server]);
  const isLocal = LOCAL_HOSTS.includes(host);

  useEffect(() => {
    if (host && !isLocal) {
      try {
        localStorage.setItem('pair_server', server.replace(/\/+$/, ''));
      } catch {
        /* ignore */
      }
    }
  }, [server, host, isLocal]);

  useLive(['device.status', 'device.heartbeat'], (d) => {
    const id = d?.device_id ?? d?.id;
    if (pairing && id === pairing.device_id && (d.status === 'online' || d.id)) setConnected(true);
  });

  const create = async () => {
    setError('');
    try {
      setPairing(await api.pairDevice(name.trim() || 'Android phone'));
    } catch (e) {
      setError((e as Error).message);
    }
  };

  const serverUrl = server.trim().replace(/\/+$/, '');
  const payload = pairing ? JSON.stringify({ v: 1, server_url: serverUrl, device_id: pairing.device_id, token: pairing.token }) : '';

  return (
    <Modal
      title="Pair a phone"
      description="Connect an Android phone running the HttpSMS Gateway app."
      onClose={onClose}
      footer={<button className={`btn ${connected ? 'primary' : ''}`} onClick={onClose}>{connected ? 'Done' : 'Close'}</button>}
    >
      {error && <div className="callout error"><Icon name="alert" />{error}</div>}
      {!pairing ? (
        <>
          <div className="field">
            <label htmlFor="pname">Phone name</label>
            <input id="pname" className="input" placeholder="e.g. Office Samsung" value={name} onChange={(e) => setName(e.target.value)} autoFocus onKeyDown={(e) => e.key === 'Enter' && create()} />
            <span className="hint">Helps you tell phones apart. You can rename it later.</span>
          </div>
          <button className="btn primary" onClick={create}><Icon name="qr" /> Generate pairing code</button>
        </>
      ) : (
        <>
          <ol className="steps">
            <li>Open <b>HttpSMS Gateway</b> on the phone and tap <b>Scan pairing QR</b>.</li>
            <li>Point the camera at this code. The phone connects automatically.</li>
          </ol>
          <div className="field">
            <label htmlFor="server">Server address the phone will use</label>
            <input id="server" className="input mono" value={server} onChange={(e) => setServer(e.target.value)} spellCheck={false} />
            <span className={`hint ${isLocal || !host ? 'warn' : ''}`}>
              {!host ? 'Enter a full URL such as http://192.168.1.20:8080'
                : isLocal ? `"${host}" means the phone itself. Use this computer's LAN IP or your public domain.`
                  : 'The phone must be able to reach this address.'}
            </span>
          </div>
          <QRCode value={payload} />
          <div className={`callout ${connected ? 'success' : ''}`} style={{ justifyContent: 'center' }}>
            {connected ? <><Icon name="check" /> Phone connected and online.</> : <><span className="dot amber" style={{ marginTop: 5 }} /> Waiting for the phone to connect…</>}
          </div>
          <details>
            <summary className="small muted" style={{ cursor: 'pointer' }}>Can't scan? Enter details manually</summary>
            <div className="stack" style={{ marginTop: 12 }}>
              <div className="field"><span className="label">Server URL</span><CopyField value={serverUrl} /></div>
              <div className="field"><span className="label">Device token</span><CopyField value={pairing.token} /></div>
            </div>
          </details>
        </>
      )}
    </Modal>
  );
}
