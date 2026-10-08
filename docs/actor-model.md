# Actor model — from "every caller is a Slack user" to actors with scoped permissions

Status: **implemented**, phases 1–6, on 2026-10-08 (commits "Auth: one signed-link
helper…" through "Scheduler: a job's session is attributed to the job…"). Written the
same day as a hand-off; revised after a review against the code (findings folded in
below; the auth inventory is in the appendix). Where the build departed from the plan:

- **Pending pairings live in a table** (`app_device_pairings`), not Redis: one code
  path, nothing to run locally. Rows are swept on each pairing-page load.
- **"Pair a device" is console-only** (`/{slug}/web/admin/devices`); the PWA has no
  approver UI.
- **CSRF:** `X-Kit-Web` is the one header, but an `application/json` body also counts
  (it forces a preflight just the same) so the cards PWA's existing JSON posts, cached
  by its service worker, kept working. `X-Kit-Chat` and `X-Kit-Vault` are accepted for
  one release; delete `legacyCSRFHeaders` after that.
- **Signed-link purposes were bumped** (cookie v3, state/deeplink/integrations v2), so
  every browser session signed out once. MCP bearer tokens were untouched.
- **The trivia driver keeps the "Manage sets" link** to `/admin/trivia`: decision 2
  gives it dataset management, and that page is where it lives. For a device, `/admin`
  itself renders the device home.
- **Phase 6 attribution** is `sessions.actor_kind` / `sessions.actor_label`, shown by
  `list_sessions`. Tasks and messages carry nothing new.
- Not built: the optional "your sessions and MCP tokens" revoke page.

## Why

Every *web* surface (console, swipe PWA, MCP) resolves an `api_tokens` row to a real
`users` row and builds `*services.Caller` (`internal/services/services.go:16`). The Slack
agent and the scheduler skip `api_tokens` but still build a Caller for a Slack user.
Everything that is *not* a person gets squeezed into that shape:

- **Scheduled jobs run as their creator** (`internal/scheduler/agent_runner.go:53`). The
  owner's roles are resolved live, and `models.Policy` (`internal/models/job_policy.go`:
  `AllowedTools`, `ForceGate`, `PinnedArgs`, enforced at `internal/tools/registry.go:540`)
  already narrows what a job may do. What's missing is *attribution*: the record says
  "Don did it", not "Don's morning-briefing job did it".
- **MCP harnesses get the user's full authority** (90-day OAuth tokens, `internal/auth/oauth.go`).
- **The website widget is a hand-built anonymous Caller** (`internal/tools/registry.go:123`,
  `internal/agent/context.go:112`).
- **Wall displays are unauthenticated URLs.** That's fine for read-only screens, but no
  use for anything that changes state.

The trigger is that the taproom needs staff UIs on shared devices, where nobody ever types
a password and nothing acts as a particular Slack user:

1. **Trivia driver**: a separate laptop that only runs trivia, with very lightweight auth.
2. **Taproom admin**: an old iPad (also the Pandora UI and a Square POS), and a laptop when
   handy. It runs happy hour, prints the menu, toggles gluten-reduced beers and repoints
   wall screens.

Direction of travel for the product: agents do most things, people occasionally do
things, and special-purpose apps do a few specific things. Authorization should be about
**what this actor may do**, with "which human is accountable" as a separate, optional
fact.

## End goal

Every request and agent turn runs as an actor. `services.Caller` gains:

| Field | Meaning |
|---|---|
| `Kind` | `user`, `device`, `agent`, `widget` |
| `ActorID` | the `actors` row for non-users; empty for users |
| `UserID` | **the accountable human, if any** (user → self; agent → job owner; device/widget → nil) |
| `Label` | for audit ("Bar iPad", "Morning briefing job") |
| `Capabilities` | what a non-user actor may do (users: derived from roles) |

`TenantID`, `Roles`, `RoleIDs`, `IsAdmin` and `Timezone` keep their current meaning.

Permissions per use case:

