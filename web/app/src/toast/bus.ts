// Lightweight singleton toast bus. One viewport (mounted at app root)
// subscribes; any code imports showToast/dismissToast and fires without
// plumbing props through every component tree. Only one toast is visible
// at a time — a new show() replaces the current one (matches capture-
// style feedback where the latest action is always the relevant signal).

export type ToastKind = 'success' | 'error' | 'pending';

export type ToastAction = {
  label: string;
  onClick: () => void;
};

export type Toast = {
  id: number;
  kind: ToastKind;
  message: string;
  action?: ToastAction;
  // Milliseconds before auto-dismiss. null = sticky until dismissed.
  duration: number | null;
  // Fires when the toast disappears without the action being clicked
  // (auto-dismiss, explicit close, or replacement). Used by the fuse
  // pattern to commit the pending action when the undo window passes.
  onExpire?: () => void;
};

type Listener = (toast: Toast | null) => void;

let counter = 0;
let current: Toast | null = null;
const listeners = new Set<Listener>();

function emit() {
  for (const l of listeners) l(current);
}

export function subscribeToasts(l: Listener): () => void {
  listeners.add(l);
  l(current);
  return () => {
    listeners.delete(l);
  };
}

export function showToast(opts: {
  kind: ToastKind;
  message: string;
  action?: ToastAction;
  duration?: number | null;
  onExpire?: () => void;
}): number {
  // If a toast is already visible, fire its onExpire (so any pending
  // fuse commits before the new toast replaces it). This keeps the
  // model simple: one visible toast = at most one pending commit.
  if (current?.onExpire) current.onExpire();
  const id = ++counter;
  current = {
    id,
    kind: opts.kind,
    message: opts.message,
    action: opts.action,
    duration: opts.duration === undefined ? 5000 : opts.duration,
    onExpire: opts.onExpire,
  };
  emit();
  return id;
}

export function dismissToast(id?: number, options?: { runOnExpire?: boolean }) {
  if (id !== undefined && current?.id !== id) return;
  const t = current;
  current = null;
  if (t && options?.runOnExpire !== false && t.onExpire) t.onExpire();
  emit();
}
