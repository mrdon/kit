# Kit Events App — research + plan

> **Status: BUILT.** Phase 1 shipped in `internal/apps/events/` (migration 070).
> This document is kept for the research and the reasoning behind each decision,
> but several proposals in the body below were **withdrawn during planning or
> implementation**. Where the body and this list disagree, this list is what
> shipped:
>
> | Body proposes | As built | Why |
> |---|---|---|
> | Two calendars (public + ops), two mapping columns | **One calendar**, one set of `gcal_*` columns | The public subscribe surface was assumed to need its own Google calendar. It doesn't — Kit serves the feed. One calendar holds everything, private bookings included, since staff and the food partner both read it. `visibility` gates only the feed. |
> | `space_impact` includes `buyout`, plus a conflict check | **`none` \| `partial`** only | A full taproom buyout has never happened at this venue. Adding the value later is an additive CHECK change. |
> | A tokenless `feed.ics` alongside `feed.json` | **`feed.json` only** | Redundant: the Google Calendar already *is* the thing people subscribe to. |
> | A `ListEvents` client method to import existing events | **Not built** | Importing is a one-off, done with an external CLI after deploy rather than as Kit code. |
> | Two-calendar reconcile, per-projection ownership stamps | **Single-calendar reconcile** | Follows from one calendar. Routing is still isolated in `desiredCalendars`, which returns a slice, so a future split changes that function and the storage shape rather than every loop. |
> | A tenant-scoped SMTP sender for the partner digest | **Not needed, and the digest may not be either** | Scheduled jobs already run as their creator, and sharing the calendar with the food partner may replace the digest entirely. Deferred until they say which they'd actually use. |
>
> Everything else — the three-axis classification, default-deny visibility, one
> RRULE row per recurring event, the four correctness traps, and the wire
> contract carrying `type` from day one — shipped as described.
>
> Final decisions and the build sequence: `~/.claude/plans/bright-chasing-patterson.md`.

**Status:** Phase 1 built (see the correction table above)
**Audience:** Implementing agent / Don
**Location:** `internal/apps/events/`

## Problem

Gravity runs events (trivia, live music, releases). Today a Google Calendar records them
and everything else is manual and repetitive:

- post + schedule on Instagram
- post + schedule on Facebook
- update the website events page
- notify DD's (food partner) about what's coming up
- produce copy to send to downtown newsletters
- send a prep guide to the bartender on shift

Goal: enter an event **once** in Kit, and have Kit fan it out — automatically where it can,
and as tracked checklist state where it can't (social posting is explicitly out of scope as
an integration; we only want to know whether it's been done).

---

## Part 1 — What Kit already has

Everything below was verified by reading the code, not inferred.

### 1. Google Calendar **write** — already built and already connected

`internal/apps/googlecalendar/` is a complete, tenant-scoped Calendar API v3 writer:

| Piece | Location |
|---|---|
| Service-account auth, token minted + re-minted on 401 | `client.go:77` `ensureToken` |
| `InsertEvent` / `UpdateEvent` / `DeleteEvent` | `client.go:147,159,170` |
| `UpsertEvent` — insert, fall back to patch on 409 | `client.go:182` |
| `ListEventsByPrivateProperties` — find events we own | `client.go:194` |
| `OwnerProps(appName, tenantID)` — the ownership stamp every writer must set | `ownership.go` |
| Deterministic event IDs | `eventid.go` `DeterministicID()` |
| Per-tenant client load | `service.go:32` `LoadClient` |

This was built for the Square shifts sync (`docs/square-shifts-calendar-app.md`), which chose
a **service account** over user OAuth precisely so it "just runs" — no refresh token to
expire, no consent screen. The credential is already configured for Gravity.

**This is directly reusable. The hardest external integration is done.**

### 2. Square shifts sync — the structural template to copy

`internal/apps/squareshifts/` is the closest existing analogue to what we're building:
a background sync that owns a set of Google Calendar events idempotently.

- `app.go:90` — `CronJobs()`: a 15-minute sync plus a 12-hour reconcile pass that heals
  out-of-band deletions by consulting Google's actual state.
- `event.go:20` — `buildEvent()`: maps a domain object to a `googlecalendar.Event` with a
  `DeterministicID("square:" + shiftID)` and an `extendedProperties.private` audit stamp.
- `reconcile.go`, `audit.go`, `models.go`, `web_console.go` — the full shape.

Copy this file layout wholesale.

### 3. Outbound email — built, and gated behind a decision card

`internal/apps/email/`:

- SMTP via `github.com/wneessen/go-mail` (`smtp.go`).
- Markdown → goldmark → **bluemonday-sanitized** HTML with an inlined-CSS shell
  (`smtp.go:33` `renderHTML`), plus a plain-text alternative.
- Per-user account signature appended outside the LLM's control.
- Credentials live in the integrations substrate (`provider=email`, `auth_type=imap_smtp`),
  encrypted; the LLM never sees the password.
- **`send_email` is `PolicyGate`** — `agent.go:193` `handleSendEmail` refuses to run without
  an approval token, and the token doubles as the send-dedupe key.

**Three constraints that shape Phase 3:**

1. Credentials are **user-scoped** (`integrations.ScopeUser`, `email/app.go:117`) —
   `LoadAccount(ctx, pool, enc, tenantID, userID)`. There is **no tenant-level or service
   sender**, and no system/transactional email config at all (nothing in
   `internal/config/config.go`; no SendGrid/Postmark/SES/Mailgun/Gmail API anywhere).
2. `smtpSend` (`smtp.go:81`) is **deliberately unexported**, and `send_email` is
   **deliberately excluded from the MCP surface** (`email/app.go:96-100`) — it is the one
   documented exception to the tool-parity rule.
3. Sends are deduped on the approval token via `app_email_sent_messages`
   (`service.go:114` `sendOnce`, claim-before-send).

These constraints look like they force new plumbing. They don't — **Surface E below sends
as a user and needs none.** Read that before designing around them.

### 4. Public, unauthenticated HTTP routes — the widget proves the pattern

`internal/apps/widget/handler.go:33` registers top-level routes with **no auth middleware**:

```go
mux.Handle("GET /widget.js", h.staticAsset(...))
mux.HandleFunc("POST /widget/api/chat", h.handleChat)
mux.HandleFunc("OPTIONS /widget/api/chat", h.handleCORSPreflight)
```

with `Access-Control-Allow-Origin: *`, a rate limiter (`ratelimit.go`), and tenant derived
from a token rather than the URL. `app.go:112` `RegisterRoutes` mounts public and
Slack-OAuth-gated routes side by side in one place.

So: **an app can serve a public JSON/ICS feed today with no framework changes.**

### 5. iCal — the library is already vendored

`github.com/arran4/golang-ical v0.3.5` is in `go.mod`, used today only for *reading*
(`internal/apps/calendar/sync.go:16`). It writes `.ics` just as well.

Note the existing `calendar` app (`internal/apps/calendar/`) is **read-only** — it HTTP-GETs
public `.ics` feeds into `app_calendar_events` so the agent can answer questions about them.
It has no authoring or write path. It is not the place to build this.

### 6. The swipe card feed — apps plug in via an interface

`internal/apps/apps.go:147` `CardProvider`:

```go
type CardProvider interface {
	SourceApp() string
	StackItems(ctx, caller, cursor, limit) (shared.StackPage, error)
	GetItem(ctx, caller, kind, id) (*shared.DetailResponse, error)
	DoAction(ctx, caller, kind, id, actionID string, params json.RawMessage) (*shared.ActionResult, error)
}
```

