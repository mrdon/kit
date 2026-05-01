import { useEffect, useRef, useState } from 'react';
import { api } from '../api';
import type { KindRenderer } from '.';
import type { StackItem, TaskEvent, TaskMetadata } from '../types';

function meta(item: StackItem): TaskMetadata | undefined {
  return item.metadata as TaskMetadata | undefined;
}

function Face({ item }: { item: StackItem }) {
  const m = meta(item);
  return (
    <div className="task-face">
      {m && (
        <div className="task-face-meta">
          <span className={`priority-chip priority-${m.priority}`}>
            {m.priority}
          </span>
          {m.role_scope && <span className="role-scope">#{m.role_scope}</span>}
          {m.assignee_name ? (
            <span className="assignee">👤 {m.assignee_name}</span>
          ) : (
            <span className="assignee unassigned">Unassigned</span>
          )}
        </div>
      )}
      <div className="hint">Swipe right ✅ · left 😴 (1d) · tap for more</div>
    </div>
  );
}

function Detail({
  item,
  extras,
  onAction,
  onRefresh,
  busy,
}: {
  item: StackItem;
  extras?: Record<string, unknown>;
  onAction: (actionID: string, params?: unknown) => void;
  onRefresh: () => void;
  busy: boolean;
}) {
  const m = meta(item);
  const events = (extras?.events as TaskEvent[] | null) ?? [];
  const isSnoozed = !!(
    m?.snoozed_until && new Date(m.snoozed_until).getTime() > Date.now()
  );
  const isUnassigned = !m?.assignee_user_id;

  // Regenerate state. Baseline is a fingerprint of the resolution chips
  // and recommended step taken at the moment we fire the action; the
  // server runs Haiku asynchronously and stamps new chip IDs on every
  // successful regen, so we poll getItem until the fingerprint changes.
  // Timeout at 20s so a Haiku error or a same-output regen doesn't
  // leave the button spinning forever.
  const [regenerating, setRegenerating] = useState(false);
  const baselineRef = useRef<string | null>(null);

  useEffect(() => {
    if (!regenerating || baselineRef.current === null) return;
    const fp = JSON.stringify({
      actions: item.actions ?? [],
      rec: item.recommended_next_step ?? null,
    });
    if (fp !== baselineRef.current) {
      setRegenerating(false);
      baselineRef.current = null;
    }
  }, [item, regenerating]);

  useEffect(() => {
    if (!regenerating) return;
    const start = Date.now();
    const timer = window.setInterval(() => {
      if (Date.now() - start > 20000) {
        setRegenerating(false);
        baselineRef.current = null;
        return;
      }
      onRefresh();
    }, 1500);
    return () => window.clearInterval(timer);
  }, [regenerating, onRefresh]);

  const handleRegenerate = async () => {
    if (regenerating || busy) return;
    baselineRef.current = JSON.stringify({
      actions: item.actions ?? [],
      rec: item.recommended_next_step ?? null,
    });
    try {
      await api.doAction(
        item.source_app,
        item.kind,
        item.id,
        'regenerate_resolutions',
      );
    } catch (e) {
      baselineRef.current = null;
      alert((e as Error).message);
      return;
    }
    setRegenerating(true);
  };

  return (
    <>
      {m && (
        <div className="task-meta">
          {m.due_date && <div>Due: {formatDate(m.due_date)}</div>}
          <div>Priority: {m.priority}</div>
          <div>Status: {m.status}</div>
          {m.role_scope && <div>Role: {m.role_scope}</div>}
          <div>
            Assignee:{' '}
            {m.assignee_name ? m.assignee_name : <em>Unassigned</em>}
          </div>
        </div>
      )}
      <div className="acks">
        <button disabled={busy} onClick={() => onAction('complete')}>
          ✅ Complete
        </button>
        {isUnassigned && (
          <button
            disabled={busy}
            onClick={() => {
              onAction('assign_to_me');
              onRefresh();
            }}
          >
            🙋 Assign to me
          </button>
        )}
        {isSnoozed && (
          <button disabled={busy} onClick={() => onAction('wake')}>
            ⏰ Wake now
          </button>
        )}
        <button disabled={busy} onClick={() => onAction('snooze', { days: 1 })}>
          😴 1 day
        </button>
        <button disabled={busy} onClick={() => onAction('snooze', { days: 3 })}>
          😴 3 days
        </button>
        <button disabled={busy} onClick={() => onAction('snooze_until_monday')}>
          😴 Monday
        </button>
      </div>
      <div className="acks" style={{ marginTop: '1rem' }}>
        <button disabled={busy || regenerating} onClick={handleRegenerate}>
          {regenerating ? '⏳ Regenerating…' : '↻ Regenerate suggestions'}
        </button>
        <button
          disabled={busy}
          onClick={() => {
            if (window.confirm('Delete this task?')) onAction('delete');
          }}
        >
          🗑️ Delete
        </button>
      </div>
      {events.length > 0 && (
        <div className="events">
          <h3>Recent activity</h3>
          <ul>
            {events.map((e) => (
              <li key={e.id}>
                <span className="events-ts">
                  {new Date(e.created_at).toLocaleString()}
                </span>{' '}
                {describeEvent(e)}
              </li>
            ))}
          </ul>
        </div>
      )}
    </>
  );
}

// formatDate renders an ISO timestamp as a calendar date in UTC. Due
// dates come from a Postgres DATE column serialized as UTC-midnight; the
// default toLocaleDateString() would shift them to the previous calendar
// day for any user east of UTC, which misrepresents the intent.
function formatDate(iso: string): string {
  return new Date(iso).toLocaleDateString(undefined, { timeZone: 'UTC' });
}

function describeEvent(e: TaskEvent): string {
  switch (e.event_type) {
    case 'comment':
      return e.content ?? '';
    case 'status_change':
      return `Status: ${e.old_value} → ${e.new_value}${e.content ? ` (${e.content})` : ''}`;
    case 'assignment':
      return `Re-scoped: ${e.old_value || '—'} → ${e.new_value}`;
    case 'assignee_change':
      return `Assignee: ${e.old_value || '—'} → ${e.new_value || '—'}`;
    case 'priority_change':
      return `Priority: ${e.old_value} → ${e.new_value}`;
    default:
      return e.event_type;
  }
}

export const taskTask: KindRenderer = { Face, Detail };