| Use case | Kind | Can do | Accountable human |
|---|---|---|---|
| Person in Slack / PWA / console | `user` | everything their roles allow today (unchanged) | themselves |
| MCP harness | `user` now | user's full set; later a subset picked at OAuth consent | the user |
| Scheduled job | `agent` | `models.Policy` ∩ owner's roles (already enforced; unchanged) | job owner |
| Trivia driver laptop | `device` | `trivia.host` only | none (pairing admin recorded as sponsor) |
| Taproom admin iPad/laptop | `device` | capabilities ticked at pairing, from: `menu.happy_hour`, `menu.print`, `menu.gluten_reduced`, `kiosk.repoint`, optionally `trivia.host` | none (sponsor recorded) |
| Website widget | `widget` | read-only knowledge Q&A (today's `Hide*` flags) | none |
| Wall screens, trivia player phones | — | **out of scope**: public read-only URLs and per-game team cookies stay as they are | — |

Rules across all of it:

- **Central default deny for non-user callers.** Today `requireCallerHandler`
  (`internal/apps/console/middleware.go:63`) and the cards/stack/vault wrappers
  (`internal/apps/cards/stack_web.go:46-57`, `internal/apps/vault/urls.go`) only check
  that *a* caller exists. A device caller reaching them would get tasks, jobs, chat and the
  vault. So non-user callers are rejected centrally (session middleware, `resolveToken`,
  `BearerMiddleware`, `MCPAuthGate`), and **only routes that explicitly opt in** via
  `RequireCap` accept them. MCP and bearer auth reject non-users outright.
- **An agent never exceeds its owner.** This already holds through `models.Policy` plus the
  owner's live roles.
- **Gated tools:** approval cards go to the accountable human. Devices and the widget can't
  reach the tool registry at all in this plan.
- **Addressing is human-only.** DMs and task assignment need a person, so code that needs
  `Identity` (a Slack user ID) must fail clearly for non-users.
- **Attribution records the actor**, plus the accountable human when there is one.
- **Tenant isolation is unchanged.** CLAUDE.md's `tenant_id` rules apply to every new table
  and query.

## Capabilities

- **Constants in one file** (`internal/auth/capabilities.go` or similar), not an app
  registry: there are about five. Each one declares its **human default**: the minimum
  role a person needs to pass the same check.
  - `menu.happy_hour`: admin (today `AdminJSON`, `internal/apps/menu/happy_hour_web.go:29-34`)
  - `menu.gluten_reduced`: admin (`gluten_reduced_web.go:26-29`)
  - `menu.print`: member for the PDF (`print_web.go:30`); print *config* stays admin
    (`web_console_print.go:32-36`)
  - `kiosk.repoint`: member (`internal/apps/kiosk/web_console.go:26-31`)
  - `trivia.host`: member (`internal/apps/trivia/web_console.go:27-49`)
- **Routes only.** Neither device touches the agent or MCP tool registry, so no
  `ToolMeta.Capability` and no agent/MCP parity work in this effort.
- One wrapper, `console.RequireCap(cap, h)`: a user passes when their role meets the
  capability's human default, and a device passes when it holds the capability. It
  replaces `AdminJSON` / `JSON` / `PageRoute` on exactly the routes above, so behaviour
  for people is identical. Where one capability spans routes with different human checks
  (trivia: member for hosting, admin for the feedback-channel routes), allow a per-route
  override of the human default (e.g. `RequireCapAdmin`), not a second capability.

## Phases

Every phase lands as shippable commits on `main`; run `make prepush` before each one. No
phase changes how things behave for people.

### Phase 1 — credential helpers (prep, pure refactor)

The same primitives are copied several times (see the appendix). Consolidate before adding
another user of them:

- **Signed-link helper** with domain separation (`signedlink.New(secret, "purpose-v1")`,
  `Sign(payload, ttl)`, `Verify`). It replaces the four hand-rolled sha256(prefix+secret)
  + HMAC copies: `internal/auth/session.go:58`, `deeplink.go:86`, `state.go:28`,
  `internal/apps/integrations/tokens.go:34`. The last two are the same shape.
- **Opaque-token helper** (generate + hash). It replaces `models.GenerateToken`/`HashToken`,
  `models.HashWidgetToken`, and `trivia.NewTeamToken`/`HashToken`. The trivia team cookies
  stay a separate *mechanism* (participants, not actors) but use the helper.
- **One CSRF header** instead of `X-Kit-Web` / `X-Kit-Chat` / `X-Kit-Vault`, plus an
  `Origin` check.
- Fix the misleading `session.go` comments: the cookie holds the raw token, not the token
  id, and there's no reaper on `expires_at`. Add the reaper as a scheduled function-lane
  task.

### Phase 2 — actors and device sessions on `api_tokens`

There's no separate credential table: devices reuse the existing session cookie and token
path.

- `actors(id, tenant_id, kind, label, capabilities TEXT[], sponsor_user_id, created_at,
  last_seen_at, revoked_at)`. No `bound_role_id`, no `actor_capabilities` table. Users are
  **not** migrated into `actors`; existing `user_id` foreign keys keep meaning "a person".
- `api_tokens` gains `kind` (`session` | `mcp` | `device`), `label`, `actor_id`
  (nullable), `last_used_at` (throttled writes) and `revoked_at`. `user_id` becomes
  nullable, with `CHECK (num_nonnulls(user_id, actor_id) = 1)`.
- A device is issued an ordinary `kit_session` cookie (`IssueWithTTL`, about 1 year,
  sliding renewal), at Path `/{slug}/` so it reaches `/{slug}/api/trivia/...`, the host SSE
  stream and `/{slug}/console`. There's one cookie and one resolver, so there's no
  question of which cookie wins.
- `resolveToken` (`internal/auth/middleware.go:193`) builds a device Caller when `actor_id`
  is set and the actor isn't revoked.
- Caller constructors live in one place (`NewUserCaller`, `NewWidgetCaller`,
  `NewDeviceCaller`, `NewAgentCaller`). Today's construction sites outside tests:
  - `internal/tools/registry.go:123,133`
  - `internal/agent/context.go:71,112`
  - `internal/auth/middleware.go:221`
  - `cmd/kit/gated_tools.go:39` (a dummy admin caller)

  Seven test files build `services.Caller{}` literals; leave those alone unless a
  constructor is clearly better.
- **Central default deny** as described under "Rules" above, with tests proving a device
  session gets 401/403 from member routes (tasks, jobs, chat, vault, stack) and from MCP.
- `GET /api/me` (`internal/apps/console/web.go:52-87`) returns `kind`, `label` and
  `capabilities`.
- **Long-lived streams:** the trivia host SSE stream (`trivia/web_console.go:37`)
  rechecks its token periodically (or caps how long a connection lives) so revoking a
  device ends the stream.
- Side benefit: since `api_tokens` now has `kind` and `label`, a "your sessions and MCP
  tokens" revoke page is cheap. That's optional; do it if it falls out naturally.

### Phase 3 — capabilities on routes

- Add the constants file and `RequireCap` wrapper from the Capabilities section, and swap
  them onto the menu, kiosk and trivia routes listed there.
- Tests: an admin and a member get exactly today's responses on every swapped route; a
  device with or without the capability gets 200 or 403.

### Phase 4 — device pairing + trivia driver (first device; it matters on quiz night)

**Hard requirement from the user: nobody signs in on the device.** A random laptop opens
a URL, then a simple step pairs it: a picture match or a code, approved by someone who is
already signed in on their own phone. No Slack login, no password and no admin session on
the device, ever. One pairing flow serves both device kinds; the trivia driver is the
first user of it.

**Pairing flow** (device-flow style, with number matching):

1. Any browser opens a short URL, `/{slug}/pair`, with nobody signed in. Kit creates a
   pending pairing and sets a secret `device_code` in an HttpOnly cookie on that
   browser. The page shows a **picture** (from a fixed set of about 50 distinct
   emoji/icons) and a short **user code**, then polls.
2. An approver, signed in to Kit on their phone, opens "Pair a device". It's reachable
   from the console and the PWA. There are no notifications (decided): the approver is
   standing at the device and opens it themselves. The phone lists pending pairings, each offering **three
   pictures**, and the approver taps the one shown on the device. Typing the user code
   works too. A wrong pick cancels that pairing.
3. The approver chooses a **preset**:
   - **Trivia driver** gets exactly `trivia.host`.
   - **Taproom admin** comes with its capabilities pre-ticked and editable.

   They give it a label ("Trivia laptop"). Approving needs `admin` (decided).
4. The device's poll, which must carry its `device_code` cookie, receives a device
   `kit_session` (phase 2), and the page redirects to the preset's home screen. Reading
   the picture or code off the screen lets you *approve* the pairing but not *take* the
   credential.
5. Limits: pending pairings expire after about 10 minutes, are rate-limited per IP, and
   are capped per tenant. Store them in Redis with GETDEL, the same pattern as poster
   uploads (`internal/apps/events/poster_upload.go:79-120`), or in a small table when
   `REDIS_URL` isn't set.
6. iOS: a home-screen web app has its own cookie jar, separate from Safari's, so pair from
   whichever one staff will actually use. Server-set HttpOnly cookies aren't subject to
   Safari's 7-day cap on JS-set cookies.

**Console "Devices" page**: list with label, preset and last seen; edit capabilities;
revoke. Put it beside kiosk boards, but keep the tables separate: kiosk keys are
deliberately unauthenticated URL halves.

**Trivia driver specifics:**

- **Route scope (decided): everything.** A device holding `trivia.host` gets the whole
  trivia console. That includes the destructive dataset work (delete all games, import,
  delete datasets/questions, load packs, `trivia/web_console.go:32, :42-49`) and the
  feedback-channel routes. For people, keep each route's current human check (member,
  or admin for the feedback-channel routes) by passing it to the wrapper per route (see
  Capabilities).