Registered separately via `apps.RegisterCardProvider`. Three providers exist today (`cards`,
`task`, `expense`); `internal/apps/task/cardprovider.go` is the worked example. `DoAction`
returns an `ActionResult` telling the client to patch or remove the card — exactly right for
"mark this promo posted" swipe actions.

Per `docs/square-shifts-calendar-app.md`, **`RegisterCardProvider` is the opt-in visibility
mechanism**: an app is invisible to end users unless it calls it.

On the client, `web/app/src/kinds/index.tsx` maps `source_app:kind` → renderer and **falls
back to `{}`** — a new kind renders with generic title/body/markdown with no client code at
all. A custom `kinds/events_promo.tsx` is a polish step, not a prerequisite.

Also note the existing `calendar` app does **not** implement `CardProvider`, so calendar
events never reach the swipe feed today.

### 7. App cron jobs

`internal/apps/apps.go:138`:

```go
type CronJob struct {
	Name     string
	Interval time.Duration
	Run      func(ctx context.Context, pool *pgxpool.Pool, enc *crypto.Encryptor) error
}
```

Panic-recovered, error-logged (`apps.go:327`). But note the limits: **interval only — no
cron expressions, no persistence, no per-tenant rows, and no missed-tick catch-up** (a
restart resets the timer). It runs in every process. Good for "sync every 15 minutes",
wrong for "email the DDs at 9am Monday".

**For anything that must fire at a wall-clock time, use the other scheduler:**
`internal/scheduler/` is DB-backed over the `jobs` table — `cron`, `timezone`,
`next_run_at`, `status`, `config JSONB`, rows claimed under `SKIP LOCKED`, dispatched
through a pluggable registry via `scheduler.RegisterJobRunner(jobType, runner)`
(`runner.go:43`). `maxConcurrentTasks = 1`, deliberately serialised against the shared
Anthropic rate limit. Builder schedules ride this with `job_type='builder_script'`
(`builder/builder_runner.go:50` `WireJobRunners`); 5-field cron via `robfig/cron/v3`,
1-hour minimum interval.

`internal/apps/task/email_intake.go` shows the third option — a coarse 15-minute app-cron
sweep checking each row's own cron against `last_scanned_at` with a claim lease. Use it
only if you need per-row schedules the `jobs` table can't express.

### 8. Decision cards / gated tools

`internal/tools/registry.go:43` — `PolicyGate` means a call without an approval token
becomes a decision card instead of an action. `GateCardPreview` (`core.go:66`) supplies the
user-facing framing. Builder scripts get the same thing as `create_decision(title, body,
options, ...)`.

CLAUDE.md rule to respect: **a gated tool's handler must be the only entry point** to the
dangerous operation. No back doors into `email.sendOnce`.

### 9. Builder (Monty Python sandbox) — and why it can't do this alone

`internal/apps/builder/` lets an admin script apps in sandboxed Python. Per
`internal/apps/builder/admin_guide.md`, scripts get `db_*` on `app_items`,
`create_task` / `create_decision` / `create_briefing` / `create_job` / `send_slack_message`
/ `dm_user` / `find_user`, `llm_*`, schedules, and `tools_call` for cross-app hops.

But: **no `import`, no HTTP, no route serving.** So a builder app cannot write to Google
Calendar and cannot serve a public feed. It's viable for the checklist/reminder logic alone,
but not for the two integrations that carry the most value here.

### 10. Netlify app — the website is a git repo Kit can already edit

`docs/netlify-app.md` + `internal/apps/netlify/`: the site is a Netlify-connected git repo,
and Kit can drive Netlify Agent Runners against it from Slack. Relevant because "update the
website" can eventually mean "trigger a rebuild", not "hand-edit a page".

### 11. `messenger` — the channel-agnostic outbound primitive

`internal/services/messenger/messenger.go`. Its package doc states the intent directly:
*"Send posts an outbound message via the right channel adapter (Slack today; email/SMS
later)."* `SendRequest` already carries `Recipient{SlackUserID, Email, Phone}`, an `Origin`
app, `OriginRef`, and `AwaitReply`.

Only the Slack adapter is implemented, and only `coordination` uses it. Relevant below: if
you ever want DD replies routed back into Kit, this is where the digest belongs. For a
one-way blast it's more machinery than the job needs.

### 12. The "three apps" pattern to follow

From `docs/square-shifts-calendar-app.md`, and visible in the code: **plumbing** packages
(`square`, `googlecalendar`) implement neither `DescribableApp` nor `CardProvider`, so they
stay invisible and out of the feature-toggle list, exposing only `TypeSpec` +
`admin.RegisterIntegration` + `LoadClient`. The **feature** app (`squareshifts`) implements
`DescribableApp`, owns the cron, the mapping table, and the admin page.

The events app is a feature app. It should add no new plumbing package — it reuses
`googlecalendar` as-is.

### 13. Structural template for a CRUD app with a console UI

`internal/apps/expense/` — `web_console.go:29` shows a full JSON API surface
(`jsonRoute` / `adminRoute` helpers) plus policy config and email intake. Best model for the
admin side of the events app.

### Migrations

Goose, embedded, numbered — latest is `069_app_squareshifts_map.sql`. Ours is **070**.

---

## Part 2 — Recommendation

Build **`internal/apps/events/`** as a Go feature app. Go rather than builder because the
two highest-value pieces (Google Calendar write, public feed) are impossible in the sandbox.

Kit becomes the **system of record** for events; Google Calendar and the website become
**derived views**; social and newsletters become **tracked checklist state**.

### Relationship to the planned `announcements` app — resolved: events only

`~/dev/website/PLAN.md` Phase 3 specifies a **new Kit `announcements` app** with a `type`
discriminator over `release | event | festival | news`, a `draft → published → unpublished`
lifecycle, per-channel social variants, and the JSON endpoint Hugo consumes. Two of those
four types are events. **These two designs must not be built independently.**

They are not the same object, and merging them naively breaks:

| | `app_events` (this doc) | `announcements` (PLAN.md) |
|---|---|---|
| Purpose | operational record | publishing artifact |
| Covers | public *and private* events | public posts only |
| Extra state | space impact, staffing, food partner, promo checklist, ops calendar | body, images, social variants, JSON-LD |
| Most rows | never published anywhere | always published |

A private birthday party is not a draft post — it's not a post at all. Forcing it through an
announcements lifecycle means the publishing feed carries private fields it must never leak,
which is exactly the failure mode "visibility is a hard gate" exists to prevent.

**Strictly, neither contains the other — they're siblings.** A beer release ("Golden Mosaic
is back on tap") isn't an event, and a new pool table certainly isn't. What they actually
share is the **publishing pipeline**: JSON feed → Hugo content adapter → website + JSON-LD +
social. That pipeline is the real umbrella; events and announcements are two sources feeding
it. Events additionally carries an operational dimension that announcements never has.

**Decision (2026-07): build events only. Announcements are deferred indefinitely.**

Events is the live pain. Beer releases and general news are someday-nice-to-have. Note this
covers more than it sounds like — a *release party* is an event and is in scope; only the
non-event announcement ("we got a pool table") is deferred.

So:

- `app_events` owns the feed endpoint outright. No union, no second table, no
  `announcements` app.
