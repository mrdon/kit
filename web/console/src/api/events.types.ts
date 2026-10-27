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
  // What kind of thing this is, for grouping downstream — the website filters
  // its Give Back page on 'giveback'. Open-ended and multi-valued, unlike the
  // closed axes above, and nothing in Kit branches on them.
  labels?: string[];
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

// Where end-of-night trivia ratings are posted. channel_id "" means nowhere,
// which is where every workspace starts.
export interface TriviaFeedbackChannel {
  channel_id: string;
  channel_name: string;
  channels: EventsChannelOption[] | null;
  channels_error?: string;
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

// STANDARD_LABELS mirrors StandardLabels in internal/apps/events/labels.go,
// which is the source of truth. The vocabulary is open — anything may be typed
// — so these are presets, not a whitelist: they exist so the common cases
// converge on one spelling instead of fragmenting into synonyms the website's
// filters will never match.
//
// Duplicated rather than fetched because it is a handful of words that change
// about once a year, and an endpoint plus a load state is a lot of machinery
// for that. TestConsoleStandardLabelsMatchGo fails the build if the two drift.
export const STANDARD_LABELS: { label: string; means: string }[] = [
  { label: 'giveback', means: 'a charity night' },
  { label: 'food', means: 'a food offer or food-led night' },
  { label: 'trivia', means: 'a quiz night' },
  { label: 'music', means: 'live music' },
  { label: 'release', means: 'a new beer coming out' },
  { label: 'family', means: 'explicitly good for kids' },
  { label: 'community', means: 'hosted for somebody else' },
];

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
  // Replaces the whole label set. Omit to leave it alone; send [] to clear.
  labels?: string[];
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

// One of the day's events, as the entrance sign page offers it.
export interface SignChoice {
  id: string;
  title: string;
  time?: string;
  has_poster: boolean;
}
