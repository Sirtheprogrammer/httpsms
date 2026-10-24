import { useState } from 'react';
import { api, type AdminUser } from '../api';
import { useAuth } from '../auth';
import { Icon } from '../components/icons';
import { Topbar } from '../components/Layout';
import { Modal, Spinner, timeAgo, useAsync, useConfirm, useToast } from '../components/ui';

export function AdminUsersPage() {
  const { user: me } = useAuth();
  const toast = useToast();
  const confirm = useConfirm();
  const users = useAsync(() => api.adminUsers());
  const [resetting, setResetting] = useState<AdminUser | null>(null);
  const [filter, setFilter] = useState('');

  const update = async (u: AdminUser, payload: { role?: string; disabled?: boolean }, msg: string) => {
    try {
      await api.adminUpdateUser(u.id, payload);
      toast(msg, 'success');
      users.reload();
    } catch (e) {
      toast((e as Error).message, 'error');
    }
  };
  const remove = async (u: AdminUser) => {
    const ok = await confirm({
      title: `Delete ${u.email}?`,
      body: `This permanently deletes the account and everything it owns: ${u.devices} phones, ${u.messages} messages and ${u.workflows} workflows.`,
      confirmText: 'Delete account',
      danger: true,
    });
    if (!ok) return;
    try {
      await api.adminDeleteUser(u.id);
      toast('User deleted', 'success');
      users.reload();
    } catch (e) {
      toast((e as Error).message, 'error');
    }
  };

  const list = (users.data ?? []).filter((u) => !filter || u.email.includes(filter.toLowerCase()) || u.name.toLowerCase().includes(filter.toLowerCase()));

  return (
    <>
      <Topbar title="Users" />
      <div className="content">
        <div className="page-head">
          <div>
            <h1>Users</h1>
            <p>Anyone can sign up. Each user only sees their own phones, messages and workflows; admins see everything.</p>
          </div>
          <input className="input" style={{ maxWidth: 260 }} placeholder="Filter by name or email" value={filter} onChange={(e) => setFilter(e.target.value)} />
        </div>
        <div className="card">
          {users.loading && !users.data ? <Spinner /> : (
            <div className="table-wrap">
              <table className="table">
                <thead><tr><th>User</th><th>Role</th><th>Usage</th><th>Last sign-in</th><th /></tr></thead>
                <tbody>
                  {list.map((u) => (
                    <tr key={u.id} style={u.disabled ? { opacity: 0.6 } : undefined}>
                      <td>
                        <div className="row">
                          <div className="avatar">{(u.name || u.email).slice(0, 1).toUpperCase()}</div>
                          <div>
                            <div style={{ fontWeight: 600 }}>{u.name} {u.id === me?.id && <span className="badge">you</span>}</div>
                            <div className="faint small">{u.email}</div>
                          </div>
                        </div>
                      </td>
                      <td>
                        <span className={`badge ${u.role === 'admin' ? 'accent' : ''}`}>{u.role}</span>{' '}
                        {u.disabled && <span className="badge red">disabled</span>}
                      </td>
                      <td className="small muted">{u.devices} phones · {u.messages} SMS · {u.workflows} workflows</td>
                      <td className="faint small">{timeAgo(u.last_login_at)}</td>
                      <td className="actions">
                        {u.id !== me?.id && (
                          <div className="row" style={{ justifyContent: 'flex-end', gap: 6 }}>
                            <button className="btn sm" onClick={() => update(u, { role: u.role === 'admin' ? 'user' : 'admin' }, u.role === 'admin' ? 'Admin rights removed' : 'Promoted to admin')}>
                              {u.role === 'admin' ? 'Make user' : 'Make admin'}
                            </button>
                            <button className="btn sm" onClick={() => update(u, { disabled: !u.disabled }, u.disabled ? 'Account enabled' : 'Account disabled')}>
                              {u.disabled ? 'Enable' : 'Disable'}
                            </button>
                            <button className="btn sm" onClick={() => setResetting(u)}>Reset password</button>
                            <button className="btn sm danger icon" onClick={() => remove(u)} aria-label="Delete user"><Icon name="trash" /></button>
                          </div>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </div>
      {resetting && <ResetPassword user={resetting} onClose={() => setResetting(null)} />}
    </>
  );
}

function ResetPassword({ user, onClose }: { user: AdminUser; onClose: () => void }) {
  const toast = useToast();
  const [pw, setPw] = useState('');
  const save = async () => {
    try {
      await api.adminUpdateUser(user.id, { password: pw });
      toast('Password reset. The user was signed out everywhere.', 'success');
      onClose();
    } catch (e) {
      toast((e as Error).message, 'error');
    }
  };
  return (
    <Modal title={`Reset password for ${user.email}`} onClose={onClose} footer={<><button className="btn" onClick={onClose}>Cancel</button><button className="btn primary" disabled={pw.length < 8} onClick={save}>Set password</button></>}>
      <div className="field">
        <label htmlFor="rpw">New password</label>
        <input id="rpw" className="input" type="text" autoComplete="off" value={pw} onChange={(e) => setPw(e.target.value)} />
        <span className="hint">Share it with the user securely; they can change it from their Account page.</span>
      </div>
    </Modal>
  );
}
