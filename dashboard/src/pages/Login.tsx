import { useEffect, useState, type FormEvent } from 'react';
import { api } from '../api';
import { useAuth } from '../auth';
import { Icon } from '../components/icons';

export function LoginPage() {
  const { setUser } = useAuth();
  const [mode, setMode] = useState<'login' | 'register'>('login');
  const [firstRun, setFirstRun] = useState(false);
  const [name, setName] = useState('');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api.authStatus().then((s) => {
      if (!s.has_users) {
        setFirstRun(true);
        setMode('register');
      }
    }).catch(() => {});
  }, []);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError('');
    setBusy(true);
    try {
      const user = mode === 'login' ? await api.login(email, password) : await api.register(name, email, password);
      setUser(user);
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="auth-page">
      <section className="auth-side">
        <div className="row" style={{ fontWeight: 700, fontSize: 16 }}>
          <div className="brand-mark"><Icon name="message" width={17} height={17} /></div>
          HttpSMS
        </div>
        <div>
          <h1>Turn Android phones into a <span>programmable SMS gateway.</span></h1>
          <ul>
            <li><Icon name="phone" /> Pair phones with a QR code and send or receive SMS over a simple API.</li>
            <li><Icon name="flow" /> Build AI agents that answer texts, visually, with OpenAI, Anthropic, Gemini or DeepSeek.</li>
            <li><Icon name="key" /> Bring your own API keys. They're encrypted and visible only to you.</li>
          </ul>
        </div>
        <p style={{ opacity: 0.7, fontSize: 13 }}>Self-hosted. Your messages stay on your server.</p>
      </section>

      <section className="auth-form">
        <form className="auth-card" onSubmit={submit}>
          <div>
            <h1>{mode === 'login' ? 'Welcome back' : firstRun ? 'Create the admin account' : 'Create your account'}</h1>
            <p className="muted" style={{ marginTop: 6 }}>
              {mode === 'login'
                ? 'Sign in to manage your phones, messages and workflows.'
                : firstRun
                  ? 'This is a fresh install. The first account becomes the administrator.'
                  : 'Each account has its own phones, messages and workflows.'}
            </p>
          </div>

          {!firstRun && (
            <div className="tabs" role="tablist">
              <button type="button" className={mode === 'login' ? 'active' : ''} onClick={() => setMode('login')}>Sign in</button>
              <button type="button" className={mode === 'register' ? 'active' : ''} onClick={() => setMode('register')}>Sign up</button>
            </div>
          )}

          {error && <div className="callout error"><Icon name="alert" />{error}</div>}

          {mode === 'register' && (
            <div className="field">
              <label htmlFor="name">Name</label>
              <input id="name" className="input" value={name} onChange={(e) => setName(e.target.value)} autoComplete="name" placeholder="Jane Doe" />
            </div>
          )}
          <div className="field">
            <label htmlFor="email">Email</label>
            <input id="email" className="input" type="email" required value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="email" placeholder="you@example.com" />
          </div>
          <div className="field">
            <label htmlFor="password">Password</label>
            <input id="password" className="input" type="password" required minLength={mode === 'register' ? 8 : undefined}
              value={password} onChange={(e) => setPassword(e.target.value)}
              autoComplete={mode === 'login' ? 'current-password' : 'new-password'} />
            {mode === 'register' && <span className="hint">At least 8 characters.</span>}
          </div>
          <button className="btn primary block" disabled={busy} style={{ height: 40 }}>
            {busy ? <span className="spinner" style={{ width: 16, height: 16, borderTopColor: 'var(--on-accent)' }} /> : mode === 'login' ? 'Sign in' : 'Create account'}
          </button>
        </form>
      </section>
    </div>
  );
}
