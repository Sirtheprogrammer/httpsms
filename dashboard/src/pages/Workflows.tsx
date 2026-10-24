import { useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { api, type Workflow } from '../api';
import { Icon } from '../components/icons';
import { Topbar } from '../components/Layout';
import { Empty, Modal, Spinner, StatusBadge, Toggle, timeAgo, useAsync, useConfirm, useToast } from '../components/ui';
import { useLive } from '../live';
import { TEMPLATES } from '../workflow/nodes';

export function WorkflowsPage() {
  const toast = useToast();
  const confirm = useConfirm();
  const list = useAsync(() => api.workflows());
  const [picking, setPicking] = useState(false);
  useLive('workflow.run', () => list.reload());

  const toggle = async (wf: Workflow, enabled: boolean) => {
    try {
      await api.updateWorkflow(wf.id, { enabled });
      toast(enabled ? `"${wf.name}" is live` : `"${wf.name}" paused`, 'success');
    } catch (e: any) {
      const problems: string[] | undefined = e?.details?.problems;
      toast(problems?.length ? `Can't turn on: ${problems[0]}` : e.message, 'error');
    }
    list.reload();
  };

  const remove = async (wf: Workflow) => {
    if (!(await confirm({ title: `Delete "${wf.name}"?`, body: 'Its run history is deleted too.', confirmText: 'Delete', danger: true }))) return;
    await api.deleteWorkflow(wf.id).catch((e) => toast(e.message, 'error'));
    list.reload();
  };

  return (
    <>
      <Topbar title="Workflows" />
      <div className="content">
        <div className="page-head">
          <div>
            <h1>Workflows</h1>
            <p>Automate replies and build AI agents that respond to incoming SMS.</p>
          </div>
          <button className="btn primary" onClick={() => setPicking(true)}><Icon name="plus" /> New workflow</button>
        </div>

        {list.loading && !list.data ? <Spinner /> : list.data?.length === 0 ? (
          <div className="card">
            <Empty icon="flow" title="No workflows yet" action={<button className="btn primary" onClick={() => setPicking(true)}><Icon name="plus" /> Create a workflow</button>}>
              Start from a template such as an AI auto-reply agent, then customise it on the visual canvas.
            </Empty>
          </div>
        ) : (
          <div className="grid cols-3">
            {list.data?.map((wf) => (
              <div key={wf.id} className="card" style={{ display: 'flex', flexDirection: 'column' }}>
                <div className="card-body stack" style={{ gap: 10, flex: 1 }}>
                  <div className="row" style={{ justifyContent: 'space-between', alignItems: 'flex-start' }}>
                    <Link to={`/workflows/${wf.id}`} style={{ color: 'inherit' }}><h3>{wf.name}</h3></Link>
                    <Toggle checked={wf.enabled} onChange={(v) => toggle(wf, v)} label={wf.enabled ? 'Turn off' : 'Turn on'} />
                  </div>
                  {wf.description && <p className="muted small">{wf.description}</p>}
                  <div className="row wrap small faint" style={{ gap: 8 }}>
                    <span className={`badge ${wf.enabled ? 'green' : ''}`}>{wf.enabled ? 'Live' : 'Off'}</span>
                    <span>{wf.graph.nodes.length} steps</span>
                    <span>· edited {timeAgo(wf.updated_at)}</span>
                  </div>
                  {wf.problems.length > 0 && (
                    <div className="callout warn small"><Icon name="alert" />{wf.problems[0]}{wf.problems.length > 1 ? ` (+${wf.problems.length - 1} more)` : ''}</div>
                  )}
                  {wf.last_run && (
                    <div className="row small">
                      <span className="faint">Last run {timeAgo(wf.last_run.started_at)}{wf.last_run.dry_run ? ' (test)' : ''}</span>
                      <StatusBadge status={wf.last_run.status} />
                    </div>
                  )}
                </div>
                <div className="card-foot" style={{ justifyContent: 'space-between' }}>
                  <button className="btn sm ghost danger" onClick={() => remove(wf)}><Icon name="trash" /></button>
                  <Link className="btn sm" to={`/workflows/${wf.id}`}><Icon name="edit" /> Open editor</Link>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>
      {picking && <TemplatePicker onClose={() => setPicking(false)} />}
    </>
  );
}

function TemplatePicker({ onClose }: { onClose: () => void }) {
  const navigate = useNavigate();
  const toast = useToast();
  const create = async (id: string) => {
    const t = TEMPLATES.find((x) => x.id === id)!;
    try {
      const wf = await api.createWorkflow({ name: t.id === 'blank' ? 'Untitled workflow' : t.title, description: t.id === 'blank' ? '' : t.description, graph: t.graph });
      navigate(`/workflows/${wf.id}`);
    } catch (e) {
      toast((e as Error).message, 'error');
    }
  };
  return (
    <Modal title="New workflow" description="Pick a starting point. You can change everything on the canvas." onClose={onClose} size="lg">
      <div className="grid cols-2" style={{ gap: 12 }}>
        {TEMPLATES.map((t) => (
          <button key={t.id} className="template-card" onClick={() => create(t.id)}>
            <div className="row">
              <span className="node-icon" style={{ background: 'var(--accent-soft)', color: 'var(--accent-text)' }}><Icon name={t.icon} /></span>
              <h3>{t.title}</h3>
            </div>
            <p className="muted small">{t.description}</p>
          </button>
        ))}
      </div>
    </Modal>
  );
}
