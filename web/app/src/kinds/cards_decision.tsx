import type { KindRenderer } from '.';
import type { DecisionMetadata, StackItem, TaskStatus } from '../types';

function meta(item: StackItem): DecisionMetadata | undefined {
  return item.metadata as DecisionMetadata | undefined;
}

function Face({ item }: { item: StackItem }) {
  const m = meta(item);
  if (!m) return null;
  const rec = m.options.find((o) => o.option_id === m.recommended_option_id);
  return (
    <div className="hint">
      Swipe right to {rec?.label ?? 'approve default'} · tap for options
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
  const task = extras?.task as TaskStatus | undefined;
  if (!m) return null;
  return (
    <>
      <div className="options">
        {m.options.map((o) => (
          <button
            key={o.option_id}
            disabled={busy}
            onClick={() => onAction('resolve', { option_id: o.option_id })}
            className={o.option_id === m.recommended_option_id ? 'recommended' : ''}
          >
            <div className="label">{o.label}</div>
            {o.prompt && <div className="prompt">{o.prompt}</div>}
          </button>
        ))}
      </div>
      {task && (
        <div className="task-status">
          <div><strong>Kit's task</strong></div>
          <div>Status: {task.status}</div>
          {task.last_run_at && (
            <div>Last run: {new Date(task.last_run_at).toLocaleString()}</div>
          )}
          {task.last_error && (
            <div style={{ color: 'var(--tier-critical-accent)' }}>
              Error: {task.last_error}
            </div>
          )}
        </div>
      )}
    </>
  );
}

export const cardsDecision: KindRenderer = { Face, Detail };
