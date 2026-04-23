import { Link } from 'react-router-dom';
import type { KindRenderer } from '.';
import type { StackItem } from '../types';

type DigestRow = {
  id: string;
  title: string;
  priority: 'low' | 'medium' | 'high' | 'urgent';
  due_date?: string;
  snoozed_until: string;
};

type DigestMetadata = {
  items: DigestRow[];
};

function digestMeta(item: StackItem): DigestMetadata | undefined {
  return item.metadata as DigestMetadata | undefined;
}

const FACE_PREVIEW = 3;

function Face({ item }: { item: StackItem }) {
  const rows = digestMeta(item)?.items ?? [];
  const preview = rows.slice(0, FACE_PREVIEW);
  const more = rows.length - preview.length;
  return (
    <div className="todo-face">
      <ul className="snoozed-face-list">
        {preview.map((r) => (
          <li key={r.id}>
            <span className={`priority-chip priority-${r.priority}`}>
              {r.priority}
            </span>
            <span className="snoozed-face-title">{r.title}</span>
          </li>
        ))}
      </ul>
      <div className="hint">
        {more > 0 ? `+${more} more · tap for all` : 'tap for details'}
      </div>
    </div>
  );
}

function Detail({ item }: { item: StackItem }) {
  const rows = digestMeta(item)?.items ?? [];
  if (rows.length === 0) {
    return (
      <div className="todo-meta">
        <div>Nothing snoozed.</div>
      </div>
    );
  }
  return (
    <ul className="snoozed-digest">
      {rows.map((r) => (
        <li key={r.id}>
          <Link to={`/stack/todo/todo/${r.id}`} className="snoozed-row">
            <span className="snoozed-row-title">{r.title}</span>
            <span className="snoozed-row-meta">
              <span className={`priority-chip priority-${r.priority}`}>
                {r.priority}
              </span>
              {r.due_date && (
                <span className="snoozed-row-due">
                  due {formatDate(r.due_date)}
                </span>
              )}
              <span className="snoozed-row-wake">
                wakes {formatRelative(r.snoozed_until)}
              </span>
            </span>
          </Link>
        </li>
      ))}
    </ul>
  );
}

function formatDate(iso: string): string {
  return new Date(iso).toLocaleDateString(undefined, { timeZone: 'UTC' });
}

// formatRelative renders a snoozed_until timestamp as a short, friendly
// label relative to now: "in 3h", "tomorrow", "in 4d". Past values (the
// pile should be free of these, but just in case) fall through to "soon".
function formatRelative(iso: string): string {
  const target = new Date(iso).getTime();
  const diffMs = target - Date.now();
  if (diffMs <= 0) return 'soon';
  const hours = Math.round(diffMs / (60 * 60 * 1000));
  if (hours < 24) return hours <= 1 ? 'in 1h' : `in ${hours}h`;
  const days = Math.round(hours / 24);
  if (days === 1) return 'tomorrow';
  return `in ${days}d`;
}

export const todoSnoozedDigest: KindRenderer = { Face, Detail };
