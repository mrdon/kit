---
name: user-guide
description: "How to use Kit — adding skills, creating rules, scheduling tasks, managing roles, and searching your knowledge base."
---

# Kit User Guide

Kit is your team's knowledge base and automation assistant. It stores skills (knowledge articles), enforces rules (agent behavior), runs scheduled jobs, and answers questions — all accessible from Slack or any MCP-compatible AI tool.

## Adding a Skill

Skills are reusable pieces of knowledge — procedures, policies, FAQs, or anything your team needs to reference.

**In Slack**, just describe what you want to save:

> "Create a skill called closing-checklist with our end-of-day steps: lock the front door, run the register report, and set the alarm."

You can also upload files directly — PDFs, Word docs, markdown, or ZIP archives. Kit reads them and creates skills automatically.

**Via MCP**, use the `create_skill` tool with a name, description, and content (markdown).

Before authoring or summarizing a skill, consult the `creating-skills` skill — it covers how to pick a name, write a description that the bot can find, and keep content dense enough for the chat agent to give short, useful answers.

By default, skills are visible to everyone. You can scope them to a specific role when creating:

> "Create a skill called payroll-process scoped to managers."

## Adding a Rule

Rules tell Kit's AI how to behave — tone, policies, guardrails. Think of them as standing instructions that shape every response.

**In Slack:**

> "Add a rule: always suggest checking the employee handbook before answering HR questions."

**Via MCP**, use `create_rule` with the rule content and an optional priority (lower number = higher priority).

Rules can be scoped to specific roles so different teams get different behavior.

## Scheduling a Task

Tasks let Kit do things on a schedule — daily summaries, weekly reminders, recurring reports. Just describe when in plain language:

> "Every weekday at 9am, post a morning briefing to this channel."
> "Every Monday at 8am, remind the team about the weekly standup."
> "Tomorrow at 3pm, send me the sales report."

**Via MCP**, use `create_job` with a description and schedule.

For non-trivial tasks — especially those posting to public channels, pinning a specific argument, or needing forced approval gates before the agent can act — consult the `creating-jobs` skill. It covers cron vs one-time schedules, scope, writing a description the scheduled agent can execute, and designing the optional `policy` block that constrains the agent at fire time.

## Managing Roles and Access

Kit uses roles to control who sees what. Create roles, assign users, then scope skills, rules, and tasks to those roles.

> "Create a role called managers."
> "Assign @jane to managers."

Anything scoped to a role is only visible to members of that role. Anything scoped to "tenant" is visible to everyone. Items with no scopes are invisible (default deny).

## Connecting External Tools via MCP

Each Slack workspace has its own MCP endpoint URL of the form `{base-url}/{workspace-slug}/mcp`. You only need your workspace's slug to configure Claude Code, Cursor, or any other MCP client.

- Right after you install Kit, Kit DMs you the exact URL. If you kept that message, copy-paste it into your client.
- Lost it? Message Kit in a DM: *"what's my MCP URL?"* and Kit will repeat it.
- Or open the web console's **Connect AI tools** page (`/{workspace-slug}/web/connect`) — it shows your workspace's endpoint plus copy-paste setup for Claude Code and Cursor.

If you belong to more than one Slack workspace with Kit installed, add one MCP entry per workspace — each URL binds its access token to exactly one workspace, and signing into the wrong one during the OAuth handshake returns a clear error rather than silently issuing a token against the wrong tenant.

## Searching Your Knowledge Base

**In Slack**, just ask a question — Kit automatically searches relevant skills and memories to answer:

> "What's our return policy?"
> "How do I close out the register?"

**Via MCP**, use `search_skills` with a query for full-text search, or `list_skills` to browse everything you have access to.

## Memories

Kit remembers important facts from conversations. These are short-lived, contextual notes that help Kit give better answers over time. You can also explicitly ask Kit to remember something:

> "Remember that our holiday hours start December 20th."

Use `search_memories` to find previously saved facts, or `forget_memory` to remove one.

## Tasks

Kit tracks tasks for your team. Create them from conversation or explicitly:

> "Create a todo to restock the paper towels, assign it to me."
> "What tasks are overdue?"

