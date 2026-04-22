---
name: user-guide
description: "How to use Kit — adding skills, creating rules, scheduling tasks, managing roles, and searching your knowledge base."
---

# Kit User Guide

Kit is your team's knowledge base and automation assistant. It stores skills (knowledge articles), enforces rules (agent behavior), runs scheduled tasks, and answers questions — all accessible from Slack or any MCP-compatible AI tool.

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

**Via MCP**, use `create_task` with a description and schedule.

For non-trivial tasks — especially those posting to public channels, pinning a specific argument, or needing forced approval gates before the agent can act — consult the `creating-tasks` skill. It covers cron vs one-time schedules, scope, writing a description the scheduled agent can execute, and designing the optional `policy` block that constrains the agent at fire time.

## Managing Roles and Access

Kit uses roles to control who sees what. Create roles, assign users, then scope skills, rules, and tasks to those roles.

> "Create a role called managers."
> "Assign @jane to managers."

Anything scoped to a role is only visible to members of that role. Anything scoped to "tenant" is visible to everyone. Items with no scopes are invisible (default deny).

## Connecting External Tools via MCP

Each Slack workspace has its own MCP endpoint URL of the form `{base-url}/{workspace-slug}/mcp`. You only need your workspace's slug to configure Claude Code, Cursor, or any other MCP client.

- Right after you install Kit, Kit DMs you the exact URL. If you kept that message, copy-paste it into your client.
- Lost it? Message Kit in a DM: *"what's my MCP URL?"* and Kit will repeat it.

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

## Todos

Kit tracks todos for your team. Create them from conversation or explicitly:

> "Create a todo to restock the paper towels, assign it to me."
> "What todos are overdue?"

Todos support priorities, due dates, role scoping, and an activity log. Use `list_todos` to see open items or `complete_todo` to mark one done. Use `snooze_todo` (with `days` = 1, 3, or 7) to hide a todo from your swipe feed temporarily while keeping it active. To delete, set `status` to `cancelled` via `update_todo` — it's a soft delete, recoverable by an admin via the DB if done accidentally.

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

## Email

Connect any IMAP + SMTP mailbox so Kit can read your inbox and draft replies on your behalf. Gmail works via an app password (enable 2FA, then generate one at https://myaccount.google.com/apppasswords). iCloud, Yahoo, Fastmail, and self-hosted IMAP work with their normal passwords. Microsoft 365 / Outlook.com aren't supported yet — they require OAuth.

Credentials go through the shared integrations flow — Kit mints a one-time URL, you enter your password in a browser form, and the LLM never sees it:

> "Set up my email."

(Kit calls `configure_integration(provider="email", auth_type="imap_smtp")` and relays the URL.)

Once configured, ask:

> "Any emails from Jim this week?"
> "Read uid 12345."
> "Draft a reply to that last email thanking them and proposing Thursday at 1pm."

**Sends always go through an approval card.** When you ask Kit to send email, the drafted message appears in your card stack — you can review it, long-press to revise ("make it more formal, drop the last paragraph"), and swipe to approve. Kit never sends directly. The body is markdown; the recipient's mail client sees both a rich-HTML and plain-text version.

Tools: `search_emails`, `read_email`, `mark_read`, `send_email` (agent-side only — `send_email` is not exposed via MCP because it's gated).

## Decisions and briefings (card stack)

Kit surfaces agent-driven prompts in a swipeable mobile card stack at `/app/` (sign in via Slack). Install it to your home screen: iOS Safari → Share → Add to Home Screen; Android Chrome → ⋮ → Install app.

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

Voice is optional — the mic button only appears in browsers with `MediaRecorder` support (Chrome/Firefox/Edge/Safari 14.5+; Firefox on mobile falls back to typed-only). Admin setup for transcription is documented in `CLAUDE.md`.

Create from an agent context (Slack, MCP, or a skill):

> "Create a decision to reorder Moonbeam hops with options: send the draft order, edit first, or skip."
> "Create a briefing about last night's sales — highest Thursday in 6 months."

Via MCP: `create_decision`, `create_briefing`, `update_decision`, `update_briefing`, `list_decisions`, `list_briefings`, `resolve_decision`, `ack_briefing`. Cards are scoped like other Kit resources — role, user, or tenant-wide.