- `venue='offsite'` projects as `type=festival`, `onsite` as `type=event` — PLAN.md's
  presentation-layer `type` derives from this doc's operational `venue`, so they never
  conflict.

**The one thing that must be right now is the wire contract, not the Go code.** Emit
`"type"` in the feed from day one even though only `event` and `festival` are ever produced.
Adding `release`/`news` later is then purely additive — a new source unioned into the same
endpoint, with no change to the contract Hugo depends on. Changing a published wire format
later is the expensive mistake; an extra enum value costs nothing.

Do **not** pre-build a provider abstraction for a second source that may never arrive. Kit
has the pattern available (`apps.CardProvider`) if it's ever needed; introducing it for one
implementation is premature. Refactor when the second source actually exists.

Two concrete corrections to this doc that follow from PLAN.md:

- **Media:** drop `image_url`; use an `internal/attachment` id plus the signed serve route
  (`GET /{slug}/apps/attachment/{id}?t=…`). PLAN.md flags an open issue worth inheriting — the
  existing token binds `(user, tenant, attachment)` with a **6h TTL**, which may not survive a
  *scheduled* rebuild. A build-scoped token is likely needed.
- **Price:** PLAN.md's event block has `price` as a string (`"Free"`). This doc's structured
  `price_cents` + `currency` is better for its own stated goal — JSON-LD `Offer` wants a
  numeric `price` + `priceCurrency`, and the whole site is explicitly machine-first. Recommend
  structured storage, rendering `"Free"` at the template layer.

### Data model — migration `070_app_events.sql`

Every table carries `tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE`.

**`app_events`** — the source of truth
`id, tenant_id, title, slug, summary, description (markdown), starts_at, ends_at, all_day,
timezone, location, hero_attachment_id (NULL), prep_notes (markdown),
status (draft|published|cancelled),
visibility (public|private), venue (onsite|offsite), space_impact (none|partial|buyout),
notify_food_partner (bool),
price_cents (NULL), currency, capacity (NULL), expected_attendance (NULL),
registration_url (NULL), square_variation_id (NULL),
rrule (NULL), created_by, created_at, updated_at`
`UNIQUE (tenant_id, slug)`. Index on `(tenant_id, status, visibility, starts_at)`.

See "Event classification" for why `visibility`/`venue`/`space_impact` are separate columns
and not one `type` enum, and "Paid vs free" for why price and capacity are **nullable
attributes rather than a fourth axis**.

**`app_event_promos`** — one row per (event, channel), the checklist
`id, tenant_id, event_id, channel (instagram|facebook|website|newsletter|dd_email|poster),
status (todo|scheduled|posted|skipped), scheduled_for, url, note, updated_by, updated_at`
`UNIQUE (tenant_id, event_id, channel)`

