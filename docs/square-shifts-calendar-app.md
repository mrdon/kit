# Square Shifts → Google Calendar app — research

Goal: a Kit app that pulls the published shift schedule from Square and mirrors it
into a Google Calendar that the team already subscribes to.

## Scope (decided)

- **Single team, not multi-tenant SaaS.** "Best solution just for my team; a background
  process that just runs." This removes the interactive per-customer OAuth requirement and
  lets us pick the most durable set-once-and-forget credentials for each side.
- **Write into an existing calendar** the team already subscribes to (not a Kit-created
  calendar). Kit is given write access to that one calendar and only touches events it owns.
- **Published scheduled shifts only** for v1 (no timecards).

### Resulting auth choices (the "just runs" versions)

- **Google → service account, no user OAuth.** Create a Google Cloud service account, then
  share the target calendar with the service account's email address as a **writer** (one
  click in Google Calendar's "Share with specific people"). The background process signs a
  JWT with the service account key and mints short-lived access tokens itself. **No refresh
  token to expire, no consent-screen publishing, no 7-day testing limit, no domain-wide
  delegation.** This is strictly better than user-OAuth for a set-and-forget single-team
  writer. The service-account JSON key is the only stored secret (encrypted).
  - JWT signing template already exists in-repo: `internal/apps/github/oauth.go`'s
    `signAppJWT` (RS256). Or pull in `golang.org/x/oauth2/google` (`JWTConfigFromJSON`) —
    small, standard, does the whole token dance; minor deviation from the no-SDK house style.
- **Square → OAuth code flow once, store the long-lived refresh token.** Square has no
  service-account equivalent, but the code-flow refresh token is valid until revoked and
  returns the same token on refresh, so a one-time authorize-your-own-app gets you a
  credential that then just runs. No public multi-tenant redirect UI needed — a single
  admin-only connect route (or even a paste-the-token setup) is enough.

## Headline findings

1. **This is greenfield.** There is no Square code anywhere in Kit, and the existing
   `calendar` app is **read-only iCal** — it HTTP-GETs public `.ics` feeds and mirrors
   them into `app_calendar_events`. It has **no Google OAuth, no API client, and no write
   path**. Nothing in it is reusable for writing events. It's only useful as a *structural*
   template (app.go/service.go/mcp.go split, cron-across-tenants with `apps.IsEnabled`
   gating, SSRF-safe HTTP).

2. **Two 3-legged OAuth flows must be built by hand.** Both Square and Google need
   interactive authorization-code consent with refresh tokens. Kit's integrations
   substrate (`internal/apps/integrations`) renders a credential *form* and stores
   `primary/secondary_token` (encrypted) or `config` JSONB — but it does **not** run an
   OAuth redirect/callback/refresh dance. The only existing template for that is the
   **GitHub app's bespoke flow** (`internal/apps/github/oauth.go` — `handleConnect`,
   `handleCallback`, state cookies), which lives *outside* the substrate. We'd be adding
   Kit's first real refresh-token OAuth2 providers.

3. **Use Square's Scheduled Shifts API — not the old Shifts API, not Timecards.**
   - The `Shift` object / `POST /v2/labor/shifts/search` was **retired 2026-05-21**; it now
     returns `410 GONE`. Do not build against it.
   - `Timecards` (`/v2/labor/timecards/search`) are retrospective clock-in/out payroll
     records — *not* the forward-looking schedule.
   - **`POST /v2/labor/scheduled_shifts/search` filtered to `PUBLISHED`** is the schedule
     the team consumes. This is our source.

4. **Idempotent calendar writes via deterministic event IDs.** Derive each Google event
   ID from the Square shift ID (base32hex: lowercase `a–v` + `0–9`, 5–1024 chars) so
   re-syncs never duplicate. Also stamp `extendedProperties.private` for audit/reconcile.

---

## Phase 2 (potential): LLM-assisted scheduling

Idea: instead of only *mirroring* Square's schedule, *generate* one — the one real gap
vs 7shifts. Findings from research (keep separate from Phase 1; much larger build):