- `callerID` (`trivia/web_console.go:490-496`) returns `nil` for non-users, not
  `&uuid.Nil`. `created_by` is already nullable (`migrations/088_app_trivia.sql:131`).
  Split `web_console.go` while you're in it; it's 531 lines, over the 500-line limit.
- **React shell** (`web/console/src/`): switch on `/api/me` `kind`. For devices, render
  only the trivia host pages: no `ConsoleChat` (`Shell.tsx:32`), no nav to other apps, and
  no logout (`TopBar.tsx:51`; logout calls `signer.Revoke`, which would unpair the
  device). Hide the `/admin/trivia` link on the setup page.

### Phase 5 — taproom admin (iPad + laptop)

- **Pairing:** the phase 4 flow with the "Taproom admin" preset. Nothing new to build.
- **UI (decided): React, inside the existing console app** (`web/console/src/`). The iPad
  runs iOS 16+, so no separate server-rendered frontend. Add a "taproom" device shell,
  chosen from `/api/me` `kind` and `capabilities`, with big touch targets and no chat,
  nav or logout. Each tile appears only when the device holds its capability:
  - happy hour start/end (reuse the "Start now / End now" service paths and existing
    components where they fit)
  - print the menu (`/menu/print.pdf` → AirPrint)
  - gluten-reduced toggles
  - wall-screen repoint
  - trivia hosting (the same pages as the trivia driver), if the device holds
    `trivia.host`

  The trivia driver from phase 4 is the same shell showing a single tile, so build the
  shell once.