**`app_event_recipients`** — mailing lists (DD's, downtown newsletters, …)
`id, tenant_id, list_name, email, name, active, created_at`
`UNIQUE (tenant_id, list_name, email)`

**`app_event_digests`** — recurring outbound templates
`id, tenant_id, name, list_name, owner_user_id, subject_template, body_template (markdown),
cron, timezone, horizon_days, delivery (email|clipboard), auto_send (bool), active,
last_sent_at, last_claimed_at`

**`app_event_digest_sends`** — audit + idempotency
`id, tenant_id, digest_id, period_key, sent_at, recipient_count, message_id`
`UNIQUE (tenant_id, digest_id, period_key)` — the guard against double-sending a weekly.

**`app_event_settings`** — per-tenant config
`tenant_id (PK), public_calendar_id, ops_calendar_id, default_promo_channels (text[]),
feed_enabled, reminder_days (int[] e.g. {14,7,2}), public_url_template,
netlify_build_hook_url (encrypted)`

> **Superseded — as built there is ONE calendar id and one set of `gcal_*` columns.**
> See the as-built table at the top.

### Event classification — three independent axes, not one enum

The tempting model is a `type` enum: *public event / private party / buyout / offsite*. That
breaks immediately, because the real cases vary along **three independent axes** and an enum
would need the cross product:

| Axis | Values | Decides |
|---|---|---|
| `visibility` | `public` \| `private` | Does it reach the public feed, website, social promo? |
| `venue` | `onsite` \| `offsite` | Does it consume taproom space and on-premise staff? |
| `space_impact` | `none` \| `partial` \| `buyout` | What the bartender needs to know. Only meaningful onsite. |

The five cases map cleanly:

| Case | visibility | venue | space_impact |
|---|---|---|---|
| Trivia night, live music | public | onsite | none |
| Public event with a reserved area | public | onsite | partial |
| Private birthday party | private | onsite | partial |
| Full taproom buyout | private | onsite | **buyout** |
| Beer festival we're attending | public | **offsite** | none |

Note the offsite festival is `public` — you *do* want "come see us at Denver Beer Fest" on
the website. That's a real argument for one shared model: the public-feed filter is
`status='published' AND visibility='public'`, and it works across both venues without a
special case.

**Two rules fall out, and both matter:**

1. **`visibility` defaults to `private` and is a hard gate.** Leaking a customer's birthday
   party onto the brewery's public website is the worst failure this app can produce. This
   matches CLAUDE.md's existing default-deny scoping rule. Put the check in exactly one
   place — a single `func (e *Event) IsPubliclyVisible() bool` used by the feed and the
   promo materialiser and nothing else — and unit-test it directly. Don't hand-write the
   predicate at each call site.
2. **A `buyout` closes the taproom.** `create_event` should refuse — or at minimum warn
   loudly — when a public onsite event overlaps a buyout on the same day. That's a real
   operational conflict, and it's the kind of thing the current all-manual process catches
   only by someone remembering.

Promo rows materialise only for `visibility='public'`, so private parties never generate an
Instagram checklist. `notify_food_partner` defaults to `visibility='public' AND
venue='onsite'` but stays overridable — a private party may well want the truck, and a
festival never does. And the prep-guide job must skip `offsite` events when looking up who's
on shift; nobody is tending the taproom at a festival.

### Paid vs free — attributes, not a fourth axis

Pottery class (paid, limited seats) vs D&D night (free, drop-in) looks like another axis,
but it isn't. The three axes above each route the event somewhere different — a public feed,
a taproom, a bartender. Paid-ness routes nothing. It only *renders* differently.

So model it as **nullable attributes**, where NULL means "doesn't apply":

- `price_cents` + `currency` — NULL means free. Structured rather than prose, because the
  website feed shouldn't have to parse "$45 (includes materials)" out of a description.
  Caveats like "includes materials and your first beer" go in `description`.
- `capacity` — NULL means unlimited/drop-in.
- `registration_url` — NULL means just show up.

Deliberately **not** a `paid bool`: it would be derivable from `price_cents` and would rot
the first time the three came apart. And they do come apart — **free-but-limited is real**
(D&D with six seats at the table needs a capacity and an RSVP link but no price), and so is
paid-but-unlimited (a $5 cover with no cap). Three independent nullables express all eight
combinations; an enum would need eight names.

**The real question hiding in here: does Kit own registration? No — link out.**

Payment processing is an enormous scope increase (PCI, refunds, attendee comms, chargebacks)
and it contradicts Kit's stated shape as the ambient surface rather than the workbench. Kit
holds the event record and points `registration_url` at whoever sells the seat.

Conveniently, the Part 3 research already found the mechanism: a Square `CatalogItem` with
`track_inventory: true` lets **Square enforce the cap and block overselling at zero**. So
`capacity` in Kit is *informational* — it feeds the website, the promo copy and the prep
guide — while Square (or Eventbrite) is the enforcer. Kit must not try to be the source of
truth for seats sold; that's a sync problem with money attached.

`square_variation_id` is a nullable forward hook: with `INVENTORY_READ` Kit could later read
remaining stock and generate *"only 3 spots left"* copy. Read-only, no money, genuinely
useful. Phase 4, and only if wanted.

Two things that follow:

1. **Publishing a paid public event with no `registration_url` should warn.** You can't post
   "tickets on sale now" to Instagram before the Square item exists — a real sequencing trap
   the promo checklist would otherwise happily let you walk into.
2. **`expected_attendance` is the field the food partner actually cares about.** A 12-seat
   pottery class and a free D&D night that draws 40 are completely different signals for
   whether the truck should show. It's distinct from `capacity` (a hard cap vs. a guess), and
   it's optional — skip it if nobody fills it in, and let the DD digest just omit the number.

### Recurrence

**Decided: one row, one `rrule`, one website listing.** Trivia is the only recurring event
and is identical every week; live music is a cadence of *distinct* events (different band
each time) and is authored one row per night, not as a series. Store a **named timezone**
(`America/Denver`) rather than a fixed offset so DST is handled — per `website/PLAN.md`.

Three consequences, all small:

**1. `googlecalendar.Event` needs a `Recurrence` field.** The struct
(`googlecalendar/client.go:67`) has no recurrence support today — it was written for shifts,
which never recur. Google's Calendar API v3 takes `recurrence` as a string array of RRULE /
EXDATE lines, so this is a one-line addition to a thin JSON wrapper:

```go
Recurrence []string `json:"recurrence,omitempty"`   // e.g. ["RRULE:FREQ=WEEKLY;BYDAY=TU"]
```

Worth doing properly rather than writing 52 events: Google renders one recurring entry, and
`squareshifts` already makes the ops calendar dense enough.

**2. Anything that answers "what's happening on date X" must expand the RRULE server-side.**
That's the prep-guide job (is there an event tomorrow?), the DD digest (next 4 weeks), and
the buyout conflict check. One shared `expand(event, from, to) []time.Time` helper, used by
all three — not expansion logic copy-pasted per caller. The feed passes the RRULE through
unexpanded and lets Hugo expand for display.

**3. Promo tracking attaches to the series, not the occurrence.** One checklist for "Trivia,
every Tuesday" is right — you promote a standing weekly once, not 52 times. This falls out
of the one-row decision for free.

**Deliberately deferred: exceptions.** Trivia will eventually skip Christmas Eve. That needs
an `exdates DATE[]` column the expander honours — an additive column, not a schema redesign,
so there's no cost to leaving it out of v1. Just don't design the expander in a way that
can't accept one.

#### Revision (2026-08): monthly rules and explicit date lists

The v1 reading above — "trivia is the only recurring event, everything else is authored one
row per night" — held for trivia and was wrong about everything else. Authoring a five-week
beer school as five rows produces five slugs, five public pages, five poster uploads and
five copies of the staff notes that immediately drift apart. The v1 console said so out
loud: *"a run of different acts is not a repeat — create one event per night."* That
instruction was the bug.

What changed, both additive:

**1. `FREQ=MONTHLY` joins the allowlist.** `BYMONTHDAY` ("the 15th", `-1` for the last day)
and ordinal `BYDAY` ("`1FR`" first Friday, "`-1FR`" last Friday), with `INTERVAL` for
quarterly. The strict-allowlist discipline is unchanged and is the whole point — Google
understands all of RFC 5545 and our expander understands a subset, so a rule we can render
but not expand would draw a perfect series on the calendar while every Kit date query
silently saw nothing. Monthly expansion resolves each month's day set fresh, which is what
makes "the last Friday" move between the 4th and 5th week, and what makes a series on the
31st **skip** February rather than roll into March.

**2. `rdates TIMESTAMPTZ[]` holds explicit extra dates** for series no rule can express —
dates picked around a chef's availability, a run with a gap over a holiday. This is RFC
5545's RDATE, and it is emitted as one `RDATE;TZID=America/Denver:...` line alongside any
RRULE, so Google unions the two exactly as `Series.Expand` does.

`starts_at` remains DTSTART — the first occurrence — and the service normalises on write:
the combined set is sorted, deduped, and the earliest becomes `starts_at`. Holding that
invariant is what let the change stay additive. Every existing query that orders or bounds on
`starts_at` kept working untouched, and the expander treats `rdates` as purely additive.

One query did need care. The `listEvents` lower bound exempts recurring rows, because a
series' stored start may be years past. A rule-based series stays exempt outright (expanding
an RRULE in SQL is not on the table), but a date list is **finite**, so its bound is applied
to `max(rdates)` instead. A blanket exemption would have pinned every finished series to the
top of the upcoming list forever.

**Still deferred: per-occurrence exceptions and overrides.** `exdates` for the skipped
Christmas Eve, and a child override table for "the December market is the holiday market".
Both remain additive on top of this shape. The override table is the one to reach for when a
single date needs its own title, poster or capacity — that is a different thing from the date
list, not a bigger version of it.

### Cloning

**Decided (2026-08): a copy, never a link.** Most events at a venue are variations on one
that already happened, and the retyping is where the drift comes from — usually in the staff
brief nobody re-reads.

`Service.Clone` copies everything and then forces three things: status back to `draft`, a
fresh slug, and zeroed `gcal_*` state. Each has a specific failure behind it. A copy that
inherited `published` would hit the team calendar — and, if public, the website — the instant
it was created, before anyone had corrected the date. Two rows cannot share a slug, and the
original's may already be in a newsletter. Inherited sync state would have both rows fighting
over one deterministic calendar id.

The copy shares no state afterwards. A linked series is what the repeat rule and the date
list are for; clone is for "like that, but". Supplying a new `starts_at` also drops the
original's extra dates, because a clone aimed at a date is "that event, on this day" and
carrying a previous series' leftovers onto it is never the intent.

### Where does an event's canonical link point?

Every public event needs one shareable URL — for the Instagram bio, the Facebook post, the
newsletter, the DD email. Paid events have an Eventbrite/Square page, but free ones like
bike night have nothing to point at.

**Point everything at the website, and derive the URL rather than storing it.** Kit already
owns `slug` with `UNIQUE (tenant_id, slug)`. Put a `public_url_template` in
`app_event_settings` (e.g. `https://gravitybrewing.com/events/{slug}`) and compute
`canonical_url` from it. Storing a full URL per row means rewriting every row the day the
domain or path changes.

That gives two links with distinct jobs:

| Field | Nullable? | Job |
|---|---|---|
| `canonical_url` (derived) | never, for public events | Where to send people to read about it |
| `registration_url` | yes | Where to buy/RSVP — Eventbrite, Square |

The website page shows a "Get tickets" button pointing at `registration_url`. Bike night
simply has none. **The reason to layer it this way rather than sharing Eventbrite links
directly: if you switch ticketing providers, every link already posted to Instagram,
Facebook, and past newsletters keeps working.** Your own URL is the stable one; it links
onward.

### Keeping the website fresh

**The website repo (`~/dev/website`) already has a plan for this** — `PLAN.md` §Architecture
and Phase 3 specify a Hugo **content adapter** using `resources.GetRemote` to pull Kit's JSON
at build time, plus a Kit-fired Netlify build hook. Read that before designing anything here;
this section only records where the events app touches it.

