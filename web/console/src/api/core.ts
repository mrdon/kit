import { apiDelete, apiGet, apiPatch, apiPost, apiPut } from './client';

// --- Shared types ---

export interface Me {
  // "user" for a signed-in person, "device" for a paired shared device.
  // A device has a label and a capability list instead of a user id.
  kind: 'user' | 'device';
  label: string;
  capabilities: string[];
  // The server's build token; the shell reloads when it changes.
  build: string;
  user_id: string;
  display_name: string;
  is_admin: boolean;
  workspace_name: string;
  workspace_icon_url: string;
  logout_url: string;
  // App names disabled for this workspace — drives nav/launcher filtering.
  disabled_apps: string[];
}

export interface Integration {
  name: string;
  description: string;
  slug: string;
  connected: boolean;
  detail: string;
  status_error: string;
  manage_url: string;
}

// IntegrationType is one registered connector plus this caller's connection
// state, from the integration catalog. Drives the connect/delete UI.
export interface IntegrationType {
  provider: string;
  auth_type: string;
  display_name: string;
  description: string;
  scope: string; // "tenant" | "user"
  connected: boolean;
  integration_id: string;
  can_manage: boolean;
}

export interface WidgetToken {
  id: string;
  placeholder: string;
  allowed_origins: string[];
  created_at: string;
  last_used_at: string;
}

export interface MintedToken {
  embed_snippet: string;
  allowed_origins: string[];
}

export interface SquareShiftRun {
  status: string; // "completed" | "failed"
  triggered_by: string;
  created: number;
  updated: number;
  deleted: number;
  duration_ms: number;
  error: string;
  at: string;
}

export interface SquareShiftsStatus {
  square_connected: boolean;
  google_connected: boolean;
  enabled: boolean;
  recent: SquareShiftRun[];
}

export interface RoleInfo {
  name: string;
  description: string;
  member_count: number;
  // The universal "member" catchall: everyone holds it and it can't be
  // toggled, so the UI renders its column locked.
  catchall: boolean;
}

export interface RoleUser {
  user_id: string;
  slack_user_id: string;
  display_name: string;
  roles: string[];
}

export interface RolesMatrix {
  roles: RoleInfo[];
  users: RoleUser[];
}

export interface Task {
  id: string;
  title: string;
  description?: string;
  status: string;
  priority: string;
  category?: string;
  blocked_reason?: string;
  scope_id: string;
  assignee_user_id?: string;
  due_date?: string;
  snoozed_until?: string;
  created_at: string;
  updated_at: string;
  closed_at?: string;
  assignee_name?: string;
  role_name?: string;
}

export interface TaskEvent {
  id: string;
  event_type: string;
  content?: string;
  old_value?: string;
  new_value?: string;
  author_name?: string;
  created_at: string;
}

export interface TasksMeta {
  roles: string[];
  statuses: string[];
  priorities: string[];
  categories: string[];
}

export interface EmailIntake {
  enabled: boolean;
  schedule: string;
  extra_instructions: string;
  last_scanned_at: string | null;
  last_run_at: string | null;
  has_mailbox: boolean;
  default_instructions: string;
}

export interface EmailIntakeBody {
  enabled: boolean;
  schedule: string;
  extra_instructions: string;
}

export interface TaskFilters {
  status?: string;
  priority?: string;
  category?: string;
  role_scope?: string;
  search?: string;
  assigned_to_me?: boolean;
  unassigned?: boolean;
  overdue?: boolean;
  include_closed?: boolean;
}

export interface CreateTaskBody {
  title: string;
  description?: string;
  priority?: string;
  role_scope?: string;
  assignee?: string;
  due_date?: string;
}

export interface UpdateTaskBody {
  title?: string;
  description?: string;
  status?: string;
  priority?: string;
  category?: string;
  blocked_reason?: string;
  assignee?: string;
  clear_assignee?: boolean;
  role_scope?: string;
  due_date?: string;
  clear_due_date?: boolean;
}