- Server side: only the `RequireCap` route swaps from phase 3. Add a Go package only if
  some endpoint doesn't already exist. CSRF comes free, because the console's fetch
  wrapper already sends the header.
- **Repoint (decided): any URL**, same as people today. No history restriction.
- Update the user guide (`internal/skills/builtins/user-guide/SKILL.md`), and add a line to
  the landing page if it's worth one.

### Phase 6 — jobs attributed as agents (small)

- `models.Policy` already provides the allowlist and the "never exceed the owner" rule.
  **Don't add a second allowlist.**
- The runner builds `NewAgentCaller(job, owner)`: `Kind=agent`, `Label` = the job's name,
  `UserID` = the owner. Everything that reads `UserID` keeps working.
- Attribution (session events, created tasks, messages) records the agent label alongside
  the owner.

### Later, optional

- MCP OAuth consent picks a capability subset. `api_tokens` with `kind=mcp` already holds
  the grant.
- Widget tokens fold into `api_tokens` as `kind=widget`, with a `widget` actor per token.
- `actor_id` attribution columns where "who did it" matters, and an audit view.
- Fine-grained mapping from roles to capabilities for people, if a real need appears.

## Separate security follow-ups (found during review; not part of this effort)

- The Slack install OAuth has no `state` parameter (`internal/slack/oauth.go:57-80`).
- The MCP OAuth token exchange doesn't recheck `redirect_uri`. DCR client secrets are
  stored in plaintext (and unused).
