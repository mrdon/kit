// Mirror of internal/apps/cards/models.go Card struct. Keep in sync.

export type CardKind = 'decision' | 'briefing';
export type CardState =
  | 'pending'
  | 'resolved'
  | 'archived'
  | 'dismissed'
  | 'saved'
  | 'cancelled';

export type DecisionPriority = 'low' | 'medium' | 'high';
export type BriefingSeverity = 'info' | 'notable' | 'important';

export type DecisionOption = {
  option_id: string;
  sort_order: number;
  label: string;
  prompt?: string;
};

export type DecisionData = {
  priority: DecisionPriority;
  recommended_option_id?: string;
  resolved_option_id?: string;
  resolved_task_id?: string;
  options: DecisionOption[];
};

export type BriefingData = {
  severity: BriefingSeverity;
};

export type Card = {
  id: string;
  tenant_id: string;
  kind: CardKind;
  title: string;
  body: string;
  state: CardState;
  terminal_at?: string;
  terminal_by?: string;
  created_at: string;
  updated_at: string;
  decision?: DecisionData;
  briefing?: BriefingData;
};

export type StackResponse = { items: Card[] };

export type TaskStatus = {
  id: string;
  status: string;
  description: string;
  last_run_at?: string;
  last_error?: string;
};

export type CardDetailResponse = {
  card: Card;
  task?: TaskStatus;
};
