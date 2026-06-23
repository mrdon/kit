import { SLUG } from './workspace';

// The console talks to JSON endpoints under /{slug}/web/api/*. Those
// routes return 401 (never a 303-to-login) on a missing/expired session
// — a redirect would dump login HTML into fetch().json(). We intercept
// 401 here and do the client-side bounce to the login interstitial,
// preserving where the user was via return_to.
//
// State-changing requests carry the X-Kit-Web: 1 header. The server
// requires it on every POST/PATCH/DELETE; sending a custom header lifts
// the request out of the CORS "simple request" category, which is the
// CSRF guard (same pattern as the cards app's X-Kit-Chat / vault's
// X-Kit-Vault).
const CSRF_HEADER = 'X-Kit-Web';

// The console API lives at /{slug}/api/... — fetch-only, not under the
// /web shell prefix. The cards service worker skips anything containing
// /api/, so these responses are never cached.
export const API_BASE = `/${SLUG}/api`;

function loginRedirect(): never {
  const returnTo = encodeURIComponent(
    location.pathname + location.search,
  );
  window.location.href = `/${SLUG}/login?return_to=${returnTo}`;
  throw new Error('redirecting to login');
}

async function parse<T>(r: Response): Promise<T> {
  if (r.status === 401) loginRedirect();
  if (!r.ok) {
    let msg = `${r.status} ${r.statusText}`;
    try {
      const body = await r.json();
      if (body?.error) msg = body.error;
    } catch {
      /* non-JSON error body */
    }
    throw new Error(msg);
  }
  if (r.status === 204) return undefined as T;
  return r.json() as Promise<T>;
}

export async function apiGet<T>(path: string): Promise<T> {
  const r = await fetch(`${API_BASE}${path}`, { credentials: 'same-origin' });
  return parse<T>(r);
}