Tasks support priorities, due dates, role scoping, and an activity log. Use `list_tasks` to see open items or `complete_task` to mark one done. Use `snooze_task` (with `days` = any value 1–365; common picks are 1, 3, 7, 14, 30) to hide a todo from your swipe feed temporarily while keeping it active. To delete, set `status` to `cancelled` via `update_task` — it's a soft delete, recoverable by an admin via the DB if done accidentally.

## Expense reports

Kit files and routes expense reports. A report is a titled group of line items — each line is one receipt (vendor, date, amount, optional tax). A report belongs to a role (the team that owns the spend); anyone in the role can see it, and the submitter (or an admin) edits it while it's a draft.

Attach a receipt photo or PDF in chat and ask Kit to log it:

> "Here's my receipt from the hardware store — start an expense report."
> "Add this $42 lunch to my June expenses."

Kit reads the receipt with `read_attachment` (vision OCR), pulls out the vendor/date/amount, and adds a line item. When you're done, submit the report for approval:

> "Submit my June expenses."

Lifecycle: **draft → submitted → approved / rejected → reimbursed**. On submit, Kit raises an approval **decision card**. **Who approves is an admin setting** — in the web console's Expenses → Settings, an admin picks the approver role (e.g. "managers", "board"); until then, admins approve. Only the configured approvers (and admins) can approve, and they're also the only people besides the submitter who can see a report. Approvers act via the card, MCP/agent tools, or the web console — you can't approve your own. A rejected report can be reopened, fixed, and resubmitted; an approved one can be marked reimbursed. Spend categories are assigned automatically — no need to fill them in.

**Public receipt intake.** Admins can enable a public page (Expenses → Settings) so people *without a Slack account* — volunteers, occasional helpers — can submit an expense. They open the workspace link, upload a receipt (Kit reads the vendor/date/amount for them to check), add their email, and submit. It lands as a normal submitted report owned by the role the admin chose, routed to that role for approval. Nothing is reimbursed until someone approves it, so the page is safe to share openly.

Tools: `create_expense_report`, `add_expense_item`, `update_expense_item`, `remove_expense_item`, `assign_expense_approver`, `submit_expense_report`, `approve_expense_report`, `reject_expense_report`, `mark_expense_reimbursed`, `reopen_expense_report`, `delete_expense_report` (draft/rejected only), `list_expense_reports`, `get_expense_report`, `add_expense_comment`.

## Web console

For deliberate, do-it-yourself work on a desktop, open the web console at `/<your-slug>/web`. It's a direct-manipulation UI — distinct from Slack (ask the agent) and the swipe feed (mobile triage). The launcher links to:

- **Tasks** — a priority-banded list (Blocker / High / Normal) grouped by an auto-assigned topic category. Drag a task between bands to reprioritize, tap "I'm on it" to claim it (which reserves it so teammates skip it) or the checkbox to resolve. Also a flat List view, a detail drawer, and the comment timeline. The same tasks still surface in the swipe feed.
- **Expenses** — reports grouped by status; open one to add or remove line items, assign an approver, and submit/approve/reject/reimburse. Receipts attached in chat show on their line items.
- **Kiosk** — wall-mounted screens: give each one a permanent Kit address, then change what it shows by editing its URL here. Any member can repoint a screen.
- **Vault** — the shared-password vault (set up, unlock, add, reveal, rotate), end-to-end encrypted in your browser.
- **Skills** — browse the knowledge base, search, and open a skill to read it. Admins can create, edit (name, description, content), delete, and attach files; built-in skills show read-only. Everyone sees only the skills their roles can.
- **Jobs** — your scheduled work: each row shows its schedule, status, linked skill, last run, and any error. Open one to edit its description, change or clear the linked skill, adjust the capability policy, or delete it. You see your own jobs plus role/tenant ones; admins see and manage every job in the workspace. Create new jobs by asking Kit in chat.
- **Apps** — admin-only page to turn features (vault, expenses, voting, calendar, app builder, and so on) on or off for the whole workspace. Disabling a feature removes it everywhere — its tools, pages, cards, and the agent's knowledge of it — for everyone, until an admin turns it back on. Only user-facing features appear here; core plumbing (the console itself, admin tools, file attachments, the card feed, and the integrations registry) is always on.
- **Integrations** — connect external services from one page: click **Connect** and enter the secret on a secure one-time form (it never passes through the assistant). Personal email is self-service for any user; workspace-wide services (Square, Google Calendar, Netlify, GitHub) are admin-only.
- **Website / Chat widget** — admin-only setup pages for Netlify/GitHub site changes and the website chat widget.

