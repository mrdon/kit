import type { KindRenderer } from '.';
import type { StackItem } from '../types';

function Face() {
  return <div className="hint">Swipe right 👍 · left 👎 · tap to open</div>;
}

function Detail({
  onAction,
  busy,
}: {
  item: StackItem;
  extras?: Record<string, unknown>;
  onAction: (actionID: string, params?: unknown) => void;
  busy: boolean;
}) {
  return (
    <div className="acks">
      <button disabled={busy} onClick={() => onAction('ack_archived')}>
        👍 Useful
      </button>
      <button disabled={busy} onClick={() => onAction('ack_dismissed')}>
        👎 Not useful
      </button>
      <button disabled={busy} onClick={() => onAction('ack_saved')}>
        🔖 Save for later
      </button>
    </div>
  );
}

export const cardsBriefing: KindRenderer = { Face, Detail };
