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

## Non-goals

- Full sitebuilding from scratch. Assume an existing Netlify-connected git repo with a deployed site. v1 is edits, not greenfield.
- Visual editor / drag-drop UI. Slack thread is the surface.
- Multi-tenant cost controls beyond a simple per-tenant monthly Netlify-run budget.
- Supporting agents other than the one Netlify chooses by default (likely Claude). Tenants can pick at install time, not per-turn.
- Image generation, copywriting, or other content production. Bot can pass copy through but won't author it.

## Users

**Primary:** Same dogfooding owner-operator as the rest of Kit. Has a marketing site on Netlify, makes small content/styling tweaks a few times a month, currently does it via email-the-developer or by editing in the Netlify dashboard themselves.

**Future:** Multiple roles per tenant — e.g., one role can request changes but only "owner" role can ship.

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

Triggering is via `POST /api/v1/agent_runners?site_id=<id>` with body:
```json
{ "branch": "<base>", "prompt": "<text>", "agent": "claude", "model": "<optional>" }
```

Polling is via `GET /api/v1/agent_runners/<id>` for state, and `GET /api/v1/agent_runners/<id>/sessions` for the result diff and step trace.

There is no documented "follow-up" endpoint, but the `AgentRunner` schema has a `parent_agent_runner_id` field that is clearly used by the web dashboard's follow-up feature. **Try sending it first; fall back to branch chaining if rejected.**

### Branch chaining (the documented fallback)

For each turn after the first, pass `--branch` = the previous turn's `result_branch`. Netlify forks a fresh agent branch off it, so the new agent sees the cumulative state. Each turn produces a new branch; the latest is always the head of the chain. "Ship it" merges the latest result branch to production via GitHub API; Netlify auto-deploys from there.

This loses the agent's *conversation memory* across turns (each agent run starts cold), but the *code state* carries over. For tweak-style edits this is almost always what you want: "now make it red" with a branch where the banner is currently blue just yields a one-line color swap.

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
3. Posts: preview URL + the summary + a "ship it / try again" hint.

If the diff is suspiciously broad ("touched 47 files" for a "make banner blue" request), surface that — "this changed more than I expected; want to look before shipping?"

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

At install time the tenant connects:
1. Their Netlify account (OAuth or API token — Netlify CLI uses `--auth` tokens; mirror that).
2. The specific Netlify site to operate on.
3. The GitHub repo (auto-discovered from the Netlify site's connected repo).

Stored in `app_netlify_config` (per Kit's app-table prefix convention), encrypted token at rest using `internal/crypto`.

### Database tables

All tenant-scoped per Kit conventions (`tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE`).

- `app_netlify_config` — one row per tenant: site_id, github_repo, encrypted Netlify token, encrypted GitHub token, default agent (claude/codex/gemini), monthly run budget.
- `app_netlify_change_threads` — one row per Slack thread; tracks state (active/shipped/abandoned), production branch snapshot at start, latest agent run id.
- `app_netlify_agent_runs` — one row per Netlify agent run; foreign key to change_threads. Mirrors upstream `AgentRunner` plus our `summary` and `result_diff`.

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
- Image / asset uploads from Slack ("use this photo as the hero"). Workable later — Slack file attachment + GitHub commit via the same code path.
- Scheduled change requests ("apply this every Monday").
- Rollback UX. For v1, "make a new change that undoes it" is the rollback story.
- Cross-thread context. Each Slack thread is independent; we don't try to learn "the owner likes flat designs" across threads.

## Implementation order

1. Tenant install flow: Netlify OAuth + site picker + GitHub App install. No agent calls yet.
2. Single-shot agent run: `netlify_request_change` triggers a run, polls, posts preview URL. No clarifier, no diff summary, no chaining.
3. Diff fetching + plain-language summary.
4. Branch chaining for iteration.
5. Clarification gate.
6. Ship / merge flow.
7. Try `parent_agent_runner_id` for true follow-ups; keep branch chaining as fallback.
8. Cost cap + Slack "budget remaining" surfacing.

Each step is independently testable and useful; ship them as separate commits.
