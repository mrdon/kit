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
// The workspace's menu board. One per workspace, read-only in the console:
// the tap list follows Untappd and the presentation is pushed from an AI
// client, so this page only answers "what URL do I paste into the screen?".
export interface MenuBoard {
  name: string;
  public_url: string;
  /** Null until a tap list exists; the address works regardless. */
  updated_at: string | null;
  taps: number;
  panels: number;
  /** True before any tap list is set — the screen shows a placeholder. */
  empty: boolean;
  /** Untappd board being followed; blank when set by hand. */
  source: string;
  synced_at: string | null;
  sync_error?: string;
  /** Set when the stored payload no longer parses. */
  parse_error?: string;
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

// --- Trivia ---
//
// The live host surface. Note there is exactly ONE mutating endpoint for the
// game itself (`triviaAction`): every host click is a guarded transition
// carrying the phase it was made from, so a stale click 409s instead of
// silently skipping a question.

export type {
  BuiltinPack,
  Dataset,
  TriviaGame,
  TriviaSettings,
  TopicCount,
  ImportReport,
  HostFrame,
} from './pages/trivia/common';
import type {
  TriviaGame as TriviaGameT,
  TriviaSettings as TriviaSettingsT,
  TopicCount as TopicCountT,
  HostFrame as HostFrameT,
  ImportReport as ImportReportT,
  Dataset as DatasetT,
  BuiltinPack as BuiltinPackT,
} from './pages/trivia/common';

export interface KioskBoardInput {
  key?: string;
  name: string;
  url: string;
  notes: string;
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

// ------------------------------------------------- event promotion channels

// A channel is a place events get promoted to. Channels are DATA, not code:
// adding "City of Louisville" is a form submission. Only `automated` needs a
// Go connector behind it, which is why the mode picker disables that option
// when none exists.
export type ChannelMode = 'manual' | 'subscribed' | 'automated';

// The three rhythms a campaign step can have. They differ mainly in what
// happens when one is missed:
//   oneshot — stays outstanding until done or the event passes
//   drip    — a timed beat that EXPIRES and quietly drops off
//   cadence — series only; re-arms a fixed interval after it was last done
export type StepKind = 'oneshot' | 'drip' | 'cadence';

export type Prominence = 'background' | 'normal' | 'featured';

export interface ChannelStep {
  key: string;
  label: string;
  kind: StepKind;
  offset_days?: number;
  interval_days?: number;
  expires_after_days?: number;
  // Unset means "inherit the channel's floor". Setting it lets one channel
  // take the announce for a normal event and the full drip only for a
  // featured one.
  min_prominence?: Prominence;
  // False for work no API can do — creating the annual Facebook recurring
  // event, most obviously. Such a step keeps producing a manual row even on
  // an automated channel.
  automatable?: boolean;
}

export interface EventChannel {
  id: string;
  name: string;
  mode: ChannelMode;
  connector?: string;
  submit_url?: string;
  feed_tier?: 'all' | 'highlights' | 'featured';
  // When someone last confirmed a subscribed channel is really pulling the
  // feed. `subscribed` is the only mode that can fail silently, so this is
  // how a dead subscription gets noticed.
  verified_at?: string;
  lead_time_days: number;
  // Whether events we are ATTENDING rather than hosting belong here. Off by
  // default: the chamber already carries someone else's festival from the
  // organiser, but your own Facebook very much wants "come see us at GABF".
  include_offsite: boolean;
  steps: ChannelStep[];
  min_prominence: Prominence;
  active: boolean;
}

// 'todo' is the absence of a stored row, and 'expired' is computed — neither
// is ever written. See the promo.go comment for why.
export type PromoState =
  | 'todo'
  | 'done'
  | 'ignored'
  | 'expired'
  | 'auto_done'
  | 'auto_failed';

export interface PromoItem {
  event_id: string;
  event_title: string;
  event_slug: string;
  event_start: string;
  event_end?: string;
  // What a submission form asks for, carried so the copy button does not send
  // you back to the event to collect the venue and blurb by hand. event_url is
  // the event's own public page — distinct from `url` below, which records
  // where this item was posted.
  event_location?: string;
  event_summary?: string;
  event_url?: string;
  event_timezone?: string;
  event_all_day?: boolean;
  channel_id: string;
  channel_name: string;
  submit_url?: string;
  step_key: string;
  step_label: string;
  step_kind: StepKind;
  state: PromoState;
  url?: string;
  note?: string;
  // When this wants doing. NOT the event date: for a one-shot it is the event
  // minus the channel's lead time, and for a cadence it is the last
  // completion plus the interval. Ordering keys off this.
  due_at: string;
  overdue: boolean;
  last_done_at?: string;
  last_url?: string;
  manual: boolean;
}

export interface PromoPayload {
  items: PromoItem[];
  done: PromoItem[];
  summary: { outstanding: number; overdue: number };
  channels: EventChannel[];
}

export interface ChannelsPayload {
  channels: EventChannel[];
  // The PUBLIC calendar addresses — the copies the website republishes
  // token-free. These are what you send a chamber; Kit's own feed URL would
  // 401 for them.
  feed_urls: { all: string; highlights: string; featured: string };
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