It works alongside Slack and the feed, not instead of them.

## Slack Channel Search

Admins can configure Slack channels for Kit to search. Once configured, Kit can read and search messages in those channels:

> "Look in #general for any action items from today."
> "Search #ops for mentions of the deploy."

Channels are scoped to roles, so users only see messages from channels they have access to. Use `list_slack_channels` to see available channels.

## Calendars

Admins can plug in any public iCal (`.ics`) URL — a Google Calendar share link, a band tour calendar, a brewery shift schedule — and Kit will keep it in sync and answer questions about the events on it.

> "Configure a calendar called shifts using https://example.com/shifts.ics"
> "Configure a calendar called festivals at https://band.example.org/calendar.ics scoped to parents."

Once configured, just ask:

> "Who's working tonight?"
> "When is the next festival?"
> "Anything on the calendar this Saturday?"

Calendars are scoped to roles like other Kit resources. Use `list_calendars` to see what's configured (and the last sync status), and `get_calendar_events` for date or keyword queries. Kit re-fetches each calendar in the background, so changes on the source feed show up automatically.

## Square shift sync

If you run staff scheduling in Square, Kit can mirror your **published** schedule into a Google Calendar your team already subscribes to — so shifts show up in everyone's calendar automatically. It's admin-only setup and then runs itself (a background sync every 15 minutes).

Two one-time connections, both on the **Integrations** page in the web console (click **Connect** on each):

- **Square** — paste a Square Production Access Token (Developer Dashboard → Credentials; scopes `TIMECARDS_READ`, `EMPLOYEES_READ`, `MERCHANT_PROFILE_READ`). Leave the refresh token blank — a Production token doesn't expire. Once connected, `square_list_shifts` lists the upcoming published schedule so you can confirm the pull.
- **Google Calendar** — create a Google service account, download its JSON key, and **share your target calendar with the service account's email as a writer**. Paste the key + the calendar's ID. Then `gcal_check` writes and deletes a probe event to confirm write access. (A service account has no per-seat cost and needs no admin domain setup.)

Enable the **Square Shift Sync** feature on the Apps page and it starts syncing. Each published shift becomes an **all-day** calendar event on the shift's date, titled with the team member's first name and shift hours (e.g. "Alice · 6:00am–2:00pm") so it stays unobtrusive when layered over a personal calendar while still showing who opens and closes; cancelled shifts are removed on the next sweep. Run `squareshifts_sync_now` to sync on demand and `squareshifts_status` to see the last run. If the calendar has drifted — someone deleted synced events by hand, or stale ones linger — `squareshifts_reconcile` repairs it against Square; pass `dry_run: true` first to see exactly what it would add or remove before it touches anything. Kit only reads Square's *published* schedule — it doesn't build schedules (Square's API doesn't expose staff availability or time-off).

## Events

Enter an event once and Kit fans it out: it lands on the team's Google Calendar, and public events also reach the website. Anyone in the workspace can author events; the calendar and website setup is admin-only.

Create one from chat, or from the **Events** page in the web console:

> "Create an event for Trivia Night on Tuesday 14th at 7pm, repeating weekly"
>
> "Supper Club on Sept 4th, Oct 2nd and Nov 6th, 6pm, public"
>
> "Copy the Beer School event to January 8th"
> "Add a private booking for Sarah's 40th, Saturday 6-9pm in the back room, about 30 people"
> "We're pouring at the Denver Beer Fest on the 22nd — that's offsite and public"

**Two things are separate, and it matters.** *Status* is whether an event is settled: draft → published → cancelled. *Visibility* is whether the outside world may see it: public or private. So a confirmed private booking is **published and private** — on the team calendar the moment it's booked, and never on the website. Publishing does not make something public.

