import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { useAuth } from '../auth';
import '../landing.css';

const REPO = 'Sirtheprogrammer/httpsms';
const REPO_URL = `https://github.com/${REPO}`;
const RELEASES_URL = `${REPO_URL}/releases/latest`;
const CACHE_MS = 10 * 60 * 1000; // GitHub's anonymous API allows 60 requests/hour per IP

interface Release { version: string; url: string; size: string; date: string }

async function cachedJson(key: string, url: string) {
  try {
    const hit = JSON.parse(sessionStorage.getItem(key) || 'null');
    if (hit && Date.now() - hit.t < CACHE_MS) return hit.v;
  } catch {
    /* storage unavailable */
  }
  const r = await fetch(url, { headers: { Accept: 'application/vnd.github+json' } });
  if (!r.ok) throw new Error(`GitHub API ${r.status}`);
  const v = await r.json();
  try {
    sessionStorage.setItem(key, JSON.stringify({ t: Date.now(), v }));
  } catch {
    /* ignore */
  }
  return v;
}

/** Latest APK release and star count, straight from GitHub. Falls back to plain links. */
function useGitHub() {
  const [stars, setStars] = useState<number | null>(null);
  const [release, setRelease] = useState<Release | null>(null);
  useEffect(() => {
    cachedJson('httpsms_repo', `https://api.github.com/repos/${REPO}`).then((r) => setStars(r.stargazers_count ?? 0)).catch(() => {});
    cachedJson('httpsms_release', `https://api.github.com/repos/${REPO}/releases/latest`).then((rel) => {
      const apk = (rel.assets ?? []).find((a: { name: string }) => /\.apk$/i.test(a.name));
      if (!apk) return;
      setRelease({
        version: String(rel.tag_name ?? '').replace(/^apk-v/, '').replace(/-build\.\d+$/, ''),
        url: apk.browser_download_url,
        size: `${(apk.size / 1048576).toFixed(1)} MB`,
        date: new Date(rel.published_at).toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' }),
      });
    }).catch(() => {});
  }, []);
  return { stars, release };
}

const compact = (n: number) => (n >= 1000 ? `${(n / 1000).toFixed(n >= 10000 ? 0 : 1).replace(/\.0$/, '')}k` : String(n));

const GitHubIcon = () => (
  <svg className="lp-gh" viewBox="0 0 16 16" aria-hidden="true"><path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0 0 16 8c0-4.42-3.58-8-8-8z" /></svg>
);
const StarIcon = () => (
  <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M8 .25a.75.75 0 0 1 .673.418l1.882 3.815 4.21.612a.75.75 0 0 1 .416 1.279l-3.046 2.97.719 4.192a.75.75 0 0 1-1.088.791L8 12.347l-3.766 1.98a.75.75 0 0 1-1.088-.79l.72-4.194L.818 6.374a.75.75 0 0 1 .416-1.28l4.21-.611L7.327.668A.75.75 0 0 1 8 .25z" /></svg>
);
const DownloadIcon = () => <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4M7 10l5 5 5-5M12 15V3" /></svg>;
const LogoMark = ({ small }: { small?: boolean }) => (
  <span className={`lp-logo-mark ${small ? 'lp-sm' : ''}`}><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z" /></svg></span>
);

const FEATURES: { title: string; text: string; path: string }[] = [
  { title: 'Pair with a QR code', text: 'Scan once from the app. Dual-SIM phones work, with SIM, battery and signal shown live.', path: 'M3 3h7v7H3zM14 3h7v7h-7zM3 14h7v7H3zM14 14h3v3h-3zM17 17h4v4h-4' },
  { title: 'AI agents, visually', text: 'Drag-and-drop workflows: trigger, AI agent, condition, send SMS, HTTP call. Agents remember each conversation.', path: 'M12 3l1.9 5.1L19 10l-5.1 1.9L12 17l-1.9-5.1L5 10l5.1-1.9z' },
  { title: 'Bring your own key', text: 'OpenAI, Anthropic, Gemini or DeepSeek. Keys are encrypted on the server and only you can use them.', path: 'M21 2l-2 2m-7.61 7.61a5.5 5.5 0 1 1-7.78 7.78 5.5 5.5 0 0 1 7.78-7.78zm0 0L15.5 7.5m0 0 3 3L22 7l-3-3m-3.5 3.5L19 4' },
  { title: 'Simple REST API', text: 'Send with one POST. Scoped tokens, idempotency keys and signed webhooks with retries.', path: 'm16 18 6-6-6-6M8 6l-6 6 6 6' },
  { title: 'Your own workspace', text: 'Every account gets its own phones, messages and workflows. Nobody else sees your data.', path: 'M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2M9 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8zM22 21v-2a4 4 0 0 0-3-3.87M16 3.13a4 4 0 0 1 0 7.75' },
  { title: 'Stays online', text: 'Foreground service, auto-reconnect, restart after reboot and a watchdog against aggressive battery savers.', path: 'M13 2 3 14h9l-1 8 10-12h-9z' },
];

