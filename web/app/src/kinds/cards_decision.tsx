import { useState } from 'react';
import type { KindRenderer } from '.';
import type { DecisionMetadata, StackItem, TaskStatus } from '../types';
import { renderToolPreview } from './tool_previews';

// The client treats "resolving" as a transient state while Kit is
// running a gated tool on the server. Must mirror
// internal/apps/cards/models.go CardStateResolving.
const STATE_RESOLVING = 'resolving';

function meta(item: StackItem): DecisionMetadata | undefined {
  return item.metadata as DecisionMetadata | undefined;
}

function Face({ item }: { item: StackItem }) {
  const m = meta(item);
  if (!m) return null;
  const rec = m.options.find((o) => o.option_id === m.recommended_option_id);
  // Gate artifacts (user approving a privileged tool call) render the
  // tool preview directly on the face — the user shouldn't have to
  // drill into detail just to see which email they're approving.
  if (m.is_gate_artifact) {
    const alt = m.options.find((o) => o.option_id !== m.recommended_option_id);
    const approveLabel = rec?.label ?? 'approve';
    const rejectLabel = alt?.label;
    return (
      <div className="gate-face">
        {rec && renderToolPreview(rec.tool_name, rec.tool_arguments)}
        <div className="hint">
          Swipe right to {approveLabel.toLowerCase()}
          {rejectLabel ? ` · swipe left to ${rejectLabel.toLowerCase()}` : ''}
        </div>
      </div>
    );
  }
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

  // Brief optimistic disable so double-taps before the server's
  // FOR UPDATE lock completes don't stack approval attempts.
  const [optimisticDisable, setOptimisticDisable] = useState(false);
  const anyState = (item as unknown as { state?: string }).state;
  const resolving = anyState === STATE_RESOLVING;

  if (!m) return null;

  const disabled = busy || resolving || optimisticDisable;

  const handleResolve = (optionID: string) => {
    setOptimisticDisable(true);
    onAction('resolve', { option_id: optionID });
    // If the server doesn't flip the card out of pending within a
    // reasonable UI window, re-enable so the user can retry.
    setTimeout(() => setOptimisticDisable(false), 300);
  };

  return (
    <>
      {resolving && (
        <div className="card-state-banner card-state-banner--resolving">
          <span className="spinner" aria-hidden="true" />
          Running… (up to 5 min). Kit will update this card when the action completes.
        </div>
      )}
      <div className="options">
        {m.options.map((o) => (
          <div key={o.option_id} className="option">
            {renderToolPreview(o.tool_name, o.tool_arguments)}
            <button
              disabled={disabled}
              onClick={() => handleResolve(o.option_id)}
              className={o.option_id === m.recommended_option_id ? 'recommended' : ''}
            >
              <div className="label">{o.label}</div>
              {!o.tool_name && o.prompt && <div className="prompt">{o.prompt}</div>}
            </button>
          </div>
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
