import { useEffect, useState } from 'react';
import { subscribeToasts, dismissToast, type Toast } from './bus';

/**
 * Single-slot toast viewport mounted at app root. Renders whichever
 * toast the bus last emitted. The auto-dismiss timer runs here (not in
 * the bus) so the countdown restarts if a new toast replaces the old
 * one.
 */
export default function ToastViewport() {
  const [toast, setToast] = useState<Toast | null>(null);

  useEffect(() => subscribeToasts(setToast), []);

  useEffect(() => {
    if (!toast || toast.duration === null) return;
    const id = toast.id;
    const timer = window.setTimeout(() => {
      // Auto-dismiss is the "commit" path for pending actions — run
      // onExpire so the fuse fires.
      dismissToast(id, { runOnExpire: true });
    }, toast.duration);
    return () => window.clearTimeout(timer);
  }, [toast]);

  if (!toast) return null;

  return (
    <div
      className={`toast toast-${toast.kind}`}
      role={toast.kind === 'error' ? 'alert' : 'status'}
      aria-live={toast.kind === 'error' ? 'assertive' : 'polite'}
    >
      <span className="toast-message">{toast.message}</span>
      {toast.action && (
        <button
          type="button"
          className="toast-action"
          onClick={() => {
            // The action runs its own callback (e.g. undo). Don't fire
            // onExpire on dismissal — the pending commit should NOT
            // run when the user explicitly picked "Undo".
            toast.action!.onClick();
            dismissToast(toast.id, { runOnExpire: false });
          }}
        >
          {toast.action.label}
        </button>
      )}
      <button
        type="button"
        className="toast-close"
        aria-label="Dismiss"
        onClick={() => dismissToast(toast.id, { runOnExpire: true })}
      >
        ×
      </button>
    </div>
  );
}
