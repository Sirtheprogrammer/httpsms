import { createContext, useContext, useEffect, useState, type ReactNode } from 'react';
import { NavLink, Outlet, useLocation } from 'react-router-dom';
import { useAuth } from '../auth';
import { useLiveStatus } from '../live';
import { Icon, type IconName } from './icons';

const mainNav: { to: string; label: string; icon: IconName }[] = [
  { to: '/', label: 'Overview', icon: 'home' },
  { to: '/messages', label: 'Messages', icon: 'inbox' },
  { to: '/send', label: 'Send SMS', icon: 'send' },
  { to: '/devices', label: 'Phones', icon: 'phone' },
];
const buildNav: { to: string; label: string; icon: IconName }[] = [
  { to: '/workflows', label: 'Workflows', icon: 'flow' },
  { to: '/ai-keys', label: 'AI keys', icon: 'sparkles' },
  { to: '/webhooks', label: 'Webhooks', icon: 'webhook' },
  { to: '/api-tokens', label: 'API tokens', icon: 'code' },
];
const adminNav: { to: string; label: string; icon: IconName }[] = [
  { to: '/admin/users', label: 'Users', icon: 'users' },
  { to: '/admin/releases', label: 'App releases', icon: 'upload' },
];

const MenuCtx = createContext<() => void>(() => {});

type Theme = 'system' | 'light' | 'dark';

function useTheme(): [Theme, () => void] {
  const [theme, setTheme] = useState<Theme>(() => {
    try {
      return (localStorage.getItem('theme') as Theme) || 'system';
    } catch {
      return 'system';
    }
  });
  useEffect(() => {
    const root = document.documentElement;
    if (theme === 'system') root.removeAttribute('data-theme');
    else root.setAttribute('data-theme', theme);
    try {
      localStorage.setItem('theme', theme);
    } catch {
      /* storage unavailable */
    }
  }, [theme]);
  const cycle = () => setTheme((t) => (t === 'system' ? 'dark' : t === 'dark' ? 'light' : 'system'));
  return [theme, cycle];
}

export function Layout() {
  const { user, logout } = useAuth();
  const [theme, cycleTheme] = useTheme();
  const [open, setOpen] = useState(false);
  const location = useLocation();
  useEffect(() => setOpen(false), [location.pathname]);

  const link = (item: { to: string; label: string; icon: IconName }) => (
    <NavLink key={item.to} to={item.to} end={item.to === '/'}>
      <Icon name={item.icon} />
      {item.label}
    </NavLink>
  );
  const initials = (user?.name || user?.email || '?').slice(0, 1).toUpperCase();

  return (
    <div className="shell">
      {open && <div className="sidebar-backdrop" onClick={() => setOpen(false)} />}
      <aside className={`sidebar ${open ? 'open' : ''}`}>
        <div className="brand">
          <div className="brand-mark"><Icon name="message" width={17} height={17} /></div>
          HttpSMS
        </div>
        <nav className="nav">
          {mainNav.map(link)}
          <div className="nav-section">Build</div>
          {buildNav.map(link)}
          {user?.is_admin && (
            <>
              <div className="nav-section">Admin</div>
              {adminNav.map(link)}
            </>
          )}
        </nav>
        <div className="sidebar-foot">
          <NavLink to="/account" className="user-chip" style={{ color: 'inherit', textDecoration: 'none' }}>
            <div className="avatar">{initials}</div>
            <div className="meta">
              <div style={{ fontWeight: 600 }}>{user?.name}</div>
              <div className="faint small">{user?.is_admin ? 'Administrator' : user?.email}</div>
            </div>
          </NavLink>
          <div className="row">
            <button className="btn ghost sm grow" onClick={cycleTheme} title="Switch theme">
              <Icon name={theme === 'dark' ? 'moon' : theme === 'light' ? 'sun' : 'settings'} />
              {theme === 'system' ? 'Auto' : theme === 'dark' ? 'Dark' : 'Light'}
            </button>
            <button className="btn ghost sm grow" onClick={logout}>
              <Icon name="logout" /> Sign out
            </button>
          </div>
        </div>
      </aside>
      <div className="main">
        <MenuCtx.Provider value={() => setOpen(true)}>
          <Outlet />
        </MenuCtx.Provider>
      </div>
    </div>
  );
}

/** Page top bar with title and actions; shows the live connection state. */
export function Topbar({ title, children, back }: { title: ReactNode; children?: ReactNode; back?: ReactNode }) {
  const online = useLiveStatus();
  const openMenu = useContext(MenuCtx);
  return (
    <header className="topbar">
      <button className="btn ghost icon menu-btn" onClick={openMenu} aria-label="Open menu"><Icon name="menu" /></button>
      {back}
      <div className="row" style={{ minWidth: 0 }}>{typeof title === 'string' ? <h2 className="truncate">{title}</h2> : title}</div>
      <div className="spacer" />
      {children}
      <span className="row small faint" title={online ? 'Live updates connected' : 'Reconnecting to live updates'}>
        <span className={`dot ${online ? 'green' : 'amber'}`} />
        <span className="hide-sm">{online ? 'Connected' : 'Reconnecting'}</span>
      </span>
    </header>
  );
}