Relevant facts from that plan:

- Site is **Hugo 0.164** on Netlify (`hugo --gc --minify`), theme `gravity`.
- It is currently **Phase 1 — five brochure pages** (`_index`, `about`, `food`, `our-beers`,
  `tasting-room`). There is **no events/announcements section yet**. Real website work gates
  any of this.
- Hugo **content adapters** (`_content.gotmpl`) generate real pages from remote data, so each
  event gets a genuine URL — which is what makes a link shared to Facebook or Instagram carry
  proper OG title/description/image instead of the generic site preview. That's the argument
  for per-event pages over anchors on one list page.
- Media should reuse Kit's **`internal/attachment`** (encrypted bytea + signed serve route),
  not a new image store. So `app_events` should carry an attachment id, **not** an
  `image_url`.

**⚠️ Correction: do not fire a build on every publish.** Netlify's free plan uses a credit
model — **300 credits/month, 15 per production deploy**, leaving roughly 11–14 deploys a
month after traffic. A rebuild per event edit would exhaust that in days. Even a *daily*
scheduled rebuild is 30 × 15 = **450 credits, over budget**.

So the batching instinct is correct, and the earlier advice in this doc to fire on every
change was wrong:

- **Free plan:** a **weekly** scheduled rebuild as the baseline (~4–5 deploys ≈ 60–75
  credits), plus a **debounced** on-demand trigger for time-sensitive posts — coalesce rapid
  edits into one deploy, and only fire when someone actually needs it live now. That leaves
  headroom for ~8–10 urgent publishes a month.
- **Netlify Personal ($9/mo, 1,000 credits):** ~66 deploys/month, so daily + on-publish fits
  comfortably. If events get frequent, this is the cheap fix.

Deploy previews, failed deploys, and rollbacks cost 0 credits.

Keep the hook URL **secret in Kit config** (an open URL lets anyone burn your credits) and
rate-limit it. Per `PLAN.md`, do *not* reuse the `netlify` OAuth app — a bare authenticated
POST is simpler.

Failure mode stays benign: if Kit is unreachable mid-build the build fails and **Netlify
keeps the last successful deploy live**, so the site goes stale rather than blank. Hugo's
adapter should also keep a last-good-JSON cache and **treat an empty feed as valid**, so a
fresh Kit with no events can't break the build.

**Committing generated files to the website repo** (the other option) is defensible — events
live in git, legible cold, and builds never depend on Kit being up. But it costs deploy
credentials in Kit, commit/push logic, and a bot committing to a repo humans also edit, *and*
it still spends a deploy credit per push. It doesn't dodge the cost problem.

**Client-side fetch** is the one to avoid: it couples the brewery's public storefront to
Kit's uptime, and `PLAN.md` states the goal plainly — *"Kit being down means 'can't publish
new posts,' never 'site is down.'"*

### One calendar or several? *(superseded — one calendar shipped)*

**You need at least two, and it isn't a preference.** The moment you have a publicly-shared
calendar *and* one private birthday party, the private event cannot live on it. That decides
the floor.

Recommended: **one event model, two calendar projections**, both written by this app:

- **"Gravity Events" (public)** — `visibility='public'`. Shared publicly, safe to embed on
  the website and hand out as an iCal subscribe link to regulars, the DDs, and downtown
  organisers.
- **"Gravity Ops" (internal)** — *everything*, including private parties, buyouts, and
  offsite festivals. This is what gives the outside sales team the full picture you want.

That satisfies the pro (sales sees everything on the ops calendar) without the constraint
violation. `googlecalendar.OwnerProps("events", tenantID)` makes writing both safe — each
calendar reconciles independently and neither touches the shift sync's events.

**The bartender-confusion problem is a titling problem, not a calendar-count problem.** More
calendars won't fix an ambiguous entry; a legible summary will. The shift sync already
learned this the hard way — commits `b2fc13a` (all-day, not timed blocks) and `0e7634c`
(first-name titles with shift hours) were both iterations on *legibility at a glance*. Apply
the same discipline, prefixing by classification:

```
🍺 Trivia Night · 7pm
🔒 Private — Sarah's 40th · 6–9pm (back room)
🚫 BUYOUT — Acme Corp · 5pm–close · TAPROOM CLOSED
🚚 Offsite — Denver Beer Fest (Don, Kate)
```

A bartender scanning the ops calendar can tell in one glance what their shift looks like.
Build the prefix in one `buildSummary()` helper, the way `squareshifts/event.go:20` does it,
so the format is changeable in one place after real use.

### Surface A — authoring (agent + MCP, in parity)

Per CLAUDE.md, every tool ships on **both** `internal/tools/`-style agent registration and
`internal/mcp/`, sharing `services.ToolMeta` and the same service methods, with formatting
helpers in `internal/services/` so both render identically.

- `create_event`, `update_event`, `publish_event`, `cancel_event`, `list_events`, `get_event`
- `set_event_promo(event, channel, status, scheduled_for?, url?, note?)`
- `add_event_recipient`, `list_event_recipients`, `remove_event_recipient`
- `create_event_digest`, `list_event_digests`, `preview_event_digest`, `send_event_digest` *(gated)*

`publish_event` is the state transition that (a) makes the event visible on the feed,
(b) queues the calendar write, (c) materialises promo rows from
`default_promo_channels`.

### Surface B — Google Calendar sync

A direct port of squareshifts:

- Cron `sync_events` every 15 min; cron `reconcile_events` every 12 h.
- `DeterministicID("event:" + event.ID)` → idempotent `UpsertEvent`.
- **Stamp ownership with `googlecalendar.OwnerProps("events", tenantID)`**
  (`googlecalendar/ownership.go`) merged into the event's private properties, and read back
  with `ListEventsByPrivateProperties` using the same map. This helper is newer than the
  squareshifts code that inspired it — it exists so two features writing the same calendar
  never claim each other's events. Add `kitEventId` alongside for the row mapping.
- **The reconcile sweep must treat ownership and staleness as separate questions.** Per that
  file: only an event that is *both* owned by this app *and* no longer backed by a live
  `app_events` row may be deleted. An unstamped event — a human's meeting, another Kit
  feature's write — is invisible to the sweep and must never be touched, even mid-window.
- `cancelled` or deleted → `DeleteEvent` (404/410 already treated as success).
- **Unlike shifts, these are timed events** — use `EventDateTime{DateTime, TimeZone}`, not
  the all-day `Date` form. (Shifts deliberately went all-day; events should not.)

Write **both** calendars from the same row: the public one gets only
`IsPubliclyVisible()` events, the ops one gets everything. Use distinct id seeds
(`DeterministicID("event:pub:" + id)` / `"event:ops:" + id`) so the two copies never
collide, and reconcile each independently. An event flipping `public → private` must be
**deleted from the public calendar**, not just left stale — that's the leak path, so cover
it with a test.

### Surface C — the feeds (two, with different auth)

Only `status='published' AND IsPubliclyVisible()` rows are ever serialised. But the two
consumers have opposite auth needs, so don't build one endpoint:

```
GET /{slug}/events/feed.json   → build-time feed, Bearer token required
(feed.ics was dropped — the Google Calendar is already the subscribe surface)
```

