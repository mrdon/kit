import { useEffect, useState } from 'react';
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

export default function TaskDetail({ taskId, meta, onClose, onChanged }: Props) {
  const [task, setTask] = useState<Task | null>(null);
  const [events, setEvents] = useState<TaskEvent[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [comment, setComment] = useState('');
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

  const patch = async (body: UpdateTaskBody) => {
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

  const postComment = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!comment.trim()) return;
    setSaving(true);
    try {
      await api.addTaskComment(taskId, comment.trim());
      setComment('');
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
            <input
              className="drawer-title"
              defaultValue={task.title}
              onBlur={(e) =>
                e.target.value !== task.title && patch({ title: e.target.value })
              }
            />

            <div className="field-row">
              <label className="field">
                <span>Status</span>
                <select
                  value={task.status}
                  disabled={saving}
                  onChange={(e) => patch({ status: e.target.value })}
                >
                  {STATUSES.map((s) => (
                    <option key={s} value={s}>
                      {STATUS_LABEL[s]}
                    </option>
                  ))}
                </select>
              </label>
              <label className="field">
                <span>Priority</span>
                <select
                  value={task.priority}
                  disabled={saving}
                  onChange={(e) => patch({ priority: e.target.value })}
                >
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
                  {task.role_name &&
                    !meta?.roles.includes(task.role_name) && (
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
                    patch(
                      e.target.value
                        ? { due_date: e.target.value }
                        : { clear_due_date: true },
                    )
                  }
                />
              </label>
            </div>

            <label className="field">
              <span>Category</span>
              <input
                list="task-categories"
                placeholder="e.g. brewing, sales (auto-assigned)"
                defaultValue={task.category ?? ''}
                onBlur={(e) =>
                  e.target.value !== (task.category ?? '') &&
                  patch({ category: e.target.value })
                }
              />
              <datalist id="task-categories">
                {meta?.categories.map((c) => (
                  <option key={c} value={c} />
                ))}
              </datalist>
            </label>

            <label className="field">
              <span>Assignee {task.assignee_name ? `(${task.assignee_name})` : ''}</span>
              <div className="field-row">
                <input
                  placeholder="name, @slack id, or UUID"
                  defaultValue=""
                  onBlur={(e) =>
                    e.target.value && patch({ assignee: e.target.value })
                  }
                />
                {task.assignee_user_id && (
                  <button
                    className="btn btn-danger"
                    onClick={() => patch({ clear_assignee: true })}
                  >
                    Unassign
                  </button>
                )}
              </div>
            </label>

            <label className="field">
              <span>Description</span>
              <textarea
                rows={4}
                defaultValue={task.description ?? ''}
                onBlur={(e) =>
                  e.target.value !== (task.description ?? '') &&
                  patch({ description: e.target.value })
                }
              />
            </label>

            <div className="drawer-actions">
              {task.status !== 'done' && (
                <button className="btn" onClick={() => patch({ status: 'done' })}>
                  Complete
                </button>
              )}
              <button className="btn btn-ghost" onClick={() => api.snoozeTask(taskId, 7).then(onChanged)}>
                Snooze 7d
              </button>
            </div>

            <h3 className="timeline-head">Activity</h3>
            <form className="inline-form" onSubmit={postComment}>
              <input
                placeholder="Add a comment…"
                value={comment}
                onChange={(e) => setComment(e.target.value)}
              />
              <button className="btn" type="submit" disabled={saving}>
                Add
              </button>
            </form>
            <ul className="timeline">
              {events.map((ev) => (
                <li key={ev.id} className="timeline-item">
                  <span className="timeline-meta">
                    {ev.author_name || 'system'} · {fmtDate(ev.created_at)} ·{' '}
                    {ev.event_type}
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
        )}
      </aside>
    </div>
  );
}