### Hard blocker — Square withholds the key inputs
Verified against Square's live API method inventory (their MCP) + docs, current 2026:
- **Availability: no API.** Set in the Square Team app; zero endpoint to read it.
- **Time-off requests: no API.** Created/approved in-product; not exposed. Both are an open
  Square feature request as of Apr 2026.
- **Readable inputs that DO exist:** previous schedules (`scheduled_shifts/search`), jobs/
  roles (`ListJobs`), per-member job assignments + wages (`WageSetting.job_assignments`,
  `ListTeamMemberWages`), overtime/workweek config (`ListWorkweekConfigs`), break types.
- **Write path is fully open:** create drafts (`POST /v2/labor/scheduled-shifts`, one call
  per shift — no bulk create), sparse update, publish (`.../publish`, `bulk-publish` in
  ≤2-week slices, 1–100 shifts/call), scope `TIMECARDS_WRITE`. A human can review drafts in
  Square before publish.

**Implication:** a scheduler cannot read availability/time-off from Square — **Kit must own
them.** That's a fit for Kit's Slack-native strengths (collect "when can you work" / "need
Fri off" via Slack) and would make Kit the system-of-record Square structurally lacks — but
it's the bulk of the work, not a bolt-on.

### Correctness — the LLM must not be the scheduler
Pure-LLM generation is unreliable (NP-hard set-cover; LLMs double-book, violate
availability, understaff — they can't reason over time-coupled constraints globally). Proven
architecture:
- **LLM** = natural-language intake (parse messy availability/rules) + narration/explanation.
- **Deterministic solver** = the actual assignment (OR-Tools-style CP; for one small team the
  problem is small enough that a modest CP/greedy solver in Go suffices).
- **Validator** = re-checks every hard constraint (no double-book, availability, time-off,
  overtime via workweek config, per-role coverage, break rules) before anyone sees it.
- **Human approval** = required before publish — fits Kit's swipe-feed decision model
  (`create_decision` / card review). An LLM draft is a suggestion, never source of truth.

### Shape if pursued
Own feature app on the shared `square` client: Slack availability + time-off intake →
solver draft → LLM-narrated review as a Kit decision → write drafts back to Square (and/or
straight to Google Calendar). Unlike Phase 1, this has a real **end-user UI** (the review
feed). Note 7shifts' actual differentiator is *forecast-tied* auto-build (size shifts to
predicted sales); matching that would additionally need a sales forecast (Square Orders/
Payments data — same scopes we're already reserving).

## Square side

### Auth
- **OAuth authorization-code flow** (multi-tenant; a PAT only works for a single own
  account). Authorize at `https://connect.squareup.com/oauth2/authorize`, exchange +
  refresh at `POST https://connect.squareup.com/oauth2/token`.
- **Scopes:** `TIMECARDS_READ` (covers scheduled shifts *and* timecards),
  `EMPLOYEES_READ` (team member names + job titles), `MERCHANT_PROFILE_READ` (locations).
- **Tokens:** access token expires in 30 days; code-flow refresh token is long-lived and
  returns the same token. Square recommends proactively refreshing every ≤7 days.
- `Square-Version: 2026-05-20` header on every request. Base URL
  `https://connect.squareup.com` (sandbox `https://connect.squareupsandbox.com`).

### Data pull
1. `GET /v2/locations` → cache location IDs + IANA timezones (`MERCHANT_PROFILE_READ`).
2. `POST /v2/team-members/search` → cache `team_member_id → name`; `ListJobs`/`RetrieveJob`
   for `job_id → title` (`EMPLOYEES_READ`). Batch once, decorate in memory.
3. `POST /v2/labor/scheduled_shifts/search` with
   `filter.scheduled_shift_statuses=["PUBLISHED"]`, `location_ids`, and a rolling
   `start` window; page via body `cursor` (default limit 50, 5-min cursor TTL).
   - Read `published_shift_details` (present iff published): `team_member_id`,
     `location_id`, `job_id`, `start_at`, `end_at`, `notes`, `timezone`, `is_deleted`.
   - A deletion appears as `published_shift_details.is_deleted = true`.

### Incremental sync (optional, later)
Square emits `labor.scheduled_shift.published/updated/deleted` webhooks — but they are
still **Beta**. Recommended: poll on an interval now; add webhooks later as a latency
optimization, always backed by a periodic full reconciliation sweep.

---

## Google Calendar side

### Auth
- **Per-tenant OAuth 2.0 authorization-code with `access_type=offline`** — each customer
  connects their own Google account. (A service account with domain-wide delegation is the
  wrong model: admin-only, Workspace-only, huge blast radius.)
- **Scope:** request `https://www.googleapis.com/auth/calendar` (full). The narrower
  `calendar.events` cannot create a secondary calendar or manage ACLs, which we need.
- Refresh at `https://oauth2.googleapis.com/token` (`grant_type=refresh_token`). Store the
  refresh token encrypted.
- **Must publish the OAuth consent screen** (move out of "Testing") or refresh tokens die
  after 7 days. Calendar scopes are "sensitive" → plan for Google verification review.
  Handle `invalid_grant` on refresh by flagging the tenant "needs reconnect."

### Calendar setup (first connect)
- `POST /calendar/v3/calendars` → create a dedicated **"Shifts"** secondary calendar;
  persist its `id`. The connecting user becomes owner.
- `POST /calendar/v3/calendars/{id}/acl` with `role: reader` and a **group** or **domain**
  scope → the whole team sees one calendar without per-person shares.

### Event upsert (idempotent)
- Deterministic ID: `id = base32hex(hash("square:" + shiftId))`, lowercased, ≥5 chars.
- `POST /calendar/v3/calendars/{cal}/events`; on `409 Conflict` →
  `PATCH .../events/{id}`. On shift delete → `DELETE .../events/{id}` (ignore `410`).
- Body: `summary` (e.g. "Barista — Alice"), `description`, `start`/`end` with `dateTime`
  (RFC-3339 + offset) **and** `timeZone` (pass Square's location IANA zone straight
  through), plus:
  ```json
  "extendedProperties": { "private": {
    "squareShiftId": "SHIFT123", "source": "square", "kitTenantId": "<uuid>" } }
  ```
- Reconcile / find-orphans:
  `GET .../events?privateExtendedProperty=source%3Dsquare&privateExtendedProperty=kitTenantId%3D<uuid>`.
- Throttle < 600 req/min/user; retry `403 rateLimitExceeded`/`429`/`5xx` with
  `min((2^n)+jitter, 64s)` backoff. Do not retry `400/404/409/410`.

### ⚠️ Lifecycle risk to design around
Google is removing the secondary-calendar auto-reassignment safety net: personal accounts
**Apr 27 2026**, Workspace **Oct 5 2026**. If the employee who connected Google leaves and
their account is deleted, the **Shifts calendar and all events are destroyed**. Mitigations:
keep the shift→event mapping so we can recreate + re-sync; adopt Google's new
ownership-transfer endpoint (GA ~end of June 2026); or prompt customers to connect via a
durable/admin identity.

---

## Three apps: two plumbing integrations + one feature

Credentials are stored once via Kit's **integrations substrate** and reused. App visibility
is opt-in by interface (no whitelist): an app is invisible to end users unless it calls
`RegisterCardProvider`, and absent from the admin feature-toggle list unless it implements
`DescribableApp`. So the two credential packages implement *neither* — they're pure
infrastructure — while exposing admin config UI via `admin.RegisterIntegration` + a
`TypeSpec`.

- **`internal/apps/square/`** — plumbing. Registers the Square `TypeSpec` via
  `integrations.RegisterTypeSpec` and an `admin.Integration` via `admin.RegisterIntegration`
  (status pill + Manage link on `/{slug}/web/admin/integrations`, like `internal/apps/github/integration.go`).
  Owns token storage + auto-refresh, exposes `LoadClient(ctx, tenant)` (reads
  `models.GetIntegration` + `models.GetIntegrationTokens` + `enc.Decrypt`). The substrate
  *stores* the token but doesn't run the OAuth dance, so this package also adds the
  connect/refresh route (modeled on `internal/apps/github/oauth.go`). **No `DescribableApp`,
  no `CardProvider`** → invisible to end users, not in the feature-toggle list.
  - Request scopes up front for all planned consumers:
    `TIMECARDS_READ EMPLOYEES_READ MERCHANT_PROFILE_READ ORDERS_READ PAYMENTS_READ`.
- **`internal/apps/googlecalendar/`** — plumbing, symmetric with `square`. `TypeSpec`
  (service-account JSON key as secret, target calendar ID as config) +
  `admin.RegisterIntegration` + `LoadClient`. Also no `DescribableApp`/`CardProvider`.
- **`internal/apps/squareshifts/`** — the **feature**. Implements `DescribableApp` (a
  toggleable "Sync Square shifts to Google Calendar" in admin settings), owns the cron sync
  + mapping table, and consumes both `square.LoadClient` and `googlecalendar.LoadClient`.
- **Sales-stats briefing card (future consumer)** — a separate feature app that pulls
  `POST /v2/orders/search` + Payments via `square.LoadClient` and surfaces a daily figure
  through `CardProvider` (`internal/apps/apps.go:147-164`). Reuses the same stored Square
  credential — no second connect.

## Kit wiring

`internal/apps/squareshifts/` (mirror the calendar app's file split), depending on the
shared `internal/apps/square/` and `internal/apps/googlecalendar/` clients above:

- **`app.go`** — `apps.Register(...)` in `init()`. Implement `DescribableApp` so it's a
  toggleable feature app. Credential `TypeSpec`s live in the `square` and `googlecalendar`
  plumbing packages, not here.
- **Credentials** — both from the plumbing clients: `square.LoadClient` and
  `googlecalendar.LoadClient` (each reads the substrate via `models.GetIntegration` +
  `models.GetIntegrationTokens` + `enc.Decrypt`, like `internal/apps/email/service.go:LoadAccount`).
  (Not the vault — it's client-side E2E encrypted; the server can't decrypt it, so it's
  unusable from a job.)
- **Two HTTP clients** — new code, no SDKs (except optionally `golang.org/x/oauth2/google`
  for the service-account token). Model the thin typed wrapper + `APIError` on
  `internal/anthropic/client.go`; model Bearer-auth + base-URL-const + OAuth token exchange
  on `internal/apps/netlify/netlify_client.go`. Note: **no existing client does cursor
  pagination** (Square needs it) — a new pattern to introduce.
- **Sync trigger** — `apps.CronJob` (like calendar's `sync_calendars`, every 15 min): for
  the enabled tenant (gate via `apps.IsEnabled`), pull published Square shifts over a
  rolling window, upsert Google events into the target calendar, delete events for shifts
  that vanished. Use the `enc` param passed to `Run` for secret decryption.
- **State table** — `app_squareshifts_map` (tenant-scoped: `tenant_id UUID NOT NULL
  REFERENCES tenants(id) ON DELETE CASCADE`) mapping Square shift ID → Google event ID +
  last-synced version + `updated_at`, for reconciliation, delete detection, and recovery.
  (With deterministic event IDs the map is belt-and-suspenders, but it makes "which events
  do I own / which shifts disappeared" cheap without scanning the calendar.)
- **Run summaries → `audit_events`** (not a dedicated runs table). Append one row per run
  via a typed constructor in `internal/apps/squareshifts/audit.go` (follow the
  `internal/apps/vault/audit.go` pattern — codebase rule: typed constructors, never
  free-form text). Actions:
  - `squareshifts.sync_completed`, metadata `{created, updated, deleted, duration_ms, triggered_by}`
  - `squareshifts.sync_failed`, metadata `{error, triggered_by}`

  Rationale: a dedicated `app_squareshifts_runs` table would only add typed query columns
  and a mutable in-progress row — neither needed at this volume, and `audit_events` is
  append-only so the run just emits a single terminal event. Upside: the future audit UI is
  one generic viewer across all apps, not a per-app runs page.

### Admin UI (the only UI — no end-user surface)
The `squareshifts` feature's `admin.Integration` Manage page shows:
- **Status detail** — "Last sync 3m ago · 42 events" from the most recent
  `squareshifts.sync_*` `audit_events` row.
- **"Sync now"** button — invokes the same sync `Run` on demand; emits an event with
  `triggered_by='manual'`.
- **Recent runs** — deferred to a future generic `audit_events` viewer (serves all apps).
- **Docs** — update the user guide (`internal/skills/builtins/user-guide/SKILL.md`) and
  landing page when shipping.

### Remaining minor decisions (safe defaults chosen)
1. **Per-shift event granularity:** one **all-day** event per published shift, on the
   shift's start date (teams consume the schedule as a "who's on" banner, not timed blocks).
   Switching to timed blocks is a one-line change in `buildEvent`. Breaks/notes go in the
   event description; ignore break sub-structure for v1.
2. **Event title:** the team member's name (job-title prefix like "Barista — Alice" is a
   later enhancement needing the job cache).
3. **Poll-only vs webhooks:** poll every 15 min for v1; Square scheduled-shift webhooks are
   Beta — add later as a latency optimization behind a reconciliation sweep.
4. **Multi-location:** all locations into the one target calendar for v1; per-location
   calendars later if the team wants separation.
5. **Sync window:** rolling e.g. now → +21 days; re-sync overwrites, past events age out.

### Build order / status
1. ✅ Square client + token store + `scheduled_shifts/search` behind read-only
   `square_list_shifts` tool (`internal/apps/square/`).
2. ✅ Google service-account client + `gcal_check` probe (write+delete) tool
   (`internal/apps/googlecalendar/`).
3. ✅ Idempotent event upsert (deterministic base32hex IDs) + `app_squareshifts_map`
   (migration 069) + windowed delete detection (`internal/apps/squareshifts/`).
4. ✅ `apps.CronJob` (15 min) sync loop + `apps.IsEnabled` gating + rolling 21-day window.
5. ✅ Reconciliation sweep — second `apps.CronJob` (`reconcile_square_shifts`, every 12h)
   lists actual Kit-authored events via `ListEventsByPrivateProperty(kitTenantId)`, recreates
   desired events missing from the calendar (heals out-of-band deletions), and deletes
   in-window orphans. Records to `audit_events` with `triggered_by="reconcile"`.
6. ✅ Config/status tools (agent + MCP parity): `squareshifts_sync_now`,
   `squareshifts_status`; run summaries → `audit_events`; docs (user guide + landing).

Admin UI (built):
- **Connecting Square + Google Calendar is done on the console Integrations page**, not a
  chat tool. Integration config is web-only: the console shows a catalog of registered
  connectors with **Connect / Reconnect / Disconnect** buttons that mint the same signed
  setup URL (`integrations.MintSetupURL` + console `/api/integration-catalog*` endpoints).
  The old `configure_integration`/`check_integration_status`/`list_integrations`/
  `delete_integration`/`list_integration_types` agent+MCP tools were removed — secrets never
  route through the LLM and the agent context stays lean. Email stays self-service (user
  scope); Square/Google are admin-only (tenant scope), enforced by `MintSetupURL`.
- A **Manage page** in the React console (`/{slug}/web/admin/square-shifts`,
  `web/console/src/pages/SquareShifts.tsx`) with connection status, a **Sync now** button,
  and a **recent-runs table** — backed by JSON handlers via `console.AdminJSON`, plus
  agent/MCP `squareshifts_sync_now` / `squareshifts_status` tools. When a dependency isn't
  connected it links to the Integrations page.

Scheduling uses `apps.CronJob` (the process-wide interval ticker, same as the calendar
app) — **not** the DB-backed `internal/scheduler`/`jobs` system, which is for per-tenant
LLM-agent-driven jobs. Two jobs: `sync_square_shifts` (15 min) and
`reconcile_square_shifts` (12 h). Both skip tenants that aren't enabled/connected.

API footprint is light: the 15-min sync is ~3 Square reads + near-zero Google writes at
steady state (unchanged shifts are hash-skipped); reconcile adds ~1 Google list call per
12 h. Well within Square's limits and Google's 600/min · 1M/day.

Nothing left deferred from the original plan.