**`feed.json` should require a shared-secret bearer token** (a Netlify build env var), per
`website/PLAN.md` Phase 3. This reverses an earlier recommendation in this doc for a
tokenless feed — that argument assumed a *client-side* fetch, where any token is public
anyway. Now that consumption is server-side at build, a token is free and stops casual
scraping and enumeration.

**`feed.ics` must stay tokenless**, because its whole point is that a calendar app can
subscribe to the URL — the DDs, downtown organisers, and regulars adding Gravity to their own
calendar. Keep it minimal (title, time, location, canonical URL) and rate-limited; the rich
fields belong in the JSON.

Both get `Cache-Control: public, max-age=300`, ETag, and the widget's rate limiter;
`feed.ics` also gets `Access-Control-Allow-Origin: *`.

Note `PLAN.md` also has Hugo emit **standard RSS/Atom + JSON Feed outward** from the built
site. That's the Hugo→world hop and is unrelated to Kit's Kit→Hugo hop, which stays custom
JSON. Don't conflate them.

**JSON, not RSS/Atom — and ship iCal too.** RSS/Atom is a "what's new, chronologically"
format; it has no first-class start/end/location/ticket-URL, so you'd be smuggling event
data through `<description>` HTML and the website would have to parse it back out. JSON is
a plain contract for a website to consume. iCal is worth adding *in addition* because it's
free (library already vendored) and it lets the DDs, downtown organisers, and regulars
**subscribe** in Google/Apple Calendar — solving part of the notification problem without
any email at all.

No token on the feed: published events are public information by definition, and any token
embedded in the website's client-side JS is public anyway. Keeping it tokenless is simpler
and more honest. (If you want obscurity later, mirror `widget`'s token service.)

The website consumes `feed.json` **at build time**, triggered by a Netlify build hook Kit
POSTs on publish/update/cancel — see "Keeping the website fresh" above for why build-time
beats a client-side fetch here.

### Surface D — promo tracking as cards

Implement `apps.CardProvider`. Each `todo` promo row within the reminder window surfaces as
a swipe card — *"Trivia Night, Thu 14th — Instagram post not scheduled"* — with `DoAction`
options **Scheduled / Posted / Skip**, optionally capturing the post URL.

A `remind_events` cron walks `reminder_days` (default 14/7/2 before start) and emits a
briefing card or Slack DM for anything still `todo`.

Deliberately **not** duplicating these into Kit tasks: the value the user asked for is
"what's the promo state of this event, at a glance", which is a per-event status grid, not
scattered todos. One source of truth.

### Surface E — the DD digest (the automation with the most leverage)

**There is no plumbing gap. Send as a user.** An earlier draft of this doc proposed a
tenant-scoped SMTP sender plus a guarded export of the send path. Both were unnecessary —
the existing per-user path already covers this end to end:

- Scheduled jobs already **run as the job's creator**: `agent_runner.go:53` loads
  `job.CreatedBy` and the agent acts with that identity.
- The email app registers `send_email` for any caller with a mailbox row
  (`email/app.go:70` → `callerHasAccount`), so a job created by someone with a connected
  mailbox has the send tool.
- Hitting the gate mints a decision card and returns `HALTED`. On approval,
  `CardService.ResolveDecision` sets `jobs.resume_session_id` and `next_run_at = now()`
  (`models/job.go:639`); the scheduler resumes **the same session**
  (`agent_runner.go:66`). Dispatch goes through the one sanctioned bypass,
  `cmd/kit/gated_tools.go:85`.

So the approve→send→resume loop is already built and already correct. Put
**`owner_user_id` on the digest row**, load that user's account, and dispatch through the
same path. No tenant SMTP integration, no `email.SendGated`, no second SMTP implementation.

What you accept by doing this: the digest breaks if that person rotates their app password
or leaves — it fails loudly (`jobs.last_error` plus a DM to the creator,
`agent_runner.go:177`), and at Gravity's size that's a fine trade. The From address is a
person rather than `events@`, which for a food partner is arguably better anyway. Revisit
only if ownership needs to outlive an individual.

**Scheduling: register a JobRunner, don't hand-roll a sweep.** Add
`scheduler.RegisterJobRunner("events_digest", …)` and have `create_event_digest` write a
`jobs` row with `cron`, `timezone`, and `config = {"digest_id": …}`. That inherits
persistence, wall-clock firing, missed-tick handling, `SKIP LOCKED` claiming, and `Kick` —
none of which app-cron gives you. The `app_event_digests` table then holds only the
template and list, not a schedule.

When a digest fires:

1. Collect published events in `[now, now + horizon_days]` (4 weeks → `horizon_days: 28`).
2. Skip if a row already exists in `app_event_digest_sends` for this `period_key`.
3. Render `subject_template` / `body_template` with `text/template` (**never**
   `html/template` — CLAUDE.md) over `{.Events, .Start, .End, .Tenant}`. Templates live in
   the DB (user-editable), unlike the app's own prompts which go in `prompts/*.tmpl`.
4. Emit a **decision card**: *"Send the 4-week events email to DD's? (6 events)"* with the
   rendered body as the preview and options **Send / Edit / Skip**.
5. On approve, execute `send_email` **through `tools.Registry.Execute`** with the
   approval token from the card.

Step 5 matters: it honours the gated-tools rule instead of reaching into `email.sendOnce`,
and it gives you a weekly human check on an outbound partner email — which for a customer-
facing message you want. `auto_send` on the digest row is the escape hatch once you trust it.

The digest's `owner_user_id` determines which mailbox it sends from, since `LoadAccount` is
per-user.

**Downtown newsletter copy is the same machinery** with `delivery: clipboard` — same
templates, same event collection, but `preview_event_digest` just returns the rendered
markdown into Slack for you to paste. No new subsystem.

### Surface F — bartender prep guide

The nice payoff of building this inside Kit: **squareshifts already knows who's working.**

A `notify_event_staff` cron at T-1 day finds events starting tomorrow, looks up the Square
scheduled shift covering that window, resolves the team member to a Kit user, and DMs them
the event's `prep_notes` plus the essentials (start time, expected draw, what's needed).
Optionally LLM-expanded from a skill, but a plain template is a fine v1.

### Surface G — the editing experience

The "give it rough info, let the LLM draft, then iterate to fine-tune" loop is **already
built**, three times over. It needs no new chat machinery, and specifically **not** the
website widget.

**Don't use the widget.** `internal/apps/widget/` is for anonymous *site visitors*: its own
restricted agent (`agent.BuildWidgetSystemPrompt`, not the Slack prompt), token auth, CORS,
rate limiting, and a synthetic anonymous caller (`registry.go:121` returns a fixed member
Caller in widget mode). It has no logged-in identity and no admin tools. Wrong tool.

**Use the console chat launcher, which already exists.** A global launcher is mounted once
in the console `Shell` and spans every route (`web/console/src/chatContext.tsx`). A page
opts in with one hook:

```tsx
useSetChatContext(`the Events page, viewing "${event.title}"`, reload)
```

The description is sent as system suffix (`chat/prompts/system_page_context.tmpl`) so the
agent resolves "this", "here", "move it to 7pm" against what the user is looking at, and
`onTurnDone` refreshes the page after the agent mutates data. Ten console pages already do
this (Tasks, Expenses, Jobs, Skills, Vault, SquareShifts, …). Quick/console chat runs on
Sonnet and **keeps gated tools** — only decision-card chat drops them (`chat.go:179`).

