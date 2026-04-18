import type { KindRenderer } from '.';
import type { StackItem, TodoEvent, TodoMetadata } from '../types';

function meta(item: StackItem): TodoMetadata | undefined {
  return item.metadata as TodoMetadata | undefined;
}

function Face({ item }: { item: StackItem }) {
  const m = meta(item);
  return (
    <div className="todo-face">
      {m && (
        <div className="todo-face-meta">
          <span className={`priority-chip priority-${m.priority}`}>
            {m.priority}
          </span>
          {m.assigned_to_name && (
            <span className="assignee">👤 {m.assigned_to_name}</span>
          )}
          {m.role_scope && <span className="role-scope">#{m.role_scope}</span>}
        </div>
      )}
      <div className="hint">Swipe right to complete</div>
    </div>
  );
}

function Detail({
  item,
  extras,
  onAction,
  busy,
}: {
  item: StackItem;
  extras?: Record<string, unknown>;
  onAction: (actionID: string, params?: unknown) => void;
  busy: boolean;
}) {
  const m = meta(item);
  const events = (extras?.events as TodoEvent[] | null) ?? [];
  return (
    <>
      {m && (
        <div className="todo-meta">
          {m.due_date && <div>Due: {formatDate(m.due_date)}</div>}
          <div>Priority: {m.priority}</div>
          <div>Status: {m.status}</div>
          {m.assigned_to_name && <div>Assigned: {m.assigned_to_name}</div>}
          {m.role_scope && <div>Role: {m.role_scope}</div>}
        </div>
      )}
      <div className="acks">
        <button disabled={busy} onClick={() => onAction('complete')}>
          ✅ Complete
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

function describeEvent(e: TodoEvent): string {
  switch (e.event_type) {
    case 'comment':
      return e.content ?? '';
    case 'status_change':
      return `Status: ${e.old_value} → ${e.new_value}${e.content ? ` (${e.content})` : ''}`;
    case 'assignment':
      return `Assigned: ${e.old_value || '—'} → ${e.new_value}`;
    case 'priority_change':
      return `Priority: ${e.old_value} → ${e.new_value}`;
    default:
      return e.event_type;
  }
}

export const todoTodo: KindRenderer = { Face, Detail };
