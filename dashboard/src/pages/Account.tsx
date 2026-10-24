import { useState, type FormEvent } from 'react';
import { api } from '../api';
import { useAuth } from '../auth';
import { Topbar } from '../components/Layout';
import { fmtDateTime, useToast } from '../components/ui';

export function AccountPage() {
  const { user, setUser } = useAuth();
  const toast = useToast();
  const [name, setName] = useState(user?.name ?? '');
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');

  const saveName = async (e: FormEvent) => {
    e.preventDefault();
    try {
      setUser(await api.updateMe(name));
      toast('Profile updated', 'success');
    } catch (err) {
      toast((err as Error).message, 'error');
    }
  };
  const savePassword = async (e: FormEvent) => {
    e.preventDefault();
    try {
      await api.changePassword(current, next);
      setCurrent('');
      setNext('');
      toast('Password changed. Other sessions were signed out.', 'success');
    } catch (err) {
      toast((err as Error).message, 'error');
    }
  };

  return (
    <>
      <Topbar title="Account" />
      <div className="content stack" style={{ maxWidth: 720, gap: 20 }}>
        <div className="page-head" style={{ marginBottom: 0 }}>
          <div>
            <h1>Account</h1>
            <p>{user?.email} · {user?.is_admin ? 'Administrator' : 'User'} · joined {fmtDateTime(user?.created_at)}</p>
          </div>
        </div>
        <form className="card" onSubmit={saveName}>
          <div className="card-head"><h2>Profile</h2></div>
          <div className="card-body">
            <div className="field">
              <label htmlFor="aname">Display name</label>
              <input id="aname" className="input" value={name} onChange={(e) => setName(e.target.value)} />
            </div>
          </div>
          <div className="card-foot"><button className="btn primary" disabled={!name.trim()}>Save</button></div>
        </form>
        <form className="card" onSubmit={savePassword}>
          <div className="card-head"><h2>Password</h2></div>
          <div className="card-body stack">
            <div className="field">
              <label htmlFor="cur">Current password</label>
              <input id="cur" className="input" type="password" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} required />
            </div>
            <div className="field">
              <label htmlFor="new">New password</label>
              <input id="new" className="input" type="password" autoComplete="new-password" minLength={8} value={next} onChange={(e) => setNext(e.target.value)} required />
              <span className="hint">At least 8 characters. Changing it signs out your other sessions.</span>
            </div>
          </div>
          <div className="card-foot"><button className="btn primary">Change password</button></div>
        </form>
      </div>
    </>
  );
}