**So form vs. LLM is a false choice, and you already build every app both ways.** The
console page is the form — deterministic, and a date picker genuinely beats typing "next
Thursday". The launcher on that same page is the LLM path. Both call the same service
methods, so does MCP from Claude Code. That's just the tool-parity rule doing its job:
build `update_event` once and all three surfaces get it.

**The iterate loop for an event is `update_event`, not a decision card.** Kit already has
`revise_decision_option` (`cards/app.go:226`) for exactly the fine-tuning pattern — an
option carries editable `tool_arguments`, the user says "make it punchier", and the tool
revises the draft *in place* instead of executing it. But that is scoped to *proposed
actions* that resolve once. An event is a persistent row edited over weeks, so a
`status='draft'` row plus `update_event` is the right model. Where
`revise_decision_option` genuinely fits here is the **DD digest email and promo copy** —
those are proposed actions, and they're already the right shape.

**Skip a clarifier gate on event creation.** `internal/apps/netlify/clarifier.go` is a
Haiku gate that asks one clarifying question with 2–3 concrete suggestions before acting.
It exists because a Netlify agent run is *expensive*. Creating a draft event is cheap, so
draft-then-edit beats ask-then-draft — let `create_event` guess from "live music Thursday
8pm" and let the user fix it in the form or the chat. Borrow the clarifier only if
`send_event_digest` ever goes unattended.

**Free second surface:** because card chat is wired to any `CardProvider`, draft events
surfaced as cards get long-press → "move it to 7pm" on mobile at no extra cost.

---

## Part 3 — Should Square host this instead?

Square has an appointments/booking product, and Gravity already has Square connected. Worth
checking before building an event model. **Verified against Square's live API inventory and
Gravity's actual account, read-only, 2026-07.**

### Verdict: no. Build the event model in Kit.

The `Booking` object models *one customer receiving one service from one named team member*.
Its full schema is `id, version, status, created_at, updated_at, start_at, location_id,
customer_id, customer_note, seller_note, appointment_segments[], transition_time_minutes,
all_day (readOnly), location_type, creator_details, source, address`.

Note what is absent: **no title, no description, no image, no ticket link, no capacity, no
attendee count.** `customer_id` is singular. `all_day` is read-only, so you cannot even
create an all-day block via API. Every `AppointmentSegment` *requires* a `team_member_id`.

A 40-person drop-in trivia night has nothing to attach to. You'd fake it as a fictitious
1-person appointment and split the event across two objects — date on the booking, name and
description on a Catalog item — with the ticket link stuffed into `seller_note`.

**Square Appointments does have real Classes with capacity and recurrence — but they are
Dashboard/POS-only.** Square staff confirmed on the developer forums that the API and the
`booking.created`/`booking.updated` webhooks cover individual bookings only. There is no
capacity, seat, attendee, or participant field anywhere in the Bookings *or* Catalog
schemas, and no `EVENT` catalog object type.

Three facts from Gravity's live account make it worse in practice:
`max_appointments_per_day_limit: 1` scoped `PER_LOCATION`; `min_booking_lead_time_seconds:
604800` (7 days); `booking_policy: REQUIRES_ACCEPTANCE`. A public events calendar written as
bookings would hit the one-per-day cap immediately, and anything scheduled inside a week
would be rejected.

