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
  method: 'POST' | 'PATCH' | 'DELETE',
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
};
