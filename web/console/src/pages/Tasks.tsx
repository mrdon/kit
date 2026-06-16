import { useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  api,
  type CreateTaskBody,
  type Task,
  type TaskFilters,
  type TasksMeta,
} from '../api';
import TaskBoard, { type Swimlane } from './tasks/board';
import TaskList from './tasks/list';
import TaskDetail from './tasks/detail';
import { PRIORITIES, PRIORITY_LABEL, STATUSES, STATUS_LABEL } from './tasks/common';

type View = 'board' | 'list';

export default function Tasks() {
  const [meta, setMeta] = useState<TasksMeta | null>(null);
  const [tasks, setTasks] = useState<Task[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [view, setView] = useState<View>('board');
  const [swimlane, setSwimlane] = useState<Swimlane>('role');
  const [filters, setFilters] = useState<TaskFilters>({ include_closed: false });
  const [openId, setOpenId] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);

  useEffect(() => {
    api.tasksMeta().then(setMeta).catch(() => {});
  }, []);

  const load = useCallback(() => {
    api
      .listTasks(filters)
      .then((r) => setTasks(r.tasks))
      .catch((e) => setErr(e.message));
  }, [filters]);
  useEffect(load, [load]);

  const quickEdit = async (id: string, patch: { status?: string; priority?: string }) => {
    setErr(null);
    try {
      await api.updateTask(id, patch);
      load();
    } catch (e) {
      setErr((e as Error).message);
    }
  };

  const move = (id: string, status: string) => quickEdit(id, { status });

  const setFilter = (k: keyof TaskFilters, v: string | boolean) =>
    setFilters((f) => ({ ...f, [k]: v === '' ? undefined : v }));

  return (
    <div className="page">
      <div className="page-head">
        <nav className="crumbs">
          <Link to="/">Home</Link>
          <span className="crumb-sep">/</span>
          <span>Tasks</span>
        </nav>
        <div className="page-head-row">
          <h1>Tasks</h1>
          <button className="btn" onClick={() => setCreating(true)}>
            New task
          </button>
        </div>
      </div>

      {err && <p className="banner banner-error">{err}</p>}

      <div className="toolbar">
        <div className="seg">
          <button
            className={`seg-btn${view === 'board' ? ' seg-active' : ''}`}
            onClick={() => setView('board')}
          >
            Board
          </button>
          <button
            className={`seg-btn${view === 'list' ? ' seg-active' : ''}`}
            onClick={() => setView('list')}
          >
            List
          </button>
        </div>

        <select onChange={(e) => setFilter('role_scope', e.target.value)} defaultValue="">
          <option value="">All roles</option>
          {meta?.roles.map((r) => (
            <option key={r} value={r}>
              {r}
            </option>
          ))}
        </select>

        {view === 'list' && (
          <select onChange={(e) => setFilter('status', e.target.value)} defaultValue="">
            <option value="">Any status</option>
            {STATUSES.map((s) => (
              <option key={s} value={s}>
                {STATUS_LABEL[s]}
              </option>
            ))}
          </select>
        )}

        <label className="check">
          <input
            type="checkbox"
            onChange={(e) => setFilter('assigned_to_me', e.target.checked)}
          />
          Mine
        </label>
        <label className="check">
          <input
            type="checkbox"
            onChange={(e) => setFilter('include_closed', e.target.checked)}
          />
          Show closed
        </label>

        {view === 'board' && (
          <label className="check">
            Group by
            <select
              value={swimlane}
              onChange={(e) => setSwimlane(e.target.value as Swimlane)}
            >
              <option value="role">Role</option>
              <option value="assignee">Assignee</option>
              <option value="none">None</option>
            </select>
          </label>
        )}
      </div>

      {view === 'board' ? (
        <TaskBoard tasks={tasks} swimlane={swimlane} onMove={move} onOpen={setOpenId} />
      ) : (
        <TaskList tasks={tasks} onQuickEdit={quickEdit} onOpen={setOpenId} />
      )}

      {openId && (
        <TaskDetail
          taskId={openId}
          meta={meta}
          onClose={() => setOpenId(null)}
          onChanged={load}
        />
      )}

      {creating && (
        <CreateTask
          meta={meta}
          onClose={() => setCreating(false)}
          onCreated={() => {
            setCreating(false);
            load();
          }}
        />
      )}
    </div>
  );
}

function CreateTask({
  meta,
  onClose,
  onCreated,
}: {
  meta: TasksMeta | null;
  onClose: () => void;
  onCreated: () => void;
}) {
  const [body, setBody] = useState<CreateTaskBody>({ title: '', priority: 'medium' });
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setErr(null);
    try {
      await api.createTask(body);
      onCreated();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const set = (k: keyof CreateTaskBody, v: string) =>
    setBody((b) => ({ ...b, [k]: v }));

  return (
    <div className="drawer-backdrop" onClick={onClose}>
      <aside className="drawer" onClick={(e) => e.stopPropagation()}>
        <button className="drawer-close" onClick={onClose} aria-label="Close">
          ×
        </button>
        <h2 className="panel-title">New task</h2>
        {err && <p className="banner banner-error">{err}</p>}
        <form onSubmit={submit} className="stack-form">
          <label className="field">
            <span>Title</span>
            <input required autoFocus onChange={(e) => set('title', e.target.value)} />
          </label>
          <div className="field-row">
            <label className="field">
              <span>Priority</span>
              <select value={body.priority} onChange={(e) => set('priority', e.target.value)}>
                {PRIORITIES.map((p) => (
                  <option key={p} value={p}>
                    {PRIORITY_LABEL[p]}
                  </option>
                ))}
              </select>
            </label>
            <label className="field">
              <span>Role</span>
              <select onChange={(e) => set('role_scope', e.target.value)} defaultValue="">
                <option value="">Primary role</option>
                {meta?.roles.map((r) => (
                  <option key={r} value={r}>
                    {r}
                  </option>
                ))}
              </select>
            </label>
          </div>
          <div className="field-row">
            <label className="field">
              <span>Assignee</span>
              <input
                placeholder="name, @slack id, or UUID"
                onChange={(e) => set('assignee', e.target.value)}
              />
            </label>
            <label className="field">
              <span>Due date</span>
              <input type="date" onChange={(e) => set('due_date', e.target.value)} />
            </label>
          </div>
          <label className="field">
            <span>Description</span>
            <textarea rows={3} onChange={(e) => set('description', e.target.value)} />
          </label>
          <div className="drawer-actions">
            <button className="btn" type="submit" disabled={busy}>
              {busy ? 'Creating…' : 'Create task'}
            </button>
          </div>
        </form>
      </aside>
    </div>
  );
}