**Don't request `APPOINTMENTS_*` scopes for this feature.** They'd add a paid
Appointments Plus/Premium dependency for seller-level writes, cost a Square token rotation
(Kit's token is pasted, tenant-scoped, currently `TIMECARDS_READ, EMPLOYEES_READ,
MERCHANT_PROFILE_READ`), and buy nothing for public events.

### But the research turned up two things worth acting on

**1. Gravity is already using Bookings correctly — for private party reservations.**
Their catalog has exactly one `APPOINTMENTS_SERVICE` item: **"Event Booking"** — *"Reserve a
portion of the tap room for a group event. Best for 10-40 people… Parties of 10 or more
receive 10% off when booked in advance. Must be booked 7 days in advance."* Variable
pricing, 3-hour duration, one assigned team member. That explains the account settings
exactly — the 7-day lead time and one-per-day cap are tuned for private buyouts.

This is a genuinely different thing from a public event, and Bookings is the right home for
it. **Opportunity:** `bookings.list` (read-only, `APPOINTMENTS_READ`) would let Kit fold
private-party reservations into the same view as public events — *"Saturday: private party,
6–9pm, plus Trivia at 7"*. That's real value for the bartender prep guide and for avoiding
double-booking the taproom. Worth a follow-up, **not** part of this app's Phase 1.

Telling detail: "10-40 people" is prose inside a description field, not a modeled number.
Even Gravity's legitimate Bookings use case has capacity living in free text. That's the
clearest illustration of the gap.

**2. Requirement 7 needs no new Square work.** The bartender prep guide should use
`labor.searchScheduledShifts`, which Kit already wraps as
`square.SearchPublishedShifts` (`square/client.go:229`) under scopes it already has.

### Reservations, tables, waitlist: no API at all

Checked separately, because Square-for-Restaurants reservations is a different product from
Bookings. These are **enumeration results from the live MCP service inventory, not failed
doc searches**:

- Square exposes **37 services**. There is no `reservations`, `tables`, `waitlist`,
  `tickets`, `floorplan`, or `seating`. Calling `reservations` and `tables` explicitly
  returns `Invalid service`.
- Table management, floor plans, coursing, seat management and waitlist all exist as
  **UI-only** features. Reservations are handled by an OpenTable integration that requires
  Square for Restaurants Plus/Premium *plus* a non-Basic OpenTable account, and OpenTable
  owns the data — nothing readable from Square.
- All 19 `CatalogObjectType` values and all 52 Catalog schemas contain no
  `EVENT`/`TICKET`/`RESERVATION`/`TABLE`/`SEAT` object.

⚠️ **`Events` in the Square API is a false friend.** The service exists, but its methods are
`search` / `enable` / `disable` / `listTypes` — it's the **seller audit log** of API
activity, disabled by default. Nothing to do with calendar events. Don't let the name
mislead a future implementer.

The negative results are trustworthy for a specific reason: the same inventory correctly
shows the Labor Shifts API *gone* (retired 2026-05-21, only `*ScheduledShift*`/`*Timecard*`
methods remain), which matches known reality. The inventory tracks retirement state
accurately.

### The one genuinely useful Square finding: inventory-enforced ticketing

`CatalogItemProductType.EVENT` **does** exist and is live — `catalog.searchItems` with
`product_types: ["EVENT"]` is accepted, while `["TICKET"]` returns HTTP 400
`INVALID_ENUM_VALUE`. But per Square staff it is **not creatable via the Catalog API**; it's
a Square Online UI construct with no queryable date, venue, or capacity field. Unusable as a
source of truth.

What *does* work, and is worth remembering for **limited-capacity beer releases**:

- A plain `CatalogItem` with `product_type: "REGULAR"`, date in the name
  (`"Hazy Release — Aug 14"`), details in `description_html`.
- One `CatalogItemVariation` per tier (GA / early bird) with `price_money` and
  **`track_inventory: true`**.
- **Capacity is enforced by the Inventory API** — set the count to the cap; Square
  decrements on sale and blocks overselling at zero. This is the same mechanism third-party
  event plugins use against Square.

The date is only a string, so Kit can never ask Square "what's coming up". That's fine:
**Kit holds the event record, and the Square catalog item is a sellable side-effect** that
`app_events.registration_url` points at, with `square_variation_id` linking them. Phase 4 at
the earliest. See "Paid vs free" for the fuller argument.

Also noted, in case the website ever moves: the `Sites` API exposes only id/title/domain,
and `Snippets` can inject JS into a Square Online site's `<head>`. There is **no** content
or item feed from Square Online. Gravity's site is a Netlify git repo, so this is moot today.

### Flags for whoever implements this

- Bookings API is **US/UK/CA/AU/ES/JP only**. Bookings *writes* at seller level need a paid
  Appointments Plus/Premium subscription; reads do not.
- Sites/Snippets are Early Access with **no Square Online sandbox** — production-only testing.
- One honest uncertainty from the research: a forum report suggests EVENT items may return
  undocumented `start_at`/`end_at` on read (the live API does return undocumented fields like
  `ecom_available` on other items). Unverified — Gravity has zero EVENT items and the
  research was read-only. Treat as unsupported: undocumented, unwritable, unqueryable by date.

Sources: [Bookings API overview](https://developer.squareup.com/docs/bookings-api/what-it-is)
· [Bookings reference](https://developer.squareup.com/reference/square/bookings-api)
· [Square staff on class-booking webhooks](https://developer.squareup.com/forums/t/webhook-events-for-bookings-of-type-class-plus/20222)
· [EVENT not upsertable](https://developer.squareup.com/forums/t/is-event-type-available-for-upserting-to-catalog/8517)
· [No waitlist API](https://developer.squareup.com/forums/t/waitlist-api-does-this-exist-somewhere/8439)
· [OpenTable + Square for Restaurants](https://squareup.com/help/us/en/article/7878-opentable-and-square-for-restaurants)

---

## Phasing

**Phase 0 — prototype the DD digest this week, with no code at all**

The digest is the most speculative part of this plan, and it can be tested before any of it
is built:

1. Publish Gravity's existing Google Calendar as an iCal URL (Settings → *Secret address in
   iCal format*).
2. `configure_calendar` — the read-only iCal app ingests it on a 15-minute sync.
3. Create a scheduled job, owned by whoever has a mailbox connected: *"Every Monday at 9am,
   call `get_calendar_events` for the next 28 days, draft an email to the DD list
   summarising what's coming up, and send it with `send_email`."*

That produces a real decision card every Monday with a real drafted email. If the rhythm and
the content are right, Phase 3 formalises it with deterministic templates; if they're not,
you've learned that for free. Constrain the job with `jobs.config` policy
(`allowed_tools`, `pinned_args` for the recipient list, `force_gate`) so an unattended
prompt can't wander — see `models/job_policy.go`.

**Phase 1 — event as source of truth** *(the bulk of the value)*

- Migration 070: `app_events`, `app_event_settings`. Models + service.
- Authoring tools on **both** surfaces (agent + MCP parity), `visibility` defaulting private.
- `IsPubliclyVisible()` as the single public gate, directly unit-tested.
- `expand(event, from, to)` RRULE helper — one implementation, three callers.
- Add `Recurrence []string` to `googlecalendar.Event` (one line).
- Google Calendar sync + reconcile cron, writing **two** calendars (public + ops) with
  `OwnerProps("events", tenantID)`; owned-**and**-stale is the only deletable state.
- Admin console page with `useSetChatContext` wired (Surface G).

⚠️ **Importing existing calendar events needs a new client method.** `googlecalendar.Client`
only writes and lists-by-private-property; there is no general `ListEvents`. Either add one
(`events.list` with `timeMin`/`timeMax`) or re-enter by hand from a cutover date. Open
question — ask before assuming.

**Phase 2 — website**
Requires website work first (site is at Phase 1, five brochure pages, no events section).
Hugo content adapter via `resources.GetRemote` · token-protected `feed.json` +
tokenless `feed.ics` · `type` in the wire contract from day one ·
`public_url_template` → derived `canonical_url` · per-event pages for OG tags ·
**batched** build-hook trigger (see the Netlify credit model — *not* one deploy per edit).

**Phase 3 — promo tracking**
`app_event_promos` · `CardProvider` (this is what makes the app visible to end users) ·
reminder cron on `reminder_days`.

**Phase 4 — outbound**
`app_event_recipients`, `app_event_digests`, `app_event_digest_sends` · a registered
`scheduler.JobRunner` (**not** app-cron — needs wall-clock firing) · decision-card-gated
send as `owner_user_id` · clipboard rendering for newsletter copy.

**Phase 5 — polish**
Bartender prep DM via `square.SearchPublishedShifts` · `exdates` for holiday skips ·
optionally fold Square private-party bookings into the calendar view (`bookings.list`,
`APPOINTMENTS_READ`) · optionally back a limited-capacity release with a Square catalog item
+ inventory cap.

Per CLAUDE.md, each phase also updates the user guide
(`internal/skills/builtins/user-guide/SKILL.md`) and the landing page.

---

## Open questions

0. ~~`events` vs `announcements`~~ — **resolved: events only**, announcements deferred
   indefinitely. See Part 2. Still open on the website side: `PLAN.md` Phase 2.5 is a
   GO/NO-GO gate on Kit-vs-Decap, and the site is only at Phase 1 (five brochure pages, no
   events section). Since events is now the *only* Kit content source, that gate is
   effectively "is Kit worth it for events alone?" — a narrower question than PLAN.md framed.
1. ~~**Recurring events**~~ — **resolved (2026-07): one row, one `rrule`, one website
   listing.** Trivia is Gravity's only recurring event and is identical week to week, so
   per-occurrence rows would buy nothing and would spam the events page with 52 near-duplicate
   entries. Live music is *not* recurring — each night is a distinct event with its own band,
   authored individually. See "Recurrence" below for the three consequences.
   Full RRULE support is a real scope increase (it affects the schema, the calendar sync, the
   feed, and the promo checklist — do you track promos per occurrence or per series?).
   Cheapest v1: a `create_event` `repeat_weekly_until` argument that bulk-creates N discrete
   events, each independently editable and cancellable. Flagging because it changes Phase 1.
2. **Calendars — resolved to two** ("Gravity Events" public, "Gravity Ops" internal), both
   shared with the existing service account, both separate from the staffing calendar. Open
   sub-question: does the outside sales team want the ops calendar, or would they rather have
   offsite festivals on a *third* calendar of their own? Don't split until someone asks.
3. **Is the full buyout real?** You flagged it as "rare, if ever". If it has genuinely never
   happened, `space_impact` can ship as `none|partial` and gain `buyout` later — but the
   conflict check ("no public event on a buyout day") is cheap enough to build now and is
   exactly the kind of thing that bites the first time it does happen.
4. **Existing events** — how many are on the current calendar, and do we import them or
   start fresh from a cutover date?
5. **Website** — is the events page static-rendered (needs a build hook) or can it fetch
   client-side? And is it the Netlify repo the netlify app already knows about?
6. **Whose mailbox sends the DD email** (it sends as that user — see Surface E), and is a
   weekly approval card acceptable, or do you want it fully unattended from day one?
   Related: **LLM-drafted or template-rendered?** The Phase 0 job route has the LLM write
   the copy fresh each week — zero code, but the wording drifts and you're proofreading
   prose rather than glancing at a list. A `text/template` digest is deterministic but only
   exists after Phase 1. Phase 0 is the cheap way to find out which you actually want.
7. **Instagram/Facebook confirmed as tracking-only** — no posting integration, correct?
