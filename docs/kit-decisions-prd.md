# Kit Decisions — Product Requirements Document

**Status:** Draft v1
**Owner:** Don
**Target:** MVP in Kit, built as a PWA surface at `kit.twdata.org/app` (or subdomain)
**Audience:** Implementing agent / engineer

---

## Background

Kit is a team knowledge and automation layer for small businesses. Today it's reached via Slack (primary) and MCP clients (Claude Code, Cursor, Cowork). Kit's agent can execute workflows, but when a workflow needs a human judgment call, the only surface for that is a Slack DM — which gets buried, ignored, or interrupts flow.

The broader problem: agent oversight is the bottleneck on agents being useful. Every AI product has the same weakness — the human-in-the-loop interface is terrible, so agents either run unsupervised (risky) or require too much oversight time to save effort.

The insight this PRD addresses: humans are good at fast intuitive judgment on well-framed options at volume, and the apps that exploit this (Tinder, TikTok, Gmail triage) have not yet been applied to agent oversight. A swipeable decision deck for agent-surfaced asks is likely the right shape.

## Goals

1. Give Kit a first-class way to ask a human to resolve a pending decision, without interrupting their flow.
2. Give Kit a first-class way to push informational updates ("briefings") that don't need action but should be seen.
3. Make reviewing these fast — a morning stack should take minutes, not half an hour.
4. Work well on Android (primary target: owner's own phone), fine on iOS.
5. No native app for v1 — PWA only. No App Store friction.

## Non-goals

- Voice input / voice output for v1. Nice-to-have later.
- Multi-user auth gymnastics beyond Kit's existing Slack sign-in.
- Custom notification categories, per-channel routing, or complex settings UI for v1.
- Full offline mode. Stale-while-revalidate is fine; full offline CRUD is not v1.
- iOS Shortcuts / Siri integration. Not v1.
- Widgets (home screen or lock screen). Not v1.

## Users

**Primary:** Owner-operator of a small business (brewery in the dogfooding case). Has Kit installed in Slack, has MCP configured for Cowork or Code, does back-office work on mobile between physical tasks.

**Future:** Taproom manager, head brewer, etc. — role-scoped stacks where each person sees decisions relevant to their role.

## Core concepts

Two new first-class concepts in Kit:

### Decision

A pending judgment call that needs a human to resolve before Kit proceeds. Created by agents, scheduled tasks, or skills when they hit a fork that requires input.

**Required fields:**
- `id` (ULID or UUID)
- `tenant_id`
- `title` (string, short — shown as card heading, ~60 char target)
- `context` (markdown, the one-paragraph framing the user needs to decide)
- `options` (array of `{id, label, action_spec}`)
- `recommended_option_id` (nullable — Kit's suggestion, if any)
- `role_scopes` (array — which roles see this decision; defaults to `tenant`)
- `priority` (`low | medium | high`)
- `state` (`pending | resolved | expired | cancelled`)
- `created_at`
- `expires_at` (nullable — if set, auto-expires to default action or no-op)

**On resolution:**
- `resolved_at`
- `resolved_by` (user id)
- `resolved_option_id` (which option was picked, or `null` if dismissed)
- `resolved_custom` (nullable — free-text from follow-up, see below)

**Option action_spec:** a structured directive for what Kit should do when this option is chosen. At minimum supports:
- `tool_call: {name, args}` — invoke an MCP tool or Kit tool
- `skill_invoke: {skill_id, args}` — run a skill
- `noop` — do nothing (for "deny" / "skip" options)
- `message: {channel, text}` — post something to Slack
- `custom` — free-form description, Kit's agent figures out how to execute

### Briefing

An informational update Kit wants the user to see. No action required. Useful for surfacing anomalies, recaps, signals the user would want to know.

**Required fields:**
- `id`
- `tenant_id`
- `title` (string)
- `body` (markdown, can be longer than decision context — up to a few paragraphs)
- `severity` (`info | notable | important`)
- `role_scopes`
- `state` (`pending | archived | dismissed`)
- `created_at`
- `related_decision_ids` (array, optional — briefings can link to decisions Kit suggests as followups)
- `sources` (array of `{label, url}`, optional — links to underlying data/Slack messages/etc.)

**On acknowledgement:**
- `ack_at`
- `ack_by`
- `ack_kind` (`archived | dismissed | saved`)

### Relationship

Briefings and decisions can reference each other. A briefing about "sales down 30% last Tuesday" can link to decisions like "post a midweek special" or "ask taproom manager for input." A decision can reference the briefing that prompted it for additional context.

Kit should prefer creating a decision over a briefing when it has a concrete recommended action. Briefings should be for genuine "you should know this" moments where no specific action is obvious yet.

## API surface

All endpoints live under `kit.twdata.org/api/v1/` (or your existing API base). Authenticated via the same session Kit uses for Slack sign-in.

### Stack retrieval

```
GET /stack
```

Returns a mixed, ordered list of pending decisions and briefings for the current user, filtered by their role scopes.

Ordering:
1. High-priority pending decisions (newest first)
2. Medium-priority pending decisions (newest first)
3. Important-severity briefings (newest first)
4. Low-priority pending decisions
5. Notable briefings
6. Info briefings

Response shape:

```json
{
  "items": [
    {
      "kind": "decision",
      "id": "...",
      "title": "...",
      "context": "...",
      "options": [...],
      "recommended_option_id": "...",
      "priority": "high",
      "created_at": "...",
      "related_briefing_id": null
    },
    {
      "kind": "briefing",
      "id": "...",
      "title": "...",
      "body": "...",
      "severity": "notable",
      "sources": [...],
      "related_decision_ids": [...],
      "created_at": "..."
    }
  ],
  "cursor": null
}
```

### Decision resolution

```
POST /decisions/:id/resolve
{
  "option_id": "...",        // or null if dismissing without picking
  "custom": "optional free-text note"
}
```

Kit executes the option's `action_spec` server-side and transitions the decision to `resolved`. Returns the updated decision.

```
POST /decisions/:id/snooze
{
  "until": "ISO timestamp"  // or omit for "back of stack, resurface next session"
}
```

### Briefing acknowledgement

```
POST /briefings/:id/ack
{
  "kind": "archived" | "dismissed" | "saved"
}
```

`archived` = seen, useful. `dismissed` = seen, not useful (feeds back into the usefulness signal for future briefings). `saved` = flagged for later reference, stays visible in a "saved" view.

### Follow-up / clarifying question

```
POST /decisions/:id/followup
{
  "question": "..."
}
```

```
POST /briefings/:id/followup
{
  "question": "..."
}
```

Creates a new conversation turn with Kit's agent using the decision/briefing as context. Returns the agent's response (streaming or full). This is how "ask a clarifying question" from the detail view works.

### Escalating a briefing to a decision

```
POST /briefings/:id/escalate
```

Asks Kit's agent to generate a decision based on the briefing's context. Returns the new decision id. Useful for "this briefing made me realize I need to make a call on something — what are my options?"

### Undo

```
POST /undo
{
  "action_id": "..."  // returned by every write endpoint
}
```

Every resolve/ack/snooze returns an `action_id` that can be undone for up to 30 seconds. After that the action is final.

### MCP tools (for agents creating decisions and briefings)

New MCP tools Kit exposes for its own agent and for skills to call:

**`create_decision`**
```
{
  title: string,
  context: string,           // markdown
  options: [{id, label, action_spec}],
  recommended_option_id?: string,
  role_scopes?: string[],    // defaults to ["tenant"]
  priority?: "low" | "medium" | "high",
  expires_at?: string
}
```
Returns the decision id.

**`create_briefing`**
```
{
  title: string,
  body: string,              // markdown
  severity?: "info" | "notable" | "important",
  role_scopes?: string[],
  sources?: [{label, url}],
  related_decision_ids?: string[]
}
```
Returns the briefing id.

**`list_pending_decisions` / `list_pending_briefings`** — for agents that need to check what's already queued before creating more.

## Push notifications

**Web Push via VAPID.** Kit generates a VAPID keypair, stores per-user subscription endpoints when the user grants notification permission in the PWA.

**Notification dispatch rules:**
- High-priority decisions: immediate push
- Medium-priority decisions: batched, at most one push per 30 min
- Low-priority decisions: no push (appears in stack only)
- Important briefings: immediate push
- Notable / info briefings: no push

**Actionable notifications.** On Android, notifications include inline action buttons:
- Decision notification: "Approve" and "Open" buttons. "Approve" calls `/decisions/:id/resolve` with the recommended option directly from the notification. "Open" opens the app to that card.
- Briefing notification: "Mark seen" and "Open" buttons.

**iOS note:** iOS Safari web push works but requires the app to be installed to home screen and doesn't support action buttons as cleanly. For v1, iOS users get notifications without action buttons; tap opens the app.

## UI / UX

### Stack view

Feed-scroll vertical layout. Cards render sequentially. No snap-to-card behavior for v1 — flow is continuous.

Cards are visually distinct by kind:

**Decision card:**
- Clear action-oriented look. Prominent title. Context below. Options rendered as buttons at the bottom, with the recommended option highlighted.
- Colored left border or accent indicating priority (red for high, yellow for medium, neutral for low).
- Always shows "tap for more context" affordance if context is truncated.

**Briefing card:**
- Content-oriented look, closer to a news feed post. Title + body. Severity indicated subtly (maybe an icon or small label).
- No action buttons in the card view; just the content.
- If `related_decision_ids` are present, a small "2 related decisions" link at the bottom.

### Gestures

**Vertical scroll:** navigate through the stack. Standard browser behavior.

**Horizontal swipe:** action.
- Swipe right on a decision = approve recommended option.
- Swipe left on a decision = deny / skip.
- Swipe right on a briefing = archive (useful).
- Swipe left on a briefing = dismiss (not useful).

**Long-press (500ms):** contextual shortcut.
- Long-press on a decision = snooze (back of stack, or snooze timer if implemented).
- Long-press on a briefing = save/flag.

**Tap:** open the detail view for that card.

**Tap on a specific option button (decision only):** pick that option directly (bypasses the "swipe right = recommended" shortcut).

### Swipe feedback

- During swipe, show a preview label of what will happen ("Approve: send email" on the right side as user swipes right; "Deny" on the left).
- Cross a visible threshold (~40% of card width), haptic fires, release to commit.
- If swipe is cancelled (doesn't cross threshold), card bounces back with no haptic.
- After commit, card animates off-screen in the swiped direction; next card slides into place.

### Haptics (Android Vibration API)

- Light tap (10ms) on scroll snap or card focus
- Medium tap (25ms) on swipe threshold crossed
- Distinct pattern (10-30-10ms) on commit
- No haptic on cancelled swipe

### Detail view

Tap any card to open the detail view. Full-screen mode.

**Decision detail:**
- Full title + full context (no truncation)
- All options rendered as tappable buttons, recommended one highlighted
- "Ask a follow-up" compose box (text input + send)
- "Snooze" button with preset options (1h, 4h, tomorrow, next week)
- Back gesture / back button returns to the stack at the same position

**Briefing detail:**
- Full title + full body
- Sources rendered as tappable links
- Related decisions rendered as mini-cards, tappable to jump to them
- "Ask a follow-up" compose box
- "Turn into a decision" button — calls `/briefings/:id/escalate`
- "Archive / Dismiss / Save" buttons
- Back returns to stack

### Follow-up flow

When the user taps "ask a follow-up" on a card, they get a compose input (text for v1; voice later). Their question is sent to Kit's agent with the decision/briefing as context. Response renders inline in the detail view, turn by turn. Think of it as a scoped mini-chat.

If the follow-up turns up new options for a decision, Kit's agent can call `create_decision` with updated options, and the current decision can be cancelled in favor of the new one. (Or the existing decision's options can be amended — pick whichever is simpler to implement; the latter is probably cleaner.)

### Undo

After any commit action (swipe, tap, option pick, archive, dismiss), a snackbar appears at the bottom with "[Action] — Undo" for 30 seconds. Tapping "Undo" calls the undo endpoint and restores the card to the stack.

### First-run gesture hints

On first app open, animate a small translucent pointer over the first card demonstrating:
1. Swipe right to approve
2. Swipe left to deny
3. Long-press to snooze
4. Tap to open

One pass. Dismissible. Never shown again unless user taps Help.

### Empty state

When the stack is empty, show: "Nothing needs you right now." Plus a small link to "recent history" and "saved briefings."

### History view

Accessible via a button in the top bar. Shows recently resolved decisions and acknowledged briefings, with the ability to see what was decided and (for briefings marked "saved") pinned items.

## Ordering, priority, and expiration

- Default stack ordering as described above (high-priority decisions first, etc.).
- Decisions with `expires_at` auto-expire at that time. Expired decisions execute their `default_on_expiry` action if set (future field, not v1), otherwise transition to `expired` state and vanish from stack.
- Snoozed decisions resurface at the snooze time.
- Briefings don't expire automatically in v1 but should auto-archive after 7 days if untouched (configurable later).

## Role scoping

Kit already has roles. Decisions and briefings use the same role-scoping mechanism:
- `role_scopes: ["tenant"]` — visible to everyone (default for most cases)
- `role_scopes: ["managers"]` — only visible to users in that role
- `role_scopes: ["bartenders", "managers"]` — visible to either
- `role_scopes: []` — invisible (default deny, same as existing skills/rules)

The stack API filters based on the calling user's roles.

## Auth

Uses Kit's existing auth — Sign in with Slack via OpenID Connect. The PWA's session is established the same way MCP clients establish theirs. No new auth surface.

First-time users of the app are prompted to:
1. Sign in with Slack (redirect + callback)
2. Grant notification permission (optional, skippable, re-promptable later)
3. Optionally "Add to Home Screen" (shown as a soft banner if the PWA detects it's not installed)

## PWA specifics

- `manifest.json` with icons (192 and 512 at minimum), theme color, `display: standalone`, start url `/app`
- Service worker handling:
  - Push event reception
  - Offline cache for app shell (HTML/JS/CSS)
  - Stale-while-revalidate for stack API (show cached stack immediately, refresh in background)
  - Background sync for queued resolve/ack calls if user acts while offline (optional for v1)
- Installable on Android Chrome / Edge and iOS Safari (16.4+ for push)
- Meta tags for iOS home-screen icon, status bar style, etc.

## Build order

Strictly sequential — each step should be usable and valuable on its own.

### Phase 0: data model + API (backend only)

- Add `decisions` and `briefings` tables + migrations
- Implement the CRUD + state transition services
- Expose `create_decision` and `create_briefing` as MCP tools
- Implement `/stack`, resolve/ack/snooze/undo/followup endpoints
- Dogfood by manually calling `create_decision` via MCP from Cowork or Code, verify it round-trips

### Phase 1: minimum viable web UI

- Plain mobile-responsive page at `/app` (or `/decisions` for v0)
- Feed-style scroll, cards render with on-screen buttons only
- No gestures, no PWA, no push — just a web page that renders the stack and lets you tap buttons to resolve
- Use it for a week. If the flow feels useful, move on. If not, iterate on the decision *framing* (the `context` and `options` agents produce) before investing more in the UI.

### Phase 2: PWA + polish

- Manifest + service worker + installable
- Gesture support (swipe, long-press, tap) with thresholds, previews, haptics
- Undo snackbar
- Detail view with follow-up compose box
- First-run gesture hints

### Phase 3: push notifications

- VAPID setup, subscription endpoint, dispatcher
- Actionable notifications on Android
- Notification preferences in settings (all / high-priority only / none)

### Phase 4: briefings

- Same data model + API support should already be there from Phase 0; just needs the card type rendered in the UI
- Related-decision cross-linking
- "Turn into decision" escalation
- Saved-briefings view in history

### Phase 5: iteration

Things to decide after using it for a few weeks:
- Snooze timing controls (currently just "back of stack")
- Smart auto-approval for high-trust decision types ("you always approve the routine reorder — should I start doing it automatically?")
- Confidence-weighted batch actions ("approve the next 3 routine ones" gesture)
- Additional gestures only if the core four feel limiting

## Quality bar for agent-produced cards

This is outside the scope of the PWA itself but is the single biggest determinant of whether the app works. Worth making explicit so the agents building decisions produce the right thing.

**Decision cards must:**
- Be resolvable in one glance. Title + context should answer "what are you asking me?" in under 10 seconds of reading.
- Have a recommended option in almost all cases. "What do you think?" without a suggested answer is lazy.
- Have 2–4 options. More than 4 means the decision wasn't framed tightly enough.
- Include the evidence needed to decide in the context. Don't make the user go look something up.
- Route to a detail view for edge cases, but the card itself should be sufficient for the common case.

**Briefing cards must:**
- Pass the "so what" test. If the user can't think of something to do differently after reading, the briefing shouldn't have been created.
- Not duplicate routine recaps. Scheduled weekly/monthly recaps are okay. Daily briefings should be threshold-triggered, not time-triggered.
- Escalate to a decision when there's a clear action. "Sales down 30%" as a pure briefing is less useful than "Sales down 30% — want to draft a midweek special?" as a decision.

A future skill/rule addition to Kit should enforce these conventions — e.g. a "decision author" skill that formats and sanity-checks a decision before it's committed to the queue. Out of scope for this PRD but worth flagging for Phase 5.

## Open questions

1. **Option execution model.** `action_spec` as a structured type vs. free-text instruction that Kit's agent interprets at resolve time. Structured is safer and more predictable; free-text is more flexible. Probably start with structured and add `custom` as an escape hatch.

2. **Follow-up amending options.** If a follow-up Q&A produces new options ("what if we draft it differently?"), do we amend the existing decision or create a new one? Amending is cleaner UX but more complex state. Creating a new one is simpler but leaves stale decisions lying around. Default to amending with a revision history field.

3. **Multi-user decisions.** What if two users are scoped to the same decision and one resolves it first? Second user should see it vanish / show as "resolved by X." Handle via real-time WebSocket push or polling refresh. Probably polling is fine for v1 given the scale.

4. **Confidence / auto-approval.** When does Kit start auto-approving things the user always approves? Probably Phase 5 territory but worth tracking as a signal in Phase 1 — record which options get picked consistently to inform future auto-approval policy.

5. **Cross-device sync.** If the user resolves a decision on their phone and then opens the app on desktop, they should see it as resolved. Covered by the API being the source of truth. Just noting that the UI should refresh on focus/visibility change.

## Success metrics

For v1 (while dogfooding):

- Daily active usage by the owner. If the stack doesn't become a daily habit within two weeks, the pattern's not working.
- Stack clear rate. What percentage of created decisions get resolved within 24h? Target >80%.
- Follow-up rate. What percentage of decisions get a follow-up question before resolution? Signals whether the initial framing is good — if >30%, framing needs work.
- Undo rate. What percentage of resolutions are undone? If >5%, gestures are misfiring or commits are happening too fast.
- Briefing useful rate. Once briefings are in, what percentage are archived (useful) vs dismissed (not useful)? Target >70% useful. Below that means briefings are noise.

For later (if rolling out to other users):

- Time from decision creation to resolution (p50, p95)
- Per-user retention after 2 weeks
- Rate of decisions created by Kit (health of the agent-side output)

## Out of scope (explicit)

- Voice input and output
- Native iOS or Android app
- Widgets, lock-screen, Siri Shortcuts, Google Assistant
- Multi-tenant admin UI for managing decision types
- Analytics dashboard for Kit operators
- Scheduled briefings as a first-class concept (can be implemented as normal scheduled tasks that call `create_briefing`)
- Customer-facing variant of the stack (future, probably a separate surface)
- Desktop-optimized UI (the app should work on desktop but v1 is mobile-first)

## Appendix: example decision and briefing

### Example decision

```json
{
  "title": "Reorder Moonbeam IPA base hops?",
  "context": "Arryved shows Moonbeam IPA at 2 kegs left. Last week you ran 4 kegs. At current pace you'll be out by Thursday. Yakima Chief has Citra and Mosaic in stock at last month's price. Draft order ready.",
  "options": [
    {"id": "send", "label": "Send order to Yakima Chief", "action_spec": {"tool_call": {"name": "send_po", "args": {"vendor": "yakima_chief", "items": [...]}}}},
    {"id": "edit", "label": "Edit before sending", "action_spec": {"custom": "open order draft for user edit"}},
    {"id": "skip", "label": "Skip — I'll handle it", "action_spec": {"noop": true}}
  ],
  "recommended_option_id": "send",
  "priority": "medium"
}
```

### Example briefing

```json
{
  "title": "Big night last night — highest Thursday in 6 months",
  "body": "Last night closed at $4,820, vs. your Thursday average of $2,950. Biggest sellers: Moonbeam IPA (14 kegs-worth), Night Shift Porter (6 kegs). Foot traffic was +45%. Possible drivers: the hockey game (overtime, went late), the pub crawl that passed through at 8pm, or both.",
  "severity": "notable",
  "sources": [
    {"label": "Arryved sales report", "url": "https://..."},
    {"label": "Friday morning Slack thread", "url": "https://slack.com/..."}
  ],
  "related_decision_ids": [
    "dec_restock_moonbeam",
    "dec_schedule_next_pub_crawl_partnership"
  ]
}
```

---

*End of PRD v1.*