Events start **private**, so nothing reaches the website unless someone says it should. A draft appears nowhere at all — not on the calendar, not on the site — so you can rewrite it as many times as you like before anyone sees it.

Three things describe an event, and they're independent rather than one "type":

- **visibility** — `public` (website + feed) or `private` (internal only)
- **venue** — `onsite`, or `offsite` for a festival you're attending. An offsite event can still be public: "come see us there" belongs on the site.
- **space impact** — whether it reserves part of the room, so whoever's working knows

**Staff notes** go on the calendar entry, where the bartender working that night is already looking. They never appear on the website.

**Repeats come in three shapes**, and picking the right one means one web page instead of five:

- **Every week** — a standing night like trivia, same weekday as the start date.
- **Every month** — "the first Friday", "the last Friday", or a day of the month like the 15th. Kit offers only the patterns that actually match your start date. A month with no such date is skipped rather than moved, so a series on the 31st simply doesn't run in February.
- **On set dates** — an add-and-remove list of dates for a series that follows no pattern: a supper club scheduled around the chef, a five-week course with a gap over a holiday. One event on several dates means one web page, one poster and one set of staff notes.

The exception is a run of genuinely *different* events — live music with a different band each week. Those each have their own name, description and poster, so they stay separate events.

**Duplicate instead of retyping.** The **Duplicate** button on an event (or "copy that event to the 12th" in chat) makes a new draft with the same blurb, staff notes, price, capacity and poster. The copy is independent — editing one never changes the other — and it gets its own web address. Give it a new date and it becomes a one-off on that date; leave the date alone and it duplicates the schedule exactly.

**Cancel rather than delete.** Cancelling removes an event from the calendar and the website but keeps the record, so the calendar entry gets cleaned up and the web address is never reused for different content. Web addresses are frozen once an event is published, because links to them may already be in a social post or newsletter.

### The table topper

The cog menu on the Events page prints the week's card for the taproom tables — **Table topper — this week** or **next week** — a coloured band per event with the day, the door time, a couple of lines about it, and the event's own poster. It's a PDF, two identical cards to a landscape sheet with a cut line down the middle, so one sheet covers two tables.

Nothing to lay out: it's built from the events you already entered, and only from **published, public** ones — a private booking never appears on a card sitting in front of customers. The bullets come from the event's description (one per line if you wrote it that way) or from its summary. Repeating events show the date they land on *this* week, so a weekly quiz prints with this Wednesday on it.

Pick **next week** on a Friday and the card is ready before the weekend. Seven events is the most that fits on one card; anything beyond that is counted at the bottom rather than dropped silently.

Admin setup, on the **Events** page under Admin:

- **Pick the calendar.** Connect Google Calendar on the Integrations page first (a service account — see Square shift sync below for the same setup), then share your events calendar with the service account's email and choose it from the dropdown. If the dropdown is empty, the calendar hasn't been shared with the service account yet.
- **Set the website URL pattern**, e.g. `https://www.example.com/events/{slug}`. Each event's public link is built from this, so changing your domain later doesn't mean rewriting past events.
- **Copy the feed URL and token** into your website's build. The site fetches published public events from it and generates a page for each.
- **Paste a build hook URL** (in Netlify: Site configuration → Build & deploy → Build hooks → Add build hook) if you want the website to rebuild itself. With one set, Kit publishes overnight at 2am whenever something is waiting to go out — so an event you add on Tuesday afternoon is live by Wednesday morning without anyone pressing anything. Nights where nothing changed are skipped, so it doesn't burn build minutes rebuilding an identical site. The **Publish** button on the Events page still rebuilds on demand when you don't want to wait.

Kit syncs to the calendar every 15 minutes. Use **Sync now** to push immediately, and **Check for drift** if the calendar has got out of step — someone deleted an entry by hand, say. That shows exactly what it would change before touching anything, and only ever touches entries Kit created.

### Telling staff what's on when they work

If you run scheduling in Square, Kit can DM everyone working each morning with what's on that day — private bookings included, so nobody sets the room five minutes before thirty people arrive. One message per person listing the whole day, at 8am in your venue's timezone.

