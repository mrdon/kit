import { useCallback, useEffect, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  api,
  type CreateTaskBody,
  type Task,
  type TaskFilters,
  type TasksMeta,
  type UpdateTaskBody,
} from '../api';
import TaskGrouped from './tasks/grouped';
import TaskList from './tasks/list';
import TaskDetail from './tasks/detail';
import { PRIORITIES, PRIORITY_LABEL, STATUSES, STATUS_LABEL } from './tasks/common';

type View = 'grouped' | 'list';

// inversePatch builds the undo for a quick action: for each field the patch
// changed, restore the task's prior value. Assignee is special — clear it if
// the task had no assignee before, otherwise reassign the previous one.
function inversePatch(prev: Task, patch: UpdateTaskBody): UpdateTaskBody {
  const inv: UpdateTaskBody = {};
  if ('priority' in patch) inv.priority = prev.priority;
  if ('status' in patch) inv.status = prev.status;
  if ('category' in patch) inv.category = prev.category ?? '';
  if ('assignee' in patch || 'clear_assignee' in patch) {
    if (prev.assignee_user_id) inv.assignee = prev.assignee_user_id;
    else inv.clear_assignee = true;
  }
  return inv;
}

export default function Tasks() {
  const [meta, setMeta] = useState<TasksMeta | null>(null);
  const [meId, setMeId] = useState('');
  const [tasks, setTasks] = useState<Task[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [view, setView] = useState<View>('grouped');
  const [filters, setFilters] = useState<TaskFilters>({ include_closed: false });
  const [openId, setOpenId] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [toast, setToast] = useState<{ msg: string; undo: () => void } | null>(null);
  const toastTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    api.tasksMeta().then(setMeta).catch(() => {});
    api.me().then((m) => setMeId(m.user_id)).catch(() => {});
  }, []);

  const load = useCallback(() => {
    api
      .listTasks(filters)
      .then((r) => setTasks(r.tasks))
      .catch((e) => setErr(e.message));
  }, [filters]);
  useEffect(load, [load]);

  const mutate = async (id: string, patch: UpdateTaskBody) => {
    setErr(null);
    try {
      await api.updateTask(id, patch);
      load();
    } catch (e) {
      setErr((e as Error).message);
    }
  };

  const showToast = useCallback((msg: string, undo: () => void) => {
    if (toastTimer.current) clearTimeout(toastTimer.current);
    setToast({ msg, undo });
    toastTimer.current = setTimeout(() => setToast(null), 7000);
  }, []);
  useEffect(() => () => {
    if (toastTimer.current) clearTimeout(toastTimer.current);
  }, []);

  // act applies a patch and offers an undo toast. The inverse is built from
  // the task's prior state for whichever fields the patch touched, so any
  // quick action — reprioritize, claim, resolve, recategorize — is reversible.
  const act = async (id: string, patch: UpdateTaskBody, label: string) => {
    const prev = tasks.find((t) => t.id === id);
    await mutate(id, patch);
    if (prev) showToast(label, () => mutate(id, inversePatch(prev, patch)));
  };

  const runUndo = () => {
    if (toastTimer.current) clearTimeout(toastTimer.current);
    toast?.undo();
    setToast(null);
  };

  // "I'm on it": claiming assigns the task to me (and marks it in progress);
  // unclaiming returns it to the open backlog. Assignment blocks the work
  // off for teammates.
  const claim = (id: string, on: boolean) =>
    on
      ? act(id, { status: 'in_progress', assignee: meId }, 'Claimed task')
      : act(id, { status: 'open', clear_assignee: true }, 'Released task');

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
            className={`seg-btn${view === 'grouped' ? ' seg-active' : ''}`}
            onClick={() => setView('grouped')}
          >
            Tasks
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
      </div>

      {view === 'grouped' ? (
        <TaskGrouped
          tasks={tasks}
          meId={meId}
          categories={meta?.categories ?? []}
          onReprioritize={(id, priority) =>
            act(id, { priority }, `Moved to ${PRIORITY_LABEL[priority] ?? priority}`)
          }
          onClaim={claim}
          onResolve={(id) => act(id, { status: 'done' }, 'Resolved task')}
          onReopen={(id) => act(id, { status: 'open' }, 'Reopened task')}
          onSetCategory={(id, category) =>
            act(id, { category }, category ? `Set category “${category}”` : 'Cleared category')
          }
          onOpen={setOpenId}
        />
      ) : (
        <TaskList
          tasks={tasks}
          onQuickEdit={(id, patch) => act(id, patch, 'Updated task')}
          onOpen={setOpenId}
        />
      )}

      {toast && (
        <div className="toast" role="status">
          <span>{toast.msg}</span>
          <button className="toast-undo" onClick={runUndo}>
            Undo
          </button>
        </div>
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
