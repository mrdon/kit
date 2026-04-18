// Mirror of internal/apps/cards/shared/stackitem.go. Keep in sync.

export type PriorityTier =
  | 'critical'
  | 'high'
  | 'elevated'
  | 'medium'
  | 'low'
  | 'minimal';

export type SwipeDirection = 'right' | 'left' | 'tap';

export type StackAction = {
  id: string;
  direction: SwipeDirection;
  label: string;
  emoji: string;
  params?: unknown;
};

export type StackBadge = {
  label: string;
  tone: 'urgent' | 'warn' | 'info';
};

export type StackItem<M = unknown> = {
  source_app: string;
  kind: string;
  kind_label: string;
  icon?: string;
  id: string;
  title: string;
  body: string;
  priority_tier: PriorityTier;
  actions: StackAction[];
  badges?: StackBadge[];
  metadata?: M;
  created_at: string;
};

export type StackResponse = {
  items: StackItem[];
  next_cursors?: Record<string, string>;
  degraded?: { source_app: string; error_code: string }[];
};

export type DetailResponse<M = unknown> = {
  item: StackItem<M>;
  extras?: Record<string, unknown>;
};

export type ActionResult = {
  item?: StackItem;
  removed_ids?: string[];
};

// Per-kind metadata types. Components narrow via the "source_app:kind" key.

export type DecisionOption = {
  option_id: string;
  sort_order: number;
  label: string;
  prompt?: string;
};

export type DecisionMetadata = {
  priority: 'low' | 'medium' | 'high';
  recommended_option_id?: string;
  resolved_option_id?: string;
  resolved_task_id?: string;
  options: DecisionOption[];
};

export type BriefingMetadata = {
  severity: 'info' | 'notable' | 'important';
};

export type TodoMetadata = {
  due_date?: string;
  priority: 'low' | 'medium' | 'high' | 'urgent';
  status: 'open' | 'in_progress' | 'blocked' | 'done';
  assigned_to?: string;
  role_scope?: string;
};

export type TodoEvent = {
  id: string;
  tenant_id: string;
  todo_id: string;
  author_id?: string;
  event_type: 'comment' | 'status_change' | 'assignment' | 'priority_change';
  content?: string;
  old_value?: string;
  new_value?: string;
  created_at: string;
};

// Task sidecar attached to resolved decision cards.
export type TaskStatus = {
  id: string;
  status: string;
  description: string;
  last_run_at?: string;
  last_error?: string;
};

// The compound client key used as the React list key and returned in
// ActionResult.removed_ids. Must match server-side shared.Key.
export const itemKey = (i: Pick<StackItem, 'source_app' | 'kind' | 'id'>): string =>
  `${i.source_app}:${i.kind}:${i.id}`;

export const kindKey = (i: Pick<StackItem, 'source_app' | 'kind'>): string =>
  `${i.source_app}:${i.kind}`;
