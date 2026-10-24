import { useState, type FormEvent } from 'react';
import { useNavigate } from 'react-router-dom';
import { api } from '../api';
import { Icon } from '../components/icons';
import { Topbar } from '../components/Layout';
import { useAsync, useToast } from '../components/ui';
import { analyzeSms } from '../gsm';

export function SendPage() {
  const toast = useToast();
  const navigate = useNavigate();
  const devices = useAsync(() => api.devices());
  const [recipient, setRecipient] = useState('');
  const [body, setBody] = useState('');
  const [deviceId, setDeviceId] = useState('');
  const [simSlot, setSimSlot] = useState(0);
  const [busy, setBusy] = useState(false);
  const stats = analyzeSms(body);

  const online = devices.data?.filter((d) => d.status === 'online') ?? [];
  const selected = devices.data?.find((d) => d.id === deviceId);
  const sims = selected?.sim_cards?.length ? selected.sim_cards : [{ slot_index: 0 }, { slot_index: 1 }];

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      await api.sendSms({ recipient: recipient.trim(), body, device_id: deviceId || undefined, sim_slot: simSlot });
      toast('SMS handed to your phone for sending', 'success');
      navigate('/messages');
    } catch (err) {
      toast((err as Error).message, 'error');
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <Topbar title="Send SMS" />
      <div className="content" style={{ maxWidth: 720 }}>
        <div className="page-head">
          <div>
            <h1>Send an SMS</h1>
            <p>The message goes out through one of your paired phones.</p>
          </div>
        </div>

        {devices.data && online.length === 0 && (
          <div className="callout warn" style={{ marginBottom: 16 }}>
            <Icon name="alert" />
            None of your phones is online. Open the HttpSMS Gateway app and tap Start, or pair a phone first.
          </div>
        )}

        <form className="card" onSubmit={submit}>
          <div className="card-body stack">
            <div className="field">
              <label htmlFor="to">Recipient</label>
              <input id="to" className="input mono" type="tel" required placeholder="+255712345678" value={recipient} onChange={(e) => setRecipient(e.target.value)} />
              <span className="hint">Use international format with the country code.</span>
            </div>

            <div className="form-grid">
              <div className="field">
                <label htmlFor="device">Send from</label>
                <select id="device" className="select" value={deviceId} onChange={(e) => { setDeviceId(e.target.value); setSimSlot(0); }}>
                  <option value="">Any online phone</option>
                  {devices.data?.map((d) => (
                    <option key={d.id} value={d.id} disabled={d.status !== 'online'}>
                      {d.name} {d.status !== 'online' ? '(offline)' : `· ${d.battery_level}%`}
                    </option>
                  ))}
                </select>
              </div>
              <div className="field">
                <label htmlFor="sim">SIM</label>
                <select id="sim" className="select" value={simSlot} onChange={(e) => setSimSlot(Number(e.target.value))}>
                  {sims.map((s) => (
                    <option key={s.slot_index} value={s.slot_index}>
                      SIM {s.slot_index + 1}{'carrier_name' in s && s.carrier_name ? ` · ${s.carrier_name}` : ''}
                    </option>
                  ))}
                </select>
              </div>
            </div>

            <div className="field">
              <label htmlFor="body">Message</label>
              <textarea id="body" className="textarea" required rows={6} placeholder="Type your message…" value={body} onChange={(e) => setBody(e.target.value)} />
              <div className="row small" style={{ justifyContent: 'space-between' }}>
                <span className={stats.encoding === 'Unicode' ? 'hint warn' : 'hint'}>
                  {stats.encoding === 'Unicode' ? 'Unicode (emoji or special characters): 70 characters per SMS' : 'Standard GSM text: 160 characters per SMS'}
                </span>
                <span className="faint mono">{stats.length} chars · {stats.parts} SMS</span>
              </div>
            </div>
          </div>
          <div className="card-foot">
            <button className="btn primary" disabled={busy || !recipient.trim() || !body.trim()}>
              <Icon name="send" /> {busy ? 'Sending…' : 'Send SMS'}
            </button>
          </div>
        </form>
      </div>
    </>
  );
}
