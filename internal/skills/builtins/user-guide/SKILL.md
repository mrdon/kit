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

## Managing Roles and Access

Kit uses roles to control who sees what. Create roles, assign users, then scope skills, rules, and tasks to those roles.

> "Create a role called managers."
> "Assign @jane to managers."

Anything scoped to a role is only visible to members of that role. Anything scoped to "tenant" is visible to everyone. Items with no scopes are invisible (default deny).

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

Todos support priorities, due dates, role scoping, and an activity log. Use `list_todos` to see open items or `complete_todo` to mark one done.

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

Create from an agent context (Slack, MCP, or a skill):

> "Create a decision to reorder Moonbeam hops with options: send the draft order, edit first, or skip."
> "Create a briefing about last night's sales — highest Thursday in 6 months."

Via MCP: `create_decision`, `create_briefing`, `update_decision`, `update_briefing`, `list_decisions`, `list_briefings`, `resolve_decision`, `ack_briefing`. Cards are scoped like other Kit resources — role, user, or tenant-wide.
