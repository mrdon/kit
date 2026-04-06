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