- The deep-link jti replay store is per-process (`internal/auth/deeplink.go`). Move it to
  the shared Redis/DB store.

## Non-goals

- Replacing Slack OAuth for people, or adding passwords or PINs anywhere.
- Signing a person in on a shared device, even briefly, to set it up.
- Authenticating wall screens or trivia players.
- Migrating `users` into `actors`, or rewriting `user_id` foreign keys.
- An RBAC editor.

## Decisions (from the user, 2026-10-08)

1. The iPad runs iOS 16+, so the taproom admin UI is React inside the existing console.
2. The trivia driver gets the whole trivia console, including dataset management and
   deletes.
3. No "tap your name" attribution in v1. Actions are recorded as the device.
4. Taproom devices can repoint wall screens to any URL.
5. Only admins approve pairings.
6. No pairing notifications; the approver opens "Pair a device" themselves.
7. Nobody ever signs in on a device. Pairing is a picture match or a code (see phase 4).

## Appendix — auth inventory (2026-10-08)

| Mechanism | Where | Authenticates | Credential / storage | Lifetime / revoke |
|---|---|---|---|---|
| `kit_session` cookie | `auth/session.go:35-111,150` | person | raw token + HMAC; `api_tokens.token_hash` | 30 days; logout deletes the row; no reaper |
| MCP OAuth bearer | `auth/oauth.go:23,327-416`, `middleware.go:118` | person | `api_tokens`, hashed | 90 days; no revoke UI |
| OAuth codes / DCR | `oauth.go:24`, `registration.go:26` | person / client | plaintext code + PKCE; plaintext client secret | 10 min single use / permanent |
| MCP OAuth state | `auth/state.go:28-60` | flow integrity | HMAC JSON | flow |
| PWA Slack OIDC | `cards/web.go:179`, `oauth.go:275`, `pwa_oauth_nonce.go` | person | `__Host-` nonce cookie | 10 min |
| Deep links | `auth/deeplink.go`, `deeplink_middleware.go` | person, mints a session | HMAC claims, in-memory jti | 2 min link → 1 h session |
| Attachment URLs | `attachment/serve.go:18-47` | person + resource | deep-link signer | 6 h, no revoke |
| Integration setup link | `integrations/tokens.go:34-80` | tenant + pending row | HMAC JSON | minutes, single use |
| Poster upload | `events/poster_upload.go:57-135` | person + event | opaque, Redis | 15 min, GETDEL |
| Events feed token | `events/feed.go:332-441` | site build | plaintext setting, constant-time compare | until rotated |
| Widget token | `models/widget_token.go`, `widget/service.go:58` | tenant + origin | sha256 | `revoked_at` |
| Trivia team cookie | `trivia/token.go`, `web_public.go:111,238` | team | sha256 | 6 h, per-game path |
| Trivia reclaim code | `trivia/token.go:83` | team | 4 digits, hashed | single use |
| Kiosk keys | `kiosk/web_public.go:24` | nothing | n/a | n/a |
| Vault step-up | `vault/unlock.go:56-71` | person, second factor | password-derived hash | 5 min window |
| CSRF headers | `X-Kit-Web`, `X-Kit-Chat`, `X-Kit-Vault` | request origin | n/a | n/a |
| Slack events | `slack/handler.go:94-109` | Slack | signing-secret HMAC | 5 min skew |
| Slack install | `slack/oauth.go:57-80` | workspace | Slack OAuth, **no state** | — |
| Scheduler | `scheduler/agent_runner.go:53` | runs as `jobs.created_by` | n/a | n/a |
| Dev login | `cards/web.go:54,326` | any user | none | dev only |

Google and Square have no inbound OAuth (their credentials are pasted in through the
integration setup link). These stay separate on purpose: Slack signatures and Slack
OAuth (external protocols), vault step-up (a second factor), trivia team cookies
(participants), and kiosk keys (not credentials).