async function mutate<T>(
  method: 'POST' | 'PATCH' | 'PUT' | 'DELETE',
  path: string,
  body?: unknown,
): Promise<T> {
  const r = await fetch(`${API_BASE}${path}`, {
    method,
    credentials: 'same-origin',
    headers: {
      'Content-Type': 'application/json',
      [CSRF_HEADER]: '1',
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  return parse<T>(r);
}

export const apiPost = <T>(path: string, body?: unknown) =>
  mutate<T>('POST', path, body);
export const apiPatch = <T>(path: string, body?: unknown) =>
  mutate<T>('PATCH', path, body);
export const apiPut = <T>(path: string, body?: unknown) =>
  mutate<T>('PUT', path, body);
export const apiDelete = <T>(path: string, body?: unknown) =>
  mutate<T>('DELETE', path, body);

// --- Shared types ---

export interface Me {
  user_id: string;
  display_name: string;
  is_admin: boolean;
  workspace_name: string;
  workspace_icon_url: string;
  logout_url: string;
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

export interface NetlifySiteOption {
  id: string;
  name: string;
  url: string;
}

export interface NetlifySiteGroup {
  team: string;
  sites: NetlifySiteOption[];
}

export interface NetlifyStatus {
  netlify_configured: boolean;
  github_configured: boolean;
  netlify_connected: boolean;
  netlify_site_name: string;
  netlify_repo_owner: string;
  netlify_repo_name: string;
  netlify_needs_picker: boolean;
  netlify_sites_error: string;
  sites_by_team: NetlifySiteGroup[];
  github_connected: boolean;
  github_account_login: string;
  github_installation_id: number;
  netlify_connect_url: string;
  github_connect_url: string;
  github_disconnect_url: string;
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

export interface ExpenseItem {
  id: string;
  report_id: string;
  attachment_id?: string;
  vendor?: string;
  spent_on?: string;
  amount_cents: number;
  tax_cents: number;
  category?: string;
  note?: string;
  sort_order: number;
}

export interface ExpenseReport {
  id: string;
  title: string;
  description?: string;
  status: string;
  scope_id: string;
  submitter_user_id: string;
  submitter_email?: string;
  submitter_name?: string;
  approver_user_id?: string;
  decided_by_user_id?: string;
  rejection_reason?: string;
  total_cents: number;
  currency: string;
  submitted_at?: string;
  decided_at?: string;
  reimbursed_at?: string;
  created_at: string;
  updated_at: string;
  items?: ExpenseItem[];
}

export interface ExpenseEvent {
  id: string;
  event_type: string;
  content?: string;
  old_value?: string;
  new_value?: string;
  created_at: string;
}

export interface ExpensesMeta {
  roles: string[];
  statuses: string[];
  currencies: string[];
  is_admin: boolean;
}

export interface ExpensePolicy {
  approver_role?: string;
  approver_user_id?: string;
  intake_enabled: boolean;
  intake_role?: string;
  intake_currency?: string;
}

export interface IntakeConfigBody {
  enabled: boolean;
  role: string;
  currency: string;
}

export interface ExpenseFilters {
  status?: string;
  mine_only?: boolean;
  search?: string;
  include_closed?: boolean;
}

export interface CreateExpenseBody {
  title: string;
  description?: string;
  currency?: string;
  role_scope?: string;
  approver?: string;
}

export interface ExpenseItemBody {
  amount?: string;
  vendor?: string;
  spent_on?: string;
  tax?: string;
  category?: string;
  note?: string;
  attachment_id?: string;
}

export interface SkillScope {
  type: string;
  value: string;
}

export interface SkillSummary {
  id: string; // empty for builtins
  name: string;
  description: string;
  scopes: SkillScope[];
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

export const api = {
  me: () => apiGet<Me>('/me'),
  integrations: () => apiGet<Integration[]>('/integrations'),

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

  widgetTokens: () => apiGet<{ tokens: WidgetToken[] }>('/widget/tokens'),
  mintWidgetToken: (origin: string) =>
    apiPost<MintedToken>('/widget/tokens', { origin }),
  revokeWidgetToken: (id: string) =>
    apiPost<void>(`/widget/tokens/${encodeURIComponent(id)}/revoke`),

  netlifyStatus: () => apiGet<NetlifyStatus>('/netlify/status'),
  netlifyPickSite: (siteId: string) =>
    apiPost<{ message: string }>('/netlify/site', { site_id: siteId }),
  netlifyDisconnect: () => apiPost<void>('/netlify/disconnect'),

  expensesMeta: () => apiGet<ExpensesMeta>('/expenses/meta'),
  listExpenses: (f: ExpenseFilters = {}) =>
    apiGet<{ reports: ExpenseReport[] }>(`/expenses${expenseQuery(f)}`),
  getExpense: (id: string) =>
    apiGet<{ report: ExpenseReport; events: ExpenseEvent[]; can_approve: boolean }>(
      `/expenses/${id}`,
    ),
  expensePolicy: () => apiGet<{ policy: ExpensePolicy }>('/expenses/policy'),
  setExpensePolicy: (body: { approver_role?: string; approver?: string }) =>
    apiPut<{ policy: ExpensePolicy }>('/expenses/policy', body),
  setExpenseIntake: (body: IntakeConfigBody) =>
    apiPut<{ policy: ExpensePolicy }>('/expenses/intake-config', body),
  createExpense: (body: CreateExpenseBody) =>
    apiPost<{ report: ExpenseReport }>('/expenses', body),
  assignExpenseApprover: (id: string, approver: string) =>
    apiPost<{ report: ExpenseReport }>(`/expenses/${id}/approver`, { approver }),
  addExpenseItem: (id: string, body: ExpenseItemBody) =>
    apiPost<{ item: ExpenseItem }>(`/expenses/${id}/items`, body),
  updateExpenseItem: (id: string, itemId: string, body: ExpenseItemBody) =>
    apiPatch<{ item: ExpenseItem }>(`/expenses/${id}/items/${itemId}`, body),
  removeExpenseItem: (id: string, itemId: string) =>
    apiDelete<void>(`/expenses/${id}/items/${itemId}`),
  submitExpense: (id: string) =>
    apiPost<{ report: ExpenseReport }>(`/expenses/${id}/submit`),
  approveExpense: (id: string) =>
    apiPost<{ report: ExpenseReport }>(`/expenses/${id}/approve`),
  rejectExpense: (id: string, reason: string) =>
    apiPost<{ report: ExpenseReport }>(`/expenses/${id}/reject`, { reason }),
  reimburseExpense: (id: string) =>
    apiPost<{ report: ExpenseReport }>(`/expenses/${id}/reimburse`),
  reopenExpense: (id: string) =>
    apiPost<{ report: ExpenseReport }>(`/expenses/${id}/reopen`),
  deleteExpense: (id: string) => apiDelete<void>(`/expenses/${id}`),
  addExpenseComment: (id: string, content: string) =>
    apiPost<void>(`/expenses/${id}/comments`, { content }),

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

function expenseQuery(f: ExpenseFilters): string {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(f)) {
    if (v === undefined || v === '' || v === false) continue;
    p.set(k, String(v));
  }
  const qs = p.toString();
  return qs ? `?${qs}` : '';
}