export interface SkillSummary {
  id: string; // empty for builtins
  name: string;
  description: string;
  // Scope tier from the backend's shared projection. scope is the picker
  // value ("tenant"/role name); scope_kind/scope_label drive grouping.
  scope: string;
  scope_kind: string;
  scope_label: string;
  builtin: boolean;
  editable: boolean;
}

export interface SkillFile {
  id: string;
  filename: string;
}

export interface SkillDetail {
  id: string;
  name: string;
  description: string;
  content: string;
  builtin: boolean;
  editable: boolean;
  // Current scope tier: "tenant" (public) or a role name.
  scope: string;
  files: SkillFile[];
}

export interface SkillsMeta {
  roles: string[];
  is_admin: boolean;
  // The universal catchall role every member holds; the UI shows skills
  // scoped to it as "All members" (vs tenant:* which is also public).
  catchall_role: string;
}

export interface CreateSkillBody {
  name: string;
  description: string;
  content: string;
  scope?: string;
}

export interface UpdateSkillBody {
  name?: string;
  description?: string;
  content?: string;
  // "tenant" (public) or a role name. Omit to leave scope unchanged.
  scope?: string;
}

// Shared shape for the Skills and Jobs scope pickers.
export interface ScopeMeta {
  roles: string[];
  is_admin: boolean;
  catchall_role: string;
}

// JobPolicy mirrors models.Policy — every field optional; it only ever
// constrains a scheduled agent (allow-list, force-gate, pinned args).
export interface JobPolicy {
  allowed_tools?: string[];
  force_gate?: string[];
  pinned_args?: Record<string, Record<string, unknown>>;
}

export interface JobView {
  id: string;
  description: string;
  job_type: string;
  status: string;
  schedule: string;
  cron_expr: string;
  run_once: boolean;
  timezone: string;
  channel_id: string;
  next_run_at: string;
  last_run_at?: string;
  last_error?: string;
  model: string;
  skill_id?: string;
  skill_name: string;
  policy: JobPolicy | null;
  policy_summary: string;
  editable: boolean;
  scope_kind: string; // "builtin" | "everyone" | "role" | "personal"
  scope_label: string;
  scope: string; // picker value: "user" | "tenant" | role name
  created_at: string;
}

export interface UpdateJobBody {
  description?: string;
  // "" clears the linked skill; omit to leave it unchanged.
  skill_name?: string;
  // A provided object replaces the policy wholesale; omit to leave unchanged.
  policy?: JobPolicy;
  // "user", "tenant" (admin only), or a role name. Omit to leave unchanged.
  scope?: string;
}

function taskQuery(f: TaskFilters): string {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(f)) {
    if (v === undefined || v === '' || v === false) continue;
    p.set(k, String(v));
  }
  const qs = p.toString();
  return qs ? `?${qs}` : '';
}

export interface WorkspaceApp {
  name: string;
  display_name: string;
  description: string;
  enabled: boolean;
  // Short "how much is this used" hint (e.g. "8 secrets"); empty if none.
  usage: string;
}

