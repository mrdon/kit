import { useEffect, useState } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import {
  api,
  type Task,
  type TaskEvent,
  type TasksMeta,
  type UpdateTaskBody,
} from '../../api';
import { PRIORITIES, PRIORITY_LABEL, STATUSES, STATUS_LABEL, fmtDate } from './common';

interface Props {
  taskId: string;
  meta: TasksMeta | null;
  onClose: () => void;
  onChanged: () => void;
}

type Patch = (body: UpdateTaskBody) => void;

export default function TaskDetail({ taskId, meta, onClose, onChanged }: Props) {
  const [task, setTask] = useState<Task | null>(null);
  const [events, setEvents] = useState<TaskEvent[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const load = () => {
    api
      .getTask(taskId)
      .then((r) => {
        setTask(r.task);
        setEvents(r.events);
      })
      .catch((e) => setErr(e.message));
  };
  useEffect(load, [taskId]);

  const patch: Patch = async (body) => {
    setSaving(true);
    setErr(null);
    try {
      const r = await api.updateTask(taskId, body);
      setTask(r.task);
      onChanged();
      load();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="drawer-backdrop" onClick={onClose}>
      <aside className="drawer" onClick={(e) => e.stopPropagation()}>
        <button className="drawer-close" onClick={onClose} aria-label="Close">
          ×
        </button>
        {err && <p className="banner banner-error">{err}</p>}
        {!task ? (
          <p className="muted">Loading…</p>
        ) : (
          <>
            <EditableTitle title={task.title} onSave={(v) => patch({ title: v })} />
            <EditableBody
              description={task.description ?? ''}
              onSave={(v) => patch({ description: v })}
            />
            <Properties task={task} meta={meta} patch={patch} saving={saving} />
            <div className="drawer-actions">
              {task.status !== 'done' && (
                <button className="btn" onClick={() => patch({ status: 'done' })}>
                  Complete
                </button>
              )}
              <button
                className="btn btn-ghost"
                onClick={() => api.snoozeTask(taskId, 7).then(onChanged)}
              >
                Snooze 7d
              </button>
            </div>
            <Activity taskId={taskId} events={events} onPosted={load} />
          </>
        )}
      </aside>
    </div>
  );
}

// EditableTitle renders the title as a heading; click to edit inline.
function EditableTitle({ title, onSave }: { title: string; onSave: (v: string) => void }) {
  const [editing, setEditing] = useState(false);
  if (editing) {
    return (
      <input
        className="drawer-title"
        autoFocus
        defaultValue={title}
        onBlur={(e) => {
          setEditing(false);
          const v = e.target.value.trim();
          if (v && v !== title) onSave(v);
        }}
      />
    );
  }
  return (
    <h2 className="drawer-title view-title" title="Click to edit" onClick={() => setEditing(true)}>
      {title}
    </h2>
  );
}

// EditableBody shows the description as formatted markdown (the focus of the
// drawer); click it to drop into a raw textarea, blur to save and re-render.
function EditableBody({ description, onSave }: { description: string; onSave: (v: string) => void }) {
  const [editing, setEditing] = useState(false);
  if (editing) {
    return (
      <textarea
        className="body-edit"
        autoFocus
        rows={10}
        defaultValue={description}
        onBlur={(e) => {
          setEditing(false);
          if (e.target.value !== description) onSave(e.target.value);
        }}
      />
    );
  }
  return (
    <div className="md-body markdown" title="Click to edit" onClick={() => setEditing(true)}>
      {description ? (
        <ReactMarkdown remarkPlugins={[remarkGfm]}>{description}</ReactMarkdown>
      ) : (
        <p className="muted">No description — click to add one.</p>
      )}
    </div>
  );
}

// Properties is the compact metadata block below the body.
function Properties({
  task,
  meta,
  patch,
  saving,
}: {
  task: Task;
  meta: TasksMeta | null;
  patch: Patch;
  saving: boolean;
}) {
  return (
    <div className="task-props">
      <div className="field-row">
        <label className="field">
          <span>Status</span>
          <select value={task.status} disabled={saving} onChange={(e) => patch({ status: e.target.value })}>
            {STATUSES.map((s) => (
              <option key={s} value={s}>
                {STATUS_LABEL[s]}
              </option>
            ))}
          </select>
        </label>
        <label className="field">
          <span>Priority</span>
          <select value={task.priority} disabled={saving} onChange={(e) => patch({ priority: e.target.value })}>
            {PRIORITIES.map((p) => (
              <option key={p} value={p}>
                {PRIORITY_LABEL[p]}
              </option>
            ))}
          </select>
        </label>
      </div>

      {task.status === 'blocked' && (
        <label className="field">
          <span>Blocked reason</span>
          <input
            defaultValue={task.blocked_reason ?? ''}
            onBlur={(e) => patch({ blocked_reason: e.target.value })}
          />
        </label>
      )}

      <div className="field-row">
        <label className="field">
          <span>Role</span>
          <select
            value={task.role_name ?? ''}
            disabled={saving}
            onChange={(e) => patch({ role_scope: e.target.value })}
          >
            {task.role_name && !meta?.roles.includes(task.role_name) && (
              <option value={task.role_name}>{task.role_name}</option>
            )}
            {meta?.roles.map((r) => (
              <option key={r} value={r}>
                {r}
              </option>
            ))}
          </select>
        </label>
        <label className="field">
          <span>Due date</span>
          <input
            type="date"
            defaultValue={task.due_date?.slice(0, 10) ?? ''}
            onBlur={(e) =>
              patch(e.target.value ? { due_date: e.target.value } : { clear_due_date: true })
            }
          />
        </label>
      </div>

      <label className="field">
        <span>Category</span>
        <select
          value={task.category ?? ''}
          disabled={saving}
          onChange={(e) => patch({ category: e.target.value })}
        >
          <option value="">Uncategorized</option>
          {task.category && !(meta?.categories ?? []).includes(task.category) && (
            <option value={task.category}>{task.category}</option>
          )}
          {(meta?.categories ?? []).map((c) => (
            <option key={c} value={c}>
              {c}
            </option>
          ))}
        </select>
      </label>

      <label className="field">
        <span>Assignee {task.assignee_name ? `(${task.assignee_name})` : ''}</span>
        <div className="field-row">
          <input
            placeholder="name, @slack id, or UUID"
            defaultValue=""
            onBlur={(e) => e.target.value && patch({ assignee: e.target.value })}
          />
          {task.assignee_user_id && (
            <button className="btn btn-danger" onClick={() => patch({ clear_assignee: true })}>
              Unassign
            </button>
          )}
        </div>
      </label>
    </div>
  );
}

// Activity is the comment box + audit timeline.
function Activity({
  taskId,
  events,
  onPosted,
}: {
  taskId: string;
  events: TaskEvent[];
  onPosted: () => void;
}) {
  const [comment, setComment] = useState('');
  const [busy, setBusy] = useState(false);

  const post = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!comment.trim()) return;
    setBusy(true);
    try {
      await api.addTaskComment(taskId, comment.trim());
      setComment('');
      onPosted();
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <h3 className="timeline-head">Activity</h3>
      <form className="inline-form" onSubmit={post}>
        <input placeholder="Add a comment…" value={comment} onChange={(e) => setComment(e.target.value)} />
        <button className="btn" type="submit" disabled={busy}>
          Add
        </button>
      </form>
      <ul className="timeline">
        {events.map((ev) => (
          <li key={ev.id} className="timeline-item">
            <span className="timeline-meta">
              {ev.author_name || 'system'} · {fmtDate(ev.created_at)} · {ev.event_type}
            </span>
            {ev.content && <span className="timeline-body">{ev.content}</span>}
            {!ev.content && (ev.old_value || ev.new_value) && (
              <span className="timeline-body">
                {ev.old_value} → {ev.new_value}
              </span>
            )}
          </li>
        ))}
      </ul>
    </>
  );
}
