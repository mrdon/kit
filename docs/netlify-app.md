# Kit Netlify App — Design Spec

**Status:** Draft v1
**Owner:** Don
**Audience:** Implementing agent / engineer
**Target location:** `internal/apps/netlify/` (new Kit app)

---

## Background

Netlify shipped **Agent Runners** — a hosted service that takes a prompt + a git branch base, spins up an AI coding agent (Claude, Codex, or Gemini) in their cloud, lets it edit code, commits to a working branch, and produces a preview deploy URL. There is **no local agent loop** required by the caller; everything runs in Netlify's infra and the result is pushed back to the connected git repo.

The opportunity for Kit: a non-technical small-business owner can describe changes to their website ("make the banner blue, then a touch lighter, then move the contact form up") from Slack, get a preview, iterate, and ship — without ever opening a code editor or the Netlify UI. Kit's role is the **conversational refinement layer** that turns vague human intent into a precise prompt before the (expensive) Netlify agent run, and the **review layer** that describes what changed in plain language after.

This is not "Kit replaces a developer." It's a thin orchestration shell over an existing remote agent, sized for the kind of tweaks the owner would otherwise text to their nephew.

## Goals

1. Owner-operator can request small website changes from Slack and ship them, end-to-end, without leaving Slack.
2. Wasted Netlify agent runs are minimized — the bot helps articulate before paying for a run.
3. Iteration is cheap and natural ("no, red"), and previous turns accumulate rather than reset.
4. The user sees, in plain language, what actually changed — not just a preview URL.
5. "Ship it" merges the latest result branch to the production branch; Netlify auto-deploys from there.
6. **No proprietary lock-in; Kit is the non-technical surface, not the primary technical workflow.** Kit is a thin orchestration shell over plain `git` + the Netlify public API. The expectation is that any technical operator — including the dogfooding builder — does the bulk of structural and complex work via `claude` / `codex` directly against the cloned repo. Kit's job is the *Slack-ambient surface for the people who can't or won't open a terminal*. Anything Kit produces (branches, commits, PRs, image commits) is legible to a human reading the repo cold, so the day a technical operator steps away from an org, the next person can pick up the repo with no Kit dependency.

## Non-goals

- Full sitebuilding from scratch. Assume an existing Netlify-connected git repo with a deployed site. v1 is edits, not greenfield.
- Visual editor / drag-drop UI. Slack thread is the surface.
- Multi-tenant cost controls beyond a simple per-tenant monthly Netlify-run budget.
- Supporting agents other than the one Netlify chooses by default (likely Claude). Tenants can pick at install time, not per-turn.
- Image generation, copywriting, or other content production. Bot can pass copy through but won't author it.

## Users

Three-tier user model. Kit's value is in serving tier 1 — the other two exist to make sure tier 1 doesn't trap anyone.

**Tier 1 — Primary end user (~90% of Kit usage): non-technical board member or volunteer.**
Sees Kit only as a Slack bot. Asks for tweaks in plain English ("update the meeting time on the events page", "swap the hero photo to the new one", "publish this update as a blog post"), gets a preview link plus a ship-it button. Has never heard of a branch, never sees the repo, doesn't know or care that the site is on Netlify. This is the user Kit exists for.

**Tier 2 — Technical operator who sets things up (dogfooding builder).**
Manually creates the Netlify project, the GitHub repo, and the Kit tenant for each org. Installs Kit in the org's Slack. Operates *one* Kit instance across multiple orgs (each org is its own Kit tenant — natural fit, no app-specific multi-org logic required). Uses Kit's Slack surface *occasionally* but does the bulk of structural / complex work via `claude` directly against the cloned repo. Kit's role for this user is to cover the gap between what tier 1 can ask for and what would otherwise wait until tier 2 has time.

**Tier 3 — Future handoff.**
The technical operator's eventual successor if they step back from the org. Discovers a vanilla Astro / Next / Hugo repo with normal git history and a connected Netlify project. Kit is removable; the site keeps working. Same workflow as tier 2's day-to-day: clone, `claude`, push. This tier never logs into Kit at all if they don't want to — turning Kit off doesn't break the site.

**Future:** Multiple roles per tenant inside tier 1 — e.g., one role can request changes but only "chair" role can ship. Not v1; Kit's existing role system already supports it the moment we wire the gate.

## End-to-end flow