export const coreApi = {
  me: () => apiGet<Me>('/me'),
  integrations: () => apiGet<Integration[]>('/integrations'),
  integrationCatalog: () => apiGet<IntegrationType[]>('/integration-catalog'),
  integrationConnect: (provider: string, authType: string) =>
    apiPost<{ url: string }>('/integration-catalog/connect', {
      provider,
      auth_type: authType,
    }),
  integrationDelete: (id: string) =>
    apiDelete<void>(`/integrations/${encodeURIComponent(id)}`),

  apps: () => apiGet<WorkspaceApp[]>('/apps'),
  setApp: (name: string, enabled: boolean) =>
    apiPut<{ enabled: boolean }>(`/apps/${encodeURIComponent(name)}`, { enabled }),

  roles: () => apiGet<RolesMatrix>('/roles'),
  assignRole: (slackUserID: string, roleName: string) =>
    apiPost<void>('/roles/assign', {
      slack_user_id: slackUserID,
      role_name: roleName,
    }),
  unassignRole: (slackUserID: string, roleName: string) =>
    apiPost<void>('/roles/unassign', {
      slack_user_id: slackUserID,
      role_name: roleName,
    }),

  tasksMeta: () => apiGet<TasksMeta>('/tasks/meta'),
  listTasks: (f: TaskFilters = {}) =>
    apiGet<{ tasks: Task[] }>(`/tasks${taskQuery(f)}`),
  getTask: (id: string) =>
    apiGet<{ task: Task; events: TaskEvent[] }>(`/tasks/${id}`),
  createTask: (body: CreateTaskBody) =>
    apiPost<{ task: Task }>('/tasks', body),
  updateTask: (id: string, body: UpdateTaskBody) =>
    apiPatch<{ task: Task }>(`/tasks/${id}`, body),
  completeTask: (id: string) =>
    apiPost<{ task: Task }>(`/tasks/${id}/complete`),
  categorizeTasks: () => apiPost<{ queued: number }>('/tasks/categorize'),
  snoozeTask: (id: string, days: number) =>
    apiPost<{ task: Task }>(`/tasks/${id}/snooze`, { days }),
  addTaskComment: (id: string, content: string) =>
    apiPost<void>(`/tasks/${id}/comments`, { content }),

  emailIntake: () => apiGet<EmailIntake>('/email-intake'),
  setEmailIntake: (body: EmailIntakeBody) =>
    apiPut<EmailIntake>('/email-intake', body),
  runEmailIntake: () =>
    apiPost<{ status: string }>('/email-intake/run'),

  widgetTokens: () => apiGet<{ tokens: WidgetToken[] }>('/widget/tokens'),
  mintWidgetToken: (origin: string) =>
    apiPost<MintedToken>('/widget/tokens', { origin }),
  revokeWidgetToken: (id: string) =>
    apiPost<void>(`/widget/tokens/${encodeURIComponent(id)}/revoke`),

  squareShiftsStatus: () => apiGet<SquareShiftsStatus>('/square-shifts/status'),
  squareShiftsSync: () => apiPost<SquareShiftsStatus>('/square-shifts/sync'),

  skillsMeta: () => apiGet<SkillsMeta>('/skills/meta'),
  listSkills: (search = '') =>
    apiGet<{ skills: SkillSummary[] }>(
      `/skills${search ? `?search=${encodeURIComponent(search)}` : ''}`,
    ),
  getSkill: (id: string) =>
    apiGet<{ skill: SkillDetail }>(`/skills/${encodeURIComponent(id)}`),
  createSkill: (body: CreateSkillBody) =>
    apiPost<{ skill: { id: string } }>('/skills', body),
  updateSkill: (id: string, body: UpdateSkillBody) =>
    apiPatch<void>(`/skills/${id}`, body),
  deleteSkill: (id: string) => apiDelete<void>(`/skills/${id}`),
  listSkillFiles: (id: string) =>
    apiGet<{ files: SkillFile[] }>(`/skills/${id}/files`),
  addSkillFile: (id: string, filename: string, content: string) =>
    apiPost<{ file: SkillFile }>(`/skills/${id}/files`, { filename, content }),
  deleteSkillFile: (fileId: string) =>
    apiDelete<void>(`/skills/files/${fileId}`),

  jobsMeta: () => apiGet<ScopeMeta>('/jobs/meta'),
  listJobs: () => apiGet<{ jobs: JobView[] }>('/jobs'),
  getJob: (id: string) => apiGet<{ job: JobView }>(`/jobs/${id}`),
  updateJob: (id: string, body: UpdateJobBody) =>
    apiPatch<{ job: JobView }>(`/jobs/${id}`, body),
  deleteJob: (id: string) => apiDelete<void>(`/jobs/${id}`),
};
