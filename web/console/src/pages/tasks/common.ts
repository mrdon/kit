// Shared task constants + helpers for the console Tasks UI. Status values
// mirror the app_tasks CHECK constraint (migration 034).
export const STATUSES = [
  'open',
  'in_progress',
  'blocked',
  'done',
  'cancelled',
] as const;
export type Status = (typeof STATUSES)[number];

export const STATUS_LABEL: Record<string, string> = {
  open: 'Open',
  in_progress: 'In Progress',
  blocked: 'Blocked',
  done: 'Done',
  cancelled: 'Cancelled',
};

// Priority bands, highest first — these drive the grouped view's sections.
export const PRIORITIES = ['blocker', 'high', 'normal'] as const;
export type Priority = (typeof PRIORITIES)[number];

export const PRIORITY_LABEL: Record<string, string> = {
  blocker: 'Blocker',
  high: 'High',
  normal: 'Normal',
};

// Uncategorized tasks group under this client-side label (category is null
// until the async categorizer runs, and may stay null for vague tasks).
export const NO_CATEGORY = 'Uncategorized';

export const statusClass = (s: string) => `st-${s}`;
export const priorityClass = (p: string) => `pr-${p}`;

export function fmtDate(iso?: string): string {
  if (!iso) return '';
  // The API sends YYYY-MM-DD for due dates and RFC3339 for timestamps.
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleDateString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
  });
}