```
[Slack thread]
User:   Make the banner blue
Bot:    (drafts prompt internally; self-checks: clear enough? Yes)
        Spinning up — I'll have a preview in ~60s.
Bot:    Preview: https://agent-abc.example.netlify.app
        Changes: hero background from #f4f4f4 → #1d4ed8, kept all other styling.
User:   Too dark. Lighter blue.
Bot:    Trying #60a5fa.
Bot:    Preview: https://agent-def.example.netlify.app
        Changes: hero background #1d4ed8 → #60a5fa.
User:   Perfect, ship it.
Bot:    Merged to main. Live in ~90s.
```

Vague-intent variant (clarification gate fires):

```
User:   Make it look more modern.
Bot:    Your site currently uses a serif headline + lots of padding.
        Want flatter (sans-serif, tighter spacing), or just punchier
        colors? You can also say "just try something."
User:   Flatter.
Bot:    Spinning up — I'll have a preview in ~60s.
...
```

## Core concepts

### Change thread

A Slack thread maps to a single **change thread** in Kit. Within a change thread:
- The user iterates on related changes ("blue → red → no, back to blue").
- Each user message produces zero or one **agent run** (zero if the bot still needs to clarify).
- Each agent run forks off the **previous run's result branch** in the same thread (branch chaining), so cumulative state survives across turns.
- A change thread ends when the user ships, abandons, or starts a new thread.

This is similar to how Kit's existing card-chat sessions are keyed by `(card, user)` — here it's `(slack_thread_ts, tenant)`.

### Agent run

One round-trip to Netlify Agent Runners. Mirrors the upstream `AgentRunner` record:
- `id` — Netlify's agent runner ID (used to fetch status and result diff)
- `change_thread_id` — Kit's grouping
- `prompt` — what we sent
- `base_branch` — what we forked off (prior result branch, or production branch on first turn)
- `result_branch` — what Netlify produced (the URL-deployed branch)
- `preview_url` — Netlify deploy preview
- `result_diff` — fetched from git after completion; stored for plain-language summarization and for future-turn context if needed
- `state` — `pending | running | succeeded | failed | cancelled`
- `summary` — bot-generated plain-language description of the diff, shown to user

### Spec doc — explicitly NOT a thing in v1

Earlier design exploration considered a growing per-thread "spec doc." Decision: **don't build it.** The branch state already encodes "what the site looks like now," and each user message is a delta against that. The bot doesn't need to maintain a separate growing artifact.

The exception is when the user references prior turns explicitly ("undo what we did last time", "go back to the blue version"). In that case the bot pulls the relevant `result_diff` from prior agent runs in the thread and injects it as context. This stays in the database; it isn't a user-visible artifact.

## Netlify integration

### Public CLI/API surface

The public surface that's actually documented is the **Netlify CLI** wrapping the REST API at `https://api.netlify.com/api/v1/`. Auth is OAuth2 or a personal access token (same one CLI accepts via `--auth`).

CLI commands (mirror to direct HTTP calls in our Go client):
- `netlify agents:create --prompt "..." --agent claude --branch <base> --project <site-id> --json` — kick off a run. Returns a run id.
- `netlify agents:list --project <site-id> --json` — list runs for the site.
- `netlify agents:show <run-id> --json` — get run state + result branch + preview URL.
- `netlify agents:stop <run-id>` — cancel.