  menuBoard: () => apiGet<MenuBoard>('/menu/board'),
  // --- Trivia ---
  triviaGames: () => apiGet<{ games: TriviaGameT[] }>('/trivia/games'),
  triviaGame: (id: string) =>
    apiGet<{
      game: TriviaGameT;
      topics: TopicCountT[];
      datasets: DatasetT[];
      selected: string[];
      state: HostFrameT;
    }>(`/trivia/games/${id}`),
  // Omitting settings means "same as last time" — the server carries the
  // previous game's setup forward.
  createTriviaGame: (settings?: TriviaSettingsT) =>
    apiPost<TriviaGameT>('/trivia/games', settings ? { settings } : {}),
  updateTriviaGame: (id: string, settings: TriviaSettingsT) =>
    apiPatch<TriviaGameT>(`/trivia/games/${id}`, { settings }),
  deleteTriviaGame: (id: string) => apiDelete<void>(`/trivia/games/${id}`),
  buildTriviaBoard: (id: string, topics: string[], auto: boolean) =>
    apiPost<HostFrameT>(`/trivia/games/${id}/board`, { topics, auto }),
  // The single host action endpoint. from_phase is what makes a double click
  // a 409 rather than a skipped question.
  triviaAction: (id: string, body: Record<string, unknown>) =>
    apiPost<HostFrameT>(`/trivia/games/${id}/action`, body),
  triviaReclaim: (gameId: string, teamId: string) =>
    apiPost<{ code: string }>(`/trivia/games/${gameId}/teams/${teamId}/reclaim`, {}),
  triviaQuestions: () =>
    apiGet<{ total: number; topics: TopicCountT[]; datasets: DatasetT[]; packs: BuiltinPackT[] }>(
      '/trivia/questions'),
  // One click, no download-and-re-upload. Idempotent: the import upserts on
  // the folded prompt, so loading it twice adds nothing.
  loadTriviaPack: (key: string) =>
    apiPost<ImportReportT>(`/trivia/questions/packs/${key}`, {}),
  triviaDatasets: () => apiGet<{ datasets: DatasetT[] }>('/trivia/datasets'),
  renameTriviaDataset: (id: string, name: string, notes: string) =>
    apiPatch<{ datasets: DatasetT[] }>(`/trivia/datasets/${id}`, { name, notes }),
  deleteTriviaDataset: (id: string) => apiDelete<void>(`/trivia/datasets/${id}`),
  // An empty list means "every dataset" — that is what keeps a game playable
  // when the set it pointed at is later deleted.
  setTriviaGameDatasets: (gameId: string, datasetIds: string[]) =>
    apiPut<{ selected: string[]; topics: TopicCountT[] }>(
      `/trivia/games/${gameId}/datasets`, { dataset_ids: datasetIds }),

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


  eventsPromo: () => apiGet<PromoPayload>('/events/promo'),
  markEventPromo: (body: {
    event_id: string;
    channel_id: string;
    step_key: string;
    // 'todo' un-ticks, which DELETES the row rather than storing a state.
    status: 'todo' | 'done' | 'ignored';
    url?: string;
    note?: string;
  }) => apiPost<PromoPayload>('/events/promo/mark', body),

  listEventChannels: () => apiGet<ChannelsPayload>('/event-channels'),
  saveEventChannel: (body: Partial<EventChannel>) =>
    body.id
      ? apiPut<ChannelsPayload>(`/event-channels/${encodeURIComponent(body.id)}`, body)
      : apiPost<ChannelsPayload>('/event-channels', body),
  deleteEventChannel: (id: string) =>
    apiDelete<ChannelsPayload>(`/event-channels/${encodeURIComponent(id)}`),

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