export function LandingPage() {
  const { user } = useAuth();
  const { stars, release } = useGitHub();
  const origin = window.location.origin;
  const download = release?.url ?? RELEASES_URL;

  const account = user
    ? <Link className="lp-btn lp-btn-primary" to="/">Open dashboard</Link>
    : <Link className="lp-btn lp-btn-ghost" to="/login">Sign in</Link>;

  return (
    <div className="lp-root">
      <header className="lp-nav">
        <div className="lp-wrap lp-nav-inner">
          <a className="lp-logo" href="#top" aria-label="HttpSMS home"><LogoMark />HttpSMS</a>
          <nav className="lp-nav-links" aria-label="Sections">
            <a href="#features">Features</a>
            <a href="#how">Get started</a>
            <a href="#api">API</a>
          </nav>
          <a className="lp-btn lp-btn-ghost lp-hide-sm" href={REPO_URL} target="_blank" rel="noopener noreferrer">
            <GitHubIcon /> GitHub
            {stars !== null && <span className="lp-stars" aria-label={`${stars} GitHub stars`}><StarIcon />{compact(stars)}</span>}
          </a>
          {account}
        </div>
      </header>

      <main id="top">
        <section className="lp-hero">
          <div className="lp-glow" aria-hidden="true" />
          <div className="lp-wrap lp-hero-inner">
            <h1>Your Android phone,<br /><span className="lp-lime">now an SMS gateway.</span></h1>
            <p className="lp-lead">
              Pair a phone with a QR code, send and receive SMS over a simple API, and build AI agents that answer texts,
              using OpenAI, Anthropic, Gemini or DeepSeek with your own key.
            </p>
            <div className="lp-cta">
              <a className="lp-btn lp-btn-primary lp-btn-lg" href={download} rel="noopener noreferrer">
                <DownloadIcon />{release ? `Download APK v${release.version}` : 'Download APK'}
              </a>
              <a className="lp-btn lp-btn-outline lp-btn-lg" href={REPO_URL} target="_blank" rel="noopener noreferrer">
                <GitHubIcon /> Star on GitHub
                {stars !== null && <span className="lp-count">{compact(stars)}</span>}
              </a>
            </div>
            <p className="lp-fineprint">
              {release ? `v${release.version} · ${release.size} · released ${release.date} · Android 8.0+` : 'Android 8.0+ · signed APK from GitHub Releases'}
              {' · '}
              {user ? <Link to="/" className="lp-lime">Open your dashboard →</Link> : <Link to="/login" className="lp-lime">Create a free account →</Link>}
            </p>
          </div>

          <div className="lp-wrap">
            <div className="lp-preview" role="img" aria-label="Preview of the HttpSMS dashboard showing stats, recent messages and an AI workflow">
              <div className="lp-window-bar"><span /><span /><span /><div className="lp-url">{window.location.host}</div></div>
              <div className="lp-app">
                <aside className="lp-side">
                  <div className="lp-side-brand"><LogoMark small />HttpSMS</div>
                  <div className="lp-side-item lp-active">Overview</div>
                  <div className="lp-side-item">Messages</div>
                  <div className="lp-side-item">Send SMS</div>
                  <div className="lp-side-item">Phones</div>
                  <div className="lp-side-label">Build</div>
                  <div className="lp-side-item">Workflows</div>
                  <div className="lp-side-item">AI keys</div>
                  <div className="lp-side-item">Webhooks</div>
                </aside>
                <div className="lp-body">
                  <div className="lp-stats">
                    <div className="lp-stat"><small>Phones online</small><b>2/2</b></div>
                    <div className="lp-stat"><small>Received</small><b>1,284</b></div>
                    <div className="lp-stat"><small>Sent</small><b>963</b></div>
                    <div className="lp-stat"><small>Active workflows</small><b>3</b></div>
                  </div>
                  <div className="lp-panels">
                    <div className="lp-panel">
                      <div className="lp-panel-title">Recent messages</div>
                      {[
                        ['in', '+255 712 *** 401', 'What time do you open tomorrow?', 'received', 'ok'],
                        ['out', '+255 712 *** 401', 'We open at 8:00 AM. See you then!', 'delivered', 'sent'],
                        ['in', '+254 701 *** 088', 'PRICE list please', 'received', 'ok'],
                        ['out', '+254 701 *** 088', 'Basic 5,000 · Pro 12,000. Reply BUY to order.', 'sent', 'sent'],
                      ].map(([dir, who, text, status, tone], i) => (
                        <div key={i} className="lp-msg">
                          <i className={`lp-${dir}`} />
                          <div><b>{who}</b><span>{text}</span></div>
                          <em className={`lp-tag lp-${tone}`}>{status}</em>
                        </div>
                      ))}
                    </div>
                    <div className="lp-panel lp-flow">
                      <div className="lp-panel-title">AI auto-reply agent <em className="lp-tag lp-live">live</em></div>
                      <div className="lp-nodes">
                        <div className="lp-node"><span className="lp-ni lp-blue" />Incoming SMS</div>
                        <div className="lp-edge" />
                        <div className="lp-node"><span className="lp-ni lp-violet" />AI agent<small>claude-opus-5-5</small></div>
                        <div className="lp-edge" />
                        <div className="lp-node"><span className="lp-ni lp-lime" />Send SMS<small>{'{{ai_1.text}}'}</small></div>
                      </div>
                    </div>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </section>

        <section id="features" className="lp-section">
          <div className="lp-wrap">
            <h2>Everything to run SMS from your own server</h2>
            <div className="lp-features">
              {FEATURES.map((f) => (
                <article key={f.title} className="lp-feature">
                  <div className="lp-fi"><svg viewBox="0 0 24 24" aria-hidden="true"><path d={f.path} /></svg></div>
                  <h3>{f.title}</h3>
                  <p>{f.text}</p>
                </article>
              ))}
            </div>
          </div>
        </section>

        <section id="how" className="lp-section lp-alt">
          <div className="lp-wrap">
            <h2>Start using this server in three steps</h2>
            <ol className="lp-steps">
              <li><span>1</span><h3>Create an account</h3><p>Sign up here. Your phones, messages and workflows are private to your account.</p></li>
              <li><span>2</span><h3>Install &amp; pair</h3><p>Install the APK, open <b>Phones → Pair a phone</b> in the dashboard and scan the QR code with the app.</p></li>
              <li><span>3</span><h3>Automate</h3><p>Send from the API or let a workflow answer incoming texts with the AI model you choose.</p></li>
            </ol>
            <div className="lp-cta">
              {user
                ? <Link className="lp-btn lp-btn-primary lp-btn-lg" to="/">Open dashboard</Link>
                : <Link className="lp-btn lp-btn-primary lp-btn-lg" to="/login">Create your account</Link>}
              <a className="lp-btn lp-btn-outline lp-btn-lg" href={download} rel="noopener noreferrer"><DownloadIcon /> Get the Android app</a>
            </div>
          </div>
        </section>

        <section id="api" className="lp-section">
          <div className="lp-wrap lp-selfhost">
            <div>
              <h2>Send your first SMS</h2>
              <p className="lp-muted">
                Create an API token under <b>API tokens</b>, then call this server from any language. Incoming messages
                and delivery reports can be pushed to your app with signed webhooks.
              </p>
              <p className="lp-muted" style={{ marginTop: 14 }}>
                Want your own instance? It's open source:{' '}
                <a className="lp-lime" href={`${REPO_URL}#readme`} target="_blank" rel="noopener noreferrer">self-host with Docker →</a>
              </p>
            </div>
            <pre className="lp-code"><code><span className="lp-c"># send an SMS through your paired phone</span>{`
curl -X POST ${origin}/api/v1/sms/send \\
  -H "Authorization: Bearer htsms_live_…" \\
  -H "Content-Type: application/json" \\
  -d '{"recipient":"+255712345678","body":"Hello!"}'

`}<span className="lp-c"># list received messages</span>{`
curl ${origin}/api/v1/messages?direction=inbound \\
  -H "Authorization: Bearer htsms_live_…"`}</code></pre>
          </div>
        </section>
      </main>

      <footer className="lp-footer">
        <div className="lp-wrap lp-footer-inner">
          <span>HttpSMS · open source</span>
          {user ? <Link to="/">Dashboard</Link> : <Link to="/login">Sign in</Link>}
          <a href={REPO_URL} target="_blank" rel="noopener noreferrer">GitHub</a>
          <a href={`${REPO_URL}/releases`} target="_blank" rel="noopener noreferrer">Releases</a>
          <a href={`${REPO_URL}/issues`} target="_blank" rel="noopener noreferrer">Report an issue</a>
        </div>
      </footer>
    </div>
  );
}