Set it up on the **Event staff notices** page under Admin. Square and Kit know people by different ids and nothing links them, so you pair them once by hand: each person on your published Square schedule gets a dropdown of your Slack members. Pick their account and you're done. Anyone left on "Nobody" simply gets no notices, and the page says how many people that is — staff who work without hearing what's on are the thing worth noticing.

Press **Preview** to see the exact messages before anyone gets one. **Send now** delivers today's; pressing it twice is safe, because a notice already delivered unchanged isn't repeated. If the day's plan genuinely changes after the morning send, the next run picks up the difference and follows up.

New hire? They appear in the dropdown once they're on the published Square schedule. You don't need them to have used Kit before — picking them creates their Kit account.

## Email

Connect any IMAP + SMTP mailbox so Kit can read your inbox and draft replies on your behalf. Gmail works via an app password (enable 2FA, then generate one at https://myaccount.google.com/apppasswords). iCloud, Yahoo, Fastmail, and self-hosted IMAP work with their normal passwords. Microsoft 365 / Outlook.com aren't supported yet — they require OAuth.

Connect it on the **Integrations** page in the web console: find the "Email — personal" connector and click **Connect**. You enter your password on a secure one-time form in the browser — it never passes through the assistant. (Email is self-service: any user can connect their own mailbox.)

Once configured, ask:

> "Any emails from Jim this week?"
> "Read uid 12345."
> "Draft a reply to that last email thanking them and proposing Thursday at 1pm."

**Sends always go through an approval card.** When you ask Kit to send email, the drafted message appears in your card stack — you can review it, long-press to revise ("make it more formal, drop the last paragraph"), and swipe to approve. Kit never sends directly. The body is markdown; the recipient's mail client sees both a rich-HTML and plain-text version.

Tools: `search_emails`, `read_email`, `mark_read`, `send_email` (agent-side only — `send_email` is not exposed via MCP because it's gated).

## Meeting scheduling

When you need to find a time that works for several people, ask Kit to set up the meeting. Kit DMs each participant on Slack, asks for availability in plain language, negotiates across rounds if there's no immediate overlap, and surfaces a confirmation card once everyone aligns.

> "Set up a 30-minute meeting with @alice and @bob next week. Tue or Wed afternoon work for me."

The organizer's availability is implicit (it's expressed via the candidate slots Kit generates from your message); the participants list is "who to DM". For a 1:1 between you and Alice, the participants list is just `["@alice"]`.

**What happens after you start it:**

- Kit drafts each outbound DM and shows you an approval card per draft so you can edit or skip before it sends. Flip on `auto_approve` to bypass the cards if you trust the drafts.
- Kit nudges non-responders on its own schedule (24h, then 24h, then times them out).
- Each reply runs through a parser that extracts free-form availability ("Wed 4pm only", "anytime after Tuesday", "Fri or Sat morning"). Kit re-proposes times to anyone whose answer doesn't intersect cleanly.
- When everyone agrees on a time, Kit surfaces a confirmation card. Tap to confirm — Kit notifies everyone (you still send the calendar invite yourself for now).
- If the deadline lapses or rounds run out without convergence, Kit surfaces an abandonment card with options to extend by 7 days or abandon.

Tools: `start_coordination`, `list_coordinations`, `get_coordination`, `cancel_coordination`. Don't hand-DM participants for scheduling — `start_coordination` is the only path that wires up approvals, parsing, and convergence.

## Group votes

When you need quorum approval from a named stakeholder list — board sign-off, partner agreement, "do we all agree on this" — use a vote. Unlike meeting scheduling, no Slack DMs go out: each participant sees a decision card in their card stack and swipes Approve / Object or taps Abstain. Long-press the card to attach a private comment ("happy to approve if we drop the per-seat clause").

> "Start a vote on the new vendor agreement with @alice, @bob, and @carol."

Once everyone resolves or the deadline hits, the organizer sees a digest card with the tally and verbatim objection reasons. Options:

- **Accept** / **Reject** — record the decision silently.
- **Accept-and-share-with-team** / **Reject-and-announce** — broadcast a sanitized briefing card to each participant so the group sees the outcome.

Votes are one-shot. If objections come in, you decide what to do — there's no automatic compromise round. Don't use this for meeting times (use `start_coordination`) or casual channel polls (Slack's built-in poll is better).

Tools: `start_vote`, `list_votes`, `get_vote`, `cancel_vote`.

## Vault (shared passwords)

Kit's vault stores team logins — POS accounts, SaaS dashboards, Mailchimp, Squarespace, anything where a small group needs to share one set of credentials. Values are encrypted in the browser before they leave the user's device; Kit and the LLM never see plaintext.

The vault is locked behind **one shared master password per workspace** — the same model your members-only website probably uses. Anyone on your team who knows the password can unlock the vault; share it out-of-band the same way you share that website password. Each entry is also scoped to one role, so role membership decides who sees which entries even after unlocking. The "member" role includes everyone in the workspace; smaller roles like `managers` or `kitchen-staff` restrict visibility.

**First-time setup.** An admin opens the vault at `/{workspace-slug}/web/vault`; with no vault yet, the page offers a setup card to pick a master password (Kit suggests a strong passphrase — accept it or type your own). Copy it, share it with at least one teammate via your usual out-of-band channel, and write it down somewhere safe. **Kit cannot recover this password if it's forgotten by everyone.**

**Unlocking.** Every user unlocks with the same shared password at `/{workspace-slug}/web/vault` — the page prompts for it the first time you take an action that needs the key. Unlock lasts for the browser session (idle-times out after 10 minutes of no activity, hard-locks after 30 minutes total).

**Saving a secret.** Ask Kit, and Kit hands back a URL to a browser form — you type the password there, the browser encrypts it client-side, and the ciphertext is what hits the server. Kit will not accept a password pasted into chat:

> "Save the password for our Squarespace, scoped to managers."

**Using a secret.** Ask Kit by name; Kit returns a one-tap URL that opens the reveal page in your browser (you'll be prompted for the shared master password if your session has timed out):

> "What's the login for our POS?"
> "Find the Mailchimp password."

**Browsing.** The vault web UI at `/{workspace-slug}/web/vault` shows every entry you can see, with filters and an add button. Tools work the same: `list_secrets` (optionally `role_id`-filtered), `find_secret` for fuzzy lookup by name, `view_secret` for a reveal URL, `start_add_secret` for the add URL.

**Re-scoping or deleting.** `set_secret_role` changes which role owns an entry; `delete_secret` removes it (no undo — recoverable only by re-adding from another source). Both are gated through a confirmation card before they take effect.

**Rotating the password.** When you need to change the shared password (e.g. after an employee departure), an admin opens `/{workspace-slug}/web/vault` and uses **Rotate password**. Rotation requires the **old** password — the browser unwraps the existing vault key under it and re-wraps under the new one, so every stored secret keeps working. After rotation, share the new password out-of-band; open tabs on other devices re-lock automatically.

**If the master password is lost.** There is no recovery — the master password is the only thing that can decrypt the vault. If nobody remembers it, an admin can open `/{workspace-slug}/web/vault` and use **Destroy vault** to permanently delete every stored secret and start over with a fresh setup. It makes you re-type the workspace slug as a confirmation gate; there is no undo. The agent tool `nuke_vault` and MCP tool of the same name return this URL but never run the destruction themselves.

## Decisions and briefings (card stack)

Kit surfaces agent-driven prompts in a swipeable mobile card stack at `{base-url}/{workspace-slug}/` (sign in via Slack at `/{workspace-slug}/login`). Each workspace has its own URL — the system prompt names yours, so just ask Kit "what's my card stack URL?" in a DM. Install it to your home screen: iOS Safari → Share → Add to Home Screen; Android Chrome → ⋮ → Install app.

Two kinds of card:

- **Decisions** — a judgment call with 2-4 options and a recommended default.
  - **Swipe right** → approve the recommended option
  - **Tap** → open the detail view to pick any option
  - If the chosen option has a prompt, Kit queues a one-shot agent task that runs it (e.g. posts to a channel, sends a DM, calls a tool).
- **Briefings** — informational updates, usually recaps or alerts.
  - **Swipe right (👍)** → useful; archive it
  - **Swipe left (👎)** → not useful; dismiss it
  - **Tap** → open the detail view

Both thumbs up and thumbs down are recorded on the card (terminal state + timestamp + user), so the signal is available if you want to tune future briefings toward what's actually useful.

**Chat with a card.** Long-press any card (about 600ms) to open a chat panel bound to that card. Type a message or hold the mic button to talk — both land in the same conversation. Use it to modify, reschedule, or ask about the card without switching back to Slack. Follow-up messages attach to the same session, so you can say "make it high priority" and then "no, actually low" and Kit understands the correction. The panel stays open until you close it.

**Quick chat (the + button).** Tap the floating "+" button in the bottom-right of the feed for fast capture without a card in mind — "add a todo to buy milk", "remind me to call Pat tomorrow", "what decisions are open?". After the agent acts (creates a todo, etc.) the panel closes itself after about a second; tap the panel during that hold to keep it open if you want to correct. Questions, clarifications, and approvals keep the panel open automatically. Each open starts a fresh conversation — close and reopen for a clean slate.

Voice is optional — the mic button only appears in browsers with `MediaRecorder` support (Chrome/Firefox/Edge/Safari 14.5+; Firefox on mobile falls back to typed-only). Admin setup for transcription is documented in `CLAUDE.md`.

Create from an agent context (Slack, MCP, or a skill):

> "Create a decision to reorder Moonbeam hops with options: send the draft order, edit first, or skip."
> "Create a briefing about last night's sales — highest Thursday in 6 months."

Via MCP: `create_decision`, `create_briefing`, `update_decision`, `update_briefing`, `list_decisions`, `list_briefings`, `resolve_decision`, `ack_briefing`. Cards are scoped like other Kit resources — role, user, or tenant-wide.

## Website chat widget

You can embed Kit as a floating chat bubble on your own website (e.g. a Wix members-only page). Visitors click the bubble, ask a question, and get an answer drawn from your tenant's skills, rules, memories, and calendar. The widget is strictly read-only — visitors can't create tasks, save memories, or trigger any action. Kit-internal builtin skills (this user guide, etc.) and any skill wired into a scheduled job are filtered out automatically, so only your customer-facing knowledge surfaces.

**Setup (admin, one-time):**

> "Create a widget token for https://example.com"

Kit returns a token (shown once — save it) and a one-line `<script>` snippet. Paste the snippet into your site's custom-HTML block; the bubble appears in the bottom-right and survives client-side navigation.

**Manage tokens** with `list_widget_tokens` and `revoke_widget_token` (revoke stops the embed working immediately).

**Review conversations** any member of the workspace can ask:

> "What questions did people ask the widget this week?"
> "Did anyone ask about band camp?"
> "Show me conversations that didn't get a good answer."

Kit calls `list_widget_conversations`, `search_widget_conversations`, and `read_widget_conversation` to answer. Visitor identity is anonymous (a per-browser UUID), so cross-conversation grouping works without storing personal data.

## Kiosk screens

For screens that just sit on a wall running a browser — a lobby TV, a shop-floor dashboard — Kit gives each one a permanent address so you never have to walk over with a keyboard to change what it shows.

Set up a **board** per screen on the **Kiosk** page (`/<your-slug>/web/kiosk`): a name, an address key, and the URL it should display. The screen's address is `https://<your-kit>/<your-slug>/kiosk/<key>` — open that once in the kiosk's browser and it redirects to whatever the board currently points at. Later, change the URL on the page and the screen follows.

A screen only picks up changes on its own if something on the machine is watching. The setup panel on the page has a copy-paste shell loop that asks the board where to point every 30 seconds and reloads the browser when the answer changes. Without it, the screen shows whatever it loaded at boot until someone reloads it.

Each board shows **Live** once a machine is polling it, so a screen that has gone dark is visible from the page rather than from someone walking past it. A board with no URL yet shows a plain "no content assigned" card on the screen instead of an error.

Board addresses are **public and unauthenticated** — that's what lets a machine with no login use them. Anyone who knows the address can see where the screen points, so don't send a screen to a URL that is itself a private link.