The preview URL for an in-progress or completed run follows a deterministic format: `https://agent-<run-id>--<site-slug>.netlify.app`. We can show the URL to the user as soon as the run id is returned (it 404s until the build completes, but it's correct).

There is no documented "follow-up" / parent-run field on the public surface. The dashboard's follow-up UX appears to drive the same `agents:create` with `--branch <prior-result-branch>` under the hood — i.e. **branch chaining IS the follow-up mechanism**. We'll do the same: each iteration turn calls `agents:create --branch <last-result-branch>`. If a `parent_agent_runner_id`-style field shows up in the API later, we adopt it then; meanwhile branch chaining is the contract.

### Branch chaining (the documented fallback)

For each turn after the first, pass `--branch` = the previous turn's `result_branch`. Netlify forks a fresh agent branch off it, so the new agent sees the cumulative state. Each turn produces a new branch; the latest is always the head of the chain. "Ship it" merges the latest result branch to production via GitHub API; Netlify auto-deploys from there.

This loses the agent's *conversation memory* across turns (each agent run starts cold), but the *code state* carries over. For tweak-style edits this is almost always what you want: "now make it red" with a branch where the banner is currently blue just yields a one-line color swap.

### Images

Image edits ("use this photo as the hero", "add these three product shots to the gallery") are the second most common ask after styling tweaks, so they're in v1. The implementation principle: **the repo holds URLs, not bytes.** Images live in object storage so clones stay sub-10MB regardless of how much media accumulates over years of use.

**Storage:** **Netlify Blobs** is the default. Same Netlify account used for the site, free tier handles hundreds of MB, and Netlify Image CDN reads directly from Blobs for on-demand transforms — no extra cloud account, no extra credential, no extra origin to configure. If a tenant ever needs to escape Netlify entirely, Blobs has a documented API for bulk export.

**Optimization / variants:** Netlify Image CDN handles resize, crop, format negotiation (AVIF/WebP) on demand via URL transforms — `/.netlify/images?url=<blob-url>&w=1600`. The agent run references that URL pattern when wiring up `<img>` tags; no pre-generated sizes to maintain.

**Upload flow (Slack):**
1. User drops an image into the Slack thread alongside a message ("use this for the hero").
2. Kit downloads the file from Slack's `files.info` URL using the bot token.
3. Kit uploads to Netlify Blobs under a per-tenant prefix and a stable key (`<short-uuid>-<slugified-original>.<ext>`).
4. Kit captures the resulting Blob URL.
5. The agent prompt then references the URL: *"A new image is available at `https://blobs.netlify.app/<tenant>/abc-hero.jpg`. Use it as the hero, replacing the current one. Wrap it with `/.netlify/images?url=...&w=1600` for the responsive variants."*

The agent never commits image bytes to the repo — only the URL reference in HTML/JSX/markdown. Branches stay text-only and clones stay fast.

**Repo-resident exception (the only one):** tiny static assets that *belong* in the repo because they're code-shaped — favicons, the org logo SVG, social-share OG images that ship once and rarely change. Sub-100KB stuff. The agent decides per case; the default rule is "Blobs unless it's tiny, versioned, and design-critical."

**Why this matters for the dev escape hatch:** a future technical operator does `git clone` and gets a fast checkout with a working dev server. Image URLs resolve to the live Blobs CDN — placeholders if they're working fully offline, real images when online. No git LFS, no submodules, no surprise 500MB checkout.

**Scale ceiling:** Netlify Blobs scales well into the GB range. If a tenant ever outgrows it (rare for a marketing site), the same URL-in-repo pattern swaps in any S3-compatible store with a config change — Cloudflare R2, Bunny CDN, Cloudinary. The orchestration code doesn't care; only the upload target changes.

**Out of v1:** image generation (Kit doesn't author images), background removal / editing tools, automatic alt-text (the code agent can write alt-text inline if prompted), bulk import of existing repo-resident images into Blobs (handled by a one-off migration tool per tenant, not by Kit's runtime).

### Reading the diff from git

After an agent run completes, fetch `GET /repos/<owner>/<repo>/compare/<production_branch>...<result_branch>` via the GitHub API. The unified diff feeds:
- The plain-language summary the bot posts back to Slack.
- Context for next-turn clarification ("you're currently on a serif headline...").
- "Undo" / "what did you change" follow-ups.

Diffs are cheap to fetch; do it every turn. Do *not* clone the repo — the GitHub compare endpoint gives the same info via HTTP.

## Bot behavior

### Clarification gate

Before each agent run, the bot self-checks: *would the downstream code agent know what to build from this prompt?*

Implementation: small Haiku call with the user's message + a short summary of the current branch state. If it answers "clear," ship the run. If "unclear," it must produce the *single* most useful clarifying question (with 2–3 inline-button options when possible). The user picks or types a freeform answer, and the gate runs once more — if still unclear, ship with what we have. Never gate more than once per user turn.

The gate is **skipped** on iteration turns where context obviously resolves the ambiguity ("now red" after "make banner blue" is unambiguous given branch state).

### Summarizing the diff back

After each successful agent run, the bot:
1. Fetches the GitHub compare diff.
2. Sends it to Haiku with a "summarize what changed in one or two sentences, in plain language" prompt.
3. Posts a Slack message with:
   - The preview URL (linked text, not a raw URL — "**Preview the change**").
   - The plain-language summary.
   - **Three Slack interactive buttons:** `Ship it` (primary/green), `Keep iterating` (default — keeps the thread open as a hint), `Discard` (danger).
   - If the diff is suspiciously broad ("touched 47 files" for a "make banner blue" request), prepend a ⚠️-prefixed warning line before the buttons — *"this changed more than I expected; want to look before shipping?"*

Button clicks route through Kit's existing Slack interactive-message handler. `Ship it` invokes the same code path as the `netlify_ship_change` tool. `Discard` invokes `netlify_abandon_change`. `Keep iterating` is a no-op acknowledgement — the thread is already open for the next message.

### Shipping

`ship it` (or any clear merge intent the bot detects) triggers a GitHub merge of the latest `result_branch` into the production branch. Netlify's existing auto-deploy-on-merge takes care of prod deploy.

Edge case: if other commits have landed on production since the chain started, the merge may conflict. v1 behavior: bail with a "the site changed since we started — start a new thread to apply this on top of the latest" message. Don't try to rebase automatically.

### Abandon

If the user says "nevermind" / "scrap it" / closes the thread without shipping, no cleanup is needed — the agent branches stay in git as orphan branches. Worth a periodic cron sweep (weekly) to delete agent branches older than 30 days that were never shipped. Out of scope for v1; nice-to-have.

## Kit app structure

Following the `internal/apps/` pattern (see `apps/task/`, `apps/cards/`):

```
internal/apps/netlify/
  app.go           # Init, Configure, self-registration
  service.go       # NetlifyService: trigger run, poll, fetch diff, merge
  models.go        # ChangeThread, AgentRun (DB-backed)
  agent.go         # Slack-message handler: clarification gate + dispatch
  prompts/         # System prompts for clarifier + summarizer (one .tmpl each)
    system_clarifier.tmpl
    user_summarize_diff.tmpl
  github.go        # Thin GitHub API wrapper (compare diff + merge)
  netlify.go       # Thin Netlify API wrapper (agent_runners endpoints)
  mcp.go           # MCP tools (see below)
  service_test.go
```

### Tool surface (agent + MCP parity)

Per Kit conventions, every tool exists on both the LLM agent surface and the MCP surface. For v1:

- `netlify_request_change(prompt)` — start or continue a change thread. Handles clarification internally if needed. Returns preview URL or a clarifying question.
- `netlify_ship_change(thread_id)` — merge the latest result branch.
- `netlify_list_pending_changes()` — list open change threads for the tenant.
- `netlify_abandon_change(thread_id)` — mark a thread done without shipping.

The Slack handler is the primary entry point; MCP tools enable Cowork / Claude Code clients to drive the same flow.

### Tenant configuration

**Why this lives in the PWA, not Slack:** the LLM agent sees every chat message. Tokens, install codes, OAuth callbacks must never travel through that channel — not even briefly, not even redacted. The Kit PWA's settings surface is the boundary: secrets enter through a web form / OAuth dance, get stored encrypted, and the agent only ever sees them via the service layer (which decrypts in-memory and drops them immediately after the call). Same pattern as Kit's existing `vault` app.

**Settings page** lives in the PWA at `/apps/netlify/settings`, scoped to the current tenant. Two connection cards:

```
┌─ Netlify ─────────────────────────────────┐
│ Status: Not connected                     │
│ [ Connect Netlify ]                       │
└───────────────────────────────────────────┘

┌─ GitHub ──────────────────────────────────┐
│ Status: Not connected                     │
│ [ Install Kit GitHub App ]                │
└───────────────────────────────────────────┘
```

**Note on the GitHub App.** It's a **workspace-scoped, Kit-wide GitHub App** — not specific to the Netlify app. Same pattern as Kit's single shared Slack bot: one Kit GitHub App per workspace, used by every future feature that ever touches GitHub (PR-decisions, issue-tasks, etc.). Today only the Netlify app needs it, so the install entry point lives on the Netlify settings page; when a second Kit feature needs GitHub, the install gets promoted to a workspace-level settings page and both features reference the shared installation. Env vars are accordingly `KIT_GITHUB_APP_*` (not `NETLIFY_GITHUB_APP_*`).

**Connect Netlify** (OAuth):
1. Click → redirect to Netlify's `authorize` endpoint with Kit's client_id + redirect_uri.
2. User approves on Netlify.
3. Netlify callback hits a Kit handler with an auth code.
4. Kit exchanges the code for an access token (+ refresh token), encrypts via `internal/crypto`, stores in `app_netlify_config`.
5. Page refreshes; a **site picker dropdown** appears (Kit calls Netlify API with the new token, lists the user's sites). User picks the site Kit will operate on. Save.

**Install Kit Website GitHub App** (GitHub App install):
1. Click → redirect to `https://github.com/apps/kit-website/installations/new`.
2. User picks the org/account and selects **only the specific repo** Netlify is connected to (Kit pre-suggests it based on the Netlify site's metadata).
3. GitHub install callback hits Kit with an `installation_id`.
4. Kit stores `installation_id` only. No long-lived token. Installation tokens are minted on demand (1-hour TTL) using Kit's GitHub App private key.

**Why GitHub App over SSH key or PAT:**
- SSH keys only authorize git transport (push/pull). We need REST API access for compare diffs, merge, and the Contents API. SSH alone can't do that.
- A PAT works but is long-lived, broadly-scoped, and pasted by hand — every PAT in Kit is a future revocation chore.
- GitHub App tokens are short-lived (1 hour), scoped to the specific repos chosen at install time, and revoking the install drops Kit's access cleanly with zero DB cleanup.

**What's in `app_netlify_config`** (encrypted at rest):
- `netlify_access_token`, `netlify_refresh_token` (refresh handled in-band when the access token expires)
- `netlify_site_id`
- `github_installation_id` (small, not really a secret, but stored consistently)
- `github_repo_owner`, `github_repo_name` (denormalized for fast lookup)
- `production_branch` (auto-detected from Netlify's site config, defaulting to `main`)
- `default_agent` (`claude` / `codex` / `gemini`)
- `monthly_run_budget` (cost cap)

**Revocation:**
- Kit-side: settings page has a "Disconnect" button per service. Drops the token / installation reference.
- User-side: revoking the OAuth grant in Netlify or uninstalling the GitHub App on GitHub immediately breaks Kit's access. Kit detects 401s on next use and pings the user to reconnect.

### Database tables

All tenant-scoped per Kit conventions (`tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE`).

- `app_netlify_config` — one row per tenant: `netlify_site_id`, `github_repo_owner`, `github_repo_name`, `production_branch`, encrypted `netlify_access_token` + `netlify_refresh_token`, `github_installation_id`, `default_agent` (claude/codex/gemini), `monthly_run_budget`, `blobs_store_name` (per-tenant Blobs namespace for images).
- `app_netlify_change_threads` — one row per Slack thread; tracks state (active/shipped/abandoned), production branch snapshot at start, latest agent run id.
- `app_netlify_agent_runs` — one row per Netlify agent run; foreign key to change_threads. Mirrors upstream `AgentRunner` plus our `summary` and `result_diff`.
- `app_netlify_image_uploads` — one row per image uploaded to Blobs via Slack; tracks blob URL, original filename, uploading user, and which agent run (if any) first referenced it. Lets us answer "which image did you mean?" and supports a future "unused images" cleanup pass.

## Open questions

Decisions to make during implementation:

1. **Authentication shape.** GitHub App (cleanest, supports many tenants, requires installation flow) vs. per-tenant PAT (simpler but worse UX and worse security). Recommend GitHub App.
2. **Netlify auth.** Netlify OAuth (best UX) vs. user-pasted API token (faster to ship). Recommend OAuth for v1.
3. **Cost controls.** Netlify agent runs cost real money. v1 should at least enforce a monthly cap per tenant. Where to surface "you've used 8 of 20 runs this month" — Slack at-mention? PWA settings page?
4. **Conflict handling on merge.** v1 bails. v2 could attempt auto-rebase via `git merge-tree`, but that's a rabbit hole.
5. **What model the clarifier uses.** Haiku is cheap and probably fine. Worth measuring quality vs. Sonnet once we have real traffic.
6. **Streaming progress.** Netlify runs take 30–120s. Do we send "still working..." pings, or just radio silence until done? Probably one ping at 30s if not done.

## Out of scope for v1

- Multiple sites per tenant (single Netlify site per install).
- Approval workflow (someone other than the requester must ship). Add when we have role-scoped permissions.
- Partner DAM integration (Cloudinary / Bynder). Repo-resident images via Netlify Image CDN cover v1; partner integration is v2 when a tenant outgrows hundreds-of-images scale.
- Image generation, editing, or auto-alt-text as a separate step. The code agent can author alt-text inline if asked.
- Scheduled change requests ("apply this every Monday").
- Rollback UX. For v1, "make a new change that undoes it" is the rollback story.
- Cross-thread context. Each Slack thread is independent; we don't try to learn "the owner likes flat designs" across threads.

## Implementation order

1. Tenant install flow: PWA settings page with Netlify OAuth + site picker + GitHub App install. Store encrypted in `app_netlify_config`. No agent calls yet.
2. Single-shot agent run: `netlify_request_change` triggers a run, polls, posts preview URL. No clarifier, no diff summary, no chaining.
3. Diff fetching + plain-language summary.
4. Branch chaining for iteration.
5. Clarification gate.
6. Ship / merge flow + Slack interactive buttons (`Ship it` / `Keep iterating` / `Discard`).
7. Image upload path: Slack file → Netlify Blobs upload (per-tenant prefix) → agent prompt references the Blob URL. Repo stays text-only.
8. Cost cap + Slack "budget remaining" surfacing.
9. Watch for a `parent_agent_runner_id`-style field on the public agent_runners API; adopt if it appears. Branch chaining stays the contract until then.

Each step is independently testable and useful; ship them as separate commits.
