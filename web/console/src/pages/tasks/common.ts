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

// Board columns exclude cancelled — it's a soft-delete, not a workflow lane.
export const BOARD_STATUSES: Status[] = [
  'open',
  'in_progress',
  'blocked',
  'done',
];

export const PRIORITIES = ['low', 'medium', 'high', 'urgent'] as const;
export const PRIORITY_LABEL: Record<string, string> = {
  low: 'Low',
  medium: 'Medium',
  high: 'High',
  urgent: 'Urgent',
};

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
