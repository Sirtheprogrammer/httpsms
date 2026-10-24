import { useState, type FormEvent } from 'react';
import { api } from '../api';
import { Icon } from '../components/icons';
import { Topbar } from '../components/Layout';
import { Empty, Spinner, timeAgo, useAsync, useToast } from '../components/ui';

export function ReleasesPage() {
  const toast = useToast();
  const releases = useAsync(() => api.otaReleases());
  const [file, setFile] = useState<File | null>(null);
  const [version, setVersion] = useState('');
  const [code, setCode] = useState('');
  const [notes, setNotes] = useState('');
  const [busy, setBusy] = useState(false);

  const upload = async (e: FormEvent) => {
    e.preventDefault();
    if (!file) return;
    const form = new FormData();
    form.append('file', file);
    form.append('version', version.trim());
    form.append('version_code', code.trim());
    form.append('changelog', notes);
    form.append('target', 'android');
    setBusy(true);
    try {
      await api.uploadOta(form);
      toast('Release published. Phones can now update.', 'success');
      setFile(null);
      setVersion('');
      setCode('');
      setNotes('');
      releases.reload();
    } catch (err) {
      toast((err as Error).message, 'error');
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <Topbar title="App releases" />
      <div className="content grid cols-2" style={{ alignItems: 'start' }}>
        <form className="card" onSubmit={upload}>
          <div className="card-head"><h2>Publish a new app version</h2></div>
          <div className="card-body stack">
            <p className="muted small">Phones see the update when they tap "Check for app updates". The version code must be higher than the installed one.</p>
            <div className="field">
              <label htmlFor="apk">APK file</label>
              <input id="apk" className="input" type="file" accept=".apk" style={{ paddingTop: 7 }} onChange={(e) => setFile(e.target.files?.[0] ?? null)} required />
            </div>
            <div className="form-grid">
              <div className="field">
                <label htmlFor="ver">Version name</label>
                <input id="ver" className="input mono" placeholder="1.2.0" value={version} onChange={(e) => setVersion(e.target.value)} required />
              </div>
              <div className="field">
                <label htmlFor="vcode">Version code</label>
                <input id="vcode" className="input mono" type="number" min={1} placeholder="3" value={code} onChange={(e) => setCode(e.target.value)} required />
              </div>
            </div>
            <div className="field">
              <label htmlFor="notes">Release notes</label>
              <textarea id="notes" className="textarea" rows={3} value={notes} onChange={(e) => setNotes(e.target.value)} />
            </div>
          </div>
          <div className="card-foot"><button className="btn primary" disabled={busy || !file}><Icon name="upload" /> {busy ? 'Uploading…' : 'Publish release'}</button></div>
        </form>

        <div className="card">
          <div className="card-head"><h2>Published releases</h2></div>
          {releases.loading && !releases.data ? <Spinner /> : releases.data?.length === 0 ? (
            <Empty icon="upload" title="No releases yet">Published APKs appear here.</Empty>
          ) : (
            <div className="card-body stack">
              {releases.data?.map((r, i) => (
                <div key={r.id} className="row" style={{ alignItems: 'flex-start' }}>
                  <span className={`badge ${i === 0 ? 'green' : ''}`}>v{r.version}</span>
                  <div className="grow">
                    <div className="small">Code {r.version_code} · {(r.file_size / 1048576).toFixed(1)} MB · {timeAgo(r.created_at)}</div>
                    {r.changelog && <div className="muted small" style={{ whiteSpace: 'pre-wrap' }}>{r.changelog}</div>}
                    <div className="mono faint" style={{ fontSize: 11, wordBreak: 'break-all' }}>sha256 {r.checksum_sha256}</div>
                  </div>
                  <a className="btn sm" href={`/api/v1/ota/download/${r.id}`}>Download</a>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </>
  );
}
