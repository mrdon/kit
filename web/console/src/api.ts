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

// A kiosk board: one unattended screen. `key` is baked into the machine's
// browser homepage, so it is the stable half; `public_url` is what an admin
// copies onto the machine. `last_seen_at` is the only health signal there is
// — it is set by the screen's own polling.
// A published menu board. Read-only in the console: boards are authored and
// pushed with set_menu_board, and this page only answers "what URL do I
// paste into the screen?".
export interface MenuBoard {
  key: string;
  name: string;
  public_url: string;
  updated_at: string;
  taps: number;
  panels: number;
  /** Set when a stored board no longer parses, so the page can say so. */
  error?: string;
}

export interface KioskUrlChange {
  url: string;
  replaced_at: string;
}

export interface KioskBoard {
  id: string;
  key: string;
  name: string;
  url: string;
  notes: string;
  public_url: string;
  last_seen_at: string | null;
  updated_at: string;
  /** What this board pointed at before, newest first. */
  recent_urls: KioskUrlChange[] | null;
}

export interface KioskBoardInput {
  key?: string;
  name: string;
  url: string;
  notes: string;
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

// ---------------------------------------------------------------- events

export interface EventRecord {
  id: string;
  title: string;
  slug: string;
  summary?: string;
  description?: string;
  prep_notes?: string;
  location?: string;
  starts_at: string;
  ends_at?: string;
  all_day: boolean;
  timezone: string;
  rrule?: string;
  // Explicit extra dates the event also happens on. `starts_at` is the first
  // occurrence, so the full list a person sees is [starts_at, ...rdates].
  rdates?: string[];
  // The next occurrence at or after today, expanded server-side from whichever
  // repeat mechanism the event uses. Absent once every date has passed.
  // Prefer this over starts_at when showing "when is this" — starts_at is the
  // FIRST occurrence and for a series is routinely months behind.
  next_occurrence?: string;
  // How many dates an explicit list holds, counting the first. 0 for a
  // one-off, and for a rule-driven series, which has no finite count.
  date_count?: number;
  // Two orthogonal axes, not one. `status` is whether the event is settled;
  // `visibility` is whether the public may see it. A confirmed private booking
  // is published AND private.
  status: 'draft' | 'published' | 'cancelled';
  visibility: 'public' | 'private';
  venue: 'onsite' | 'offsite';
  space_impact: 'none' | 'partial';
  notify_food_partner: boolean;
  // Editorial prominence. 'featured' is what the website leads with;
  // 'background' is a standing offer (a weekly pizza deal, happy hour) that
  // never takes the headline off a real event on the same day.
  prominence: 'featured' | 'normal' | 'background';
  price_cents?: number;
  currency: string;
  capacity?: number;
  expected_attendance?: number;
  registration_url?: string;
  // Present when a poster has been uploaded. The id itself is not useful to
  // the client; it is the has-a-poster flag.
  hero_attachment_id?: string;
  created_at: string;
  updated_at: string;
}

export interface EventsPendingChange {
  action: string;
  title: string;
  slug: string;
  actor?: string;
  at: string;
}

export interface EventsSiteStatus {
  hook_configured: boolean;
  built_at?: string;
  built_by?: string;
  // Server always sends [], never null.
  pending: EventsPendingChange[] | null;
  pending_truncated: boolean;
}

export interface EventsSettingsSummary {
  timezone: string;
  calendar_configured: boolean;
  public_url_template: string;
}

export interface EventsMeta {
  statuses: string[];
  visibilities: string[];
  venues: string[];
  space_impacts: string[];
  settings: EventsSettingsSummary;
}

export interface EventsCalendarOption {
  id: string;
  name: string;
  writable: boolean;
  primary: boolean;
}

export interface EventsRun {
  at: string;
  ok: boolean;
  triggered_by: string;
  created: number;
  updated: number;
  deleted: number;
  error?: string;
}

export interface EventsSettings {
  calendar_id: string;
  timezone: string;
  public_url_template: string;
  feed_token?: string;
  feed_url?: string;
  google_connected: boolean;
  // The address a calendar must be shared with. Absent until a credential is
  // loaded; shown so nobody has to go digging for it.
  service_account_email?: string;
  // Never returned — the hook URL carries its own secret. The UI only learns
  // whether one is set, via EventsSiteStatus.hook_configured.
  site_build_hook_url?: string;
  // Server always sends [], never null — the client stays defensive anyway,
  // since a nil Go slice marshals to null and that crashed this page once.
  calendars: EventsCalendarOption[] | null;
  calendars_error?: string;
  recent: EventsRun[] | null;
}


// One person on the upcoming published Square schedule — the left side of the
// staff mapping. Shifts is how many they have in the window, so the admin can
// see who matters most to map.
export interface EventsStaffMember {
  team_member_id: string;
  name: string;
  shifts: number;
}

// A Slack workspace member — the right side. Read from Slack rather than Kit's
// user table, because staff who have never messaged the bot have no Kit row.
export interface EventsSlackOption {
  slack_user_id: string;
  name: string;
}

export interface EventsStaffMapping {
  square_team_member_id: string;
  user_id: string;
  slack_user_id: string;
  display_name: string;
}

export interface EventsNoticeRun {
  at: string;
  ok: boolean;
  triggered_by: string;
  posted: boolean;
  skipped: boolean;
  mentions: number;
  unmapped: number;
  error?: string;
}

// A channel the notice could be posted to. bot_is_member matters: posting to
// a channel Kit has not been invited to fails at 8am when nobody is watching.
export interface EventsChannelOption {
  id: string;
  name: string;
  bot_is_member: boolean;
  is_private: boolean;
}

export interface EventsStaff {
  square_connected: boolean;
  // Server always sends [], never null — the client stays defensive anyway.
  staff: EventsStaffMember[] | null;
  slack_users: EventsSlackOption[] | null;
  mappings: EventsStaffMapping[] | null;
  notice_channel_id: string;
  channels: EventsChannelOption[] | null;
  channels_error?: string;
  staff_error?: string;
  slack_error?: string;
  recent: EventsNoticeRun[] | null;
}

// Today's post: the channel headline and the detail that threads under it.
export interface EventsDayNotice {
  headline: string;
  detail: string;
  mentions: number;
  unmapped: number;
}

// Every field optional: the console PATCHes only what changed, so an edit made
// here cannot silently revert one made in chat against the same event.
export interface EventInput {
  title?: string;
  prominence?: 'featured' | 'normal' | 'background';
  summary?: string;
  description?: string;
  prep_notes?: string;
  location?: string;
  starts_at?: string;
  ends_at?: string;
  all_day?: boolean;
  timezone?: string;
  repeat_rule?: string;
  // Replaces the whole extra-date list. Omit to leave it alone; send [] to
  // turn a series back into a one-off.
  repeat_dates?: string[];
  visibility?: string;
  venue?: string;
  space_impact?: string;
  price_cents?: number;
  clear_price?: boolean;
  currency?: string;
  capacity?: number;
  clear_capacity?: boolean;
  expected_attendance?: number;
  registration_url?: string;
  notify_food_partner?: boolean;
  slug?: string;
}

export interface EventsReconcilePlan {
  dry_run: boolean;
  empty?: boolean;
  removals?: string[];
  restores?: string[];
  message: string;
}

export const api = {
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

  menuBoards: () => apiGet<{ boards: MenuBoard[] }>('/menu/boards'),
  kioskBoards: () => apiGet<{ boards: KioskBoard[] }>('/kiosk/boards'),
  createKioskBoard: (body: KioskBoardInput) =>
    apiPost<KioskBoard>('/kiosk/boards', body),
  updateKioskBoard: (id: string, body: KioskBoardInput) =>
    apiPatch<KioskBoard>(`/kiosk/boards/${encodeURIComponent(id)}`, body),
  deleteKioskBoard: (id: string) =>
    apiDelete<void>(`/kiosk/boards/${encodeURIComponent(id)}`),

  widgetTokens: () => apiGet<{ tokens: WidgetToken[] }>('/widget/tokens'),
  mintWidgetToken: (origin: string) =>
    apiPost<MintedToken>('/widget/tokens', { origin }),
  revokeWidgetToken: (id: string) =>
    apiPost<void>(`/widget/tokens/${encodeURIComponent(id)}/revoke`),

  netlifyStatus: () => apiGet<NetlifyStatus>('/netlify/status'),
  netlifyPickSite: (siteId: string) =>
    apiPost<{ message: string }>('/netlify/site', { site_id: siteId }),
  netlifyDisconnect: () => apiPost<void>('/netlify/disconnect'),

  eventsMeta: () => apiGet<EventsMeta>('/events/meta'),
  listEvents: (opts: { status?: string; include_past?: boolean } = {}) => {
    const q = new URLSearchParams();
    if (opts.status) q.set('status', opts.status);
    if (opts.include_past) q.set('include_past', 'true');
    const qs = q.toString();
    return apiGet<{ events: EventRecord[]; settings: EventsSettingsSummary }>(
      `/events${qs ? `?${qs}` : ''}`,
    );
  },
  getEvent: (id: string) =>
    apiGet<{ event: EventRecord; occurrences: string[] | null; canonicalURL: string }>(
      `/events/${encodeURIComponent(id)}`,
    ),
  createEvent: (body: EventInput) => apiPost<{ event: EventRecord }>('/events', body),
  cloneEvent: (id: string, body: { starts_at?: string; title?: string } = {}) =>
    apiPost<{ event: EventRecord }>(`/events/${encodeURIComponent(id)}/clone`, body),
  updateEvent: (id: string, body: EventInput) =>
    apiPatch<{ event: EventRecord }>(`/events/${encodeURIComponent(id)}`, body),
  publishEvent: (id: string) =>
    apiPost<{ event: EventRecord; warnings: string[] | null }>(
      `/events/${encodeURIComponent(id)}/publish`,
    ),
  unpublishEvent: (id: string) =>
    apiPost<{ event: EventRecord }>(`/events/${encodeURIComponent(id)}/unpublish`),
  cancelEvent: (id: string) =>
    apiPost<{ event: EventRecord }>(`/events/${encodeURIComponent(id)}/cancel`),
  reopenEvent: (id: string) =>
    apiPost<{ event: EventRecord }>(`/events/${encodeURIComponent(id)}/reopen`),

  eventsSettings: () => apiGet<EventsSettings>('/events/settings'),
  saveEventsSettings: (body: {
    calendar_id?: string;
    timezone?: string;
    public_url_template?: string;
  }) => apiPut<{ settings: EventsSettings; warning: string }>('/events/settings', body),
  rotateEventsFeedToken: () =>
    apiPost<{ settings: EventsSettings; warning: string }>('/events/settings/feed-token'),
  eventsSyncNow: () => apiPost<{ message: string }>('/events/sync'),
  // Poster upload is multipart, so it bypasses the JSON helpers -- but it
  // still needs the CSRF header and the 401 bounce.
  uploadEventPoster: async (id: string, file: File) => {
    const body = new FormData();
    body.append('poster', file);
    const r = await fetch(`${API_BASE}/events/${id}/poster`, {
      method: 'POST',
      credentials: 'same-origin',
      headers: { [CSRF_HEADER]: '1' },
      body,
    });
    return parse<{ event: EventRecord }>(r);
  },
  deleteEvent: (id: string) => apiDelete<{ deleted: boolean }>(`/events/${id}`),
  deleteEventPoster: (id: string) =>
    apiDelete<{ event: EventRecord }>(`/events/${id}/poster`),
  // Cache-busted on the event's updated_at so a replaced poster shows at once.
  eventPosterURL: (id: string, v?: string) =>
    `${API_BASE}/events/${id}/poster${v ? `?v=${encodeURIComponent(v)}` : ''}`,

  eventsSiteStatus: () => apiGet<EventsSiteStatus>('/events/site'),
  eventsPublishSite: () => apiPost<EventsSiteStatus>('/events/site/publish'),
  eventsReconcile: (apply: boolean) =>
    apiPost<EventsReconcilePlan>(`/events/reconcile${apply ? '?apply=true' : ''}`),

  eventsStaff: () => apiGet<EventsStaff>('/events/staff'),
  // An empty slack_user_id clears the mapping. Returns the whole page payload
  // so a save cannot leave the two dropdowns showing stale pairings.
  saveEventsStaffMapping: (body: {
    square_team_member_id: string;
    slack_user_id: string;
  }) => apiPut<EventsStaff>('/events/staff', body),
  // An empty channel_id turns notices off. Returns the whole page payload so
  // the picker cannot show a stale selection after a change.
  saveEventsNoticeChannel: (body: { channel_id: string }) =>
    apiPut<EventsStaff>('/events/staff/channel', body),
  previewEventsNotices: () =>
    apiPost<{ notice: EventsDayNotice | null; message: string }>(
      '/events/staff/preview',
    ),
  sendEventsNotices: () =>
    apiPost<{ message: string; staff: EventsStaff }>('/events/staff/send'),

  squareShiftsStatus: () => apiGet<SquareShiftsStatus>('/square-shifts/status'),
  squareShiftsSync: () => apiPost<SquareShiftsStatus>('/square-shifts/sync'),

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
