---
name: Kit Vision
description: High-level vision document for Kit - a multi-tenant, role-based AI agent platform for small businesses, delivered as SaaS with Slack as the primary interface.
---

# Kit Vision

## What Kit Is

Kit is a general-purpose, role-based AI agent for small businesses. One agent per organization, transparently supporting multiple roles with different levels of access to data and integrations. It meets users where they already are — Slack initially, SMS and other channels later.

Kit is not a chatbot. It is an always-on team member that responds to chat, runs scheduled tasks (reports, reminders), and reacts to triggers (new email, calendar changes). It understands who is talking to it, what they're allowed to see, and what tools they can use.

## The Problem

Small businesses and nonprofits have the same operational needs as larger companies — shift scheduling, sales tracking, financial reporting, task management — but lack the resources for dedicated software or staff for each function. They end up with fragmented tools, manual processes, and tribal knowledge that lives in people's heads.

AI agents today are either consumer-focused (single user, personal assistant) or enterprise-focused (complex setup, heavy infrastructure). There is a gap for small orgs that want a smart, capable team member without a DevOps team to run it.

## Core Principles

- **One agent, many roles.** A bartender checking the shift schedule and a manager reviewing financials talk to the same agent. Kit knows who they are and scopes its responses and capabilities accordingly.
- **Chat-first UX.** No dashboards to learn. No apps to install beyond Slack. If you can send a message, you can use Kit.
- **Meet them where they are.** Slack is the primary interface for MVP. SMS, email, and other channels come later. The agent adapts to the channel, not the other way around.
- **Chat-driven setup.** Onboarding, role configuration, and integration setup happen through conversation with the business owner. No web UI required for MVP.
- **Proactive, not just reactive.** Kit doesn't wait to be asked. It sends shift reminders, flags unusual metrics, follows up on overdue tasks, and delivers scheduled reports.
- **Transparent role boundaries.** Users understand what they can and can't access. Kit never leaks data across role boundaries, and it's clear about why it can't fulfill a request if permissions don't allow it.

## Example Use Cases

### Brewery / Bar
- A **bartender** asks about their shift schedule, requests a swap, or checks what's on tap
- A **sales rep** logs a customer visit, asks what accounts need follow-up, or checks sales numbers
- A **manager** gets a daily sales summary, is notified when inventory is low, or reviews staff schedules
- The **owner** sets up roles, connects integrations, and monitors key business metrics

### Marching Band / Nonprofit
- A **band parent** asks about upcoming festivals, practice schedules, or volunteer needs
- A **fundraising manager** tracks campaign progress, gets reminders on donor follow-ups, and logs pledges
- A **board member** reviews detailed financials, approves budget changes, or automates reporting workflows

### General Pattern
Any organization with distinct roles that need different views of shared data and different levels of access to external systems.

## Architecture Overview

### Hosting & Deployment
- **Multi-tenant SaaS** — hosted and managed centrally. A new org is a new tenant, not a new deployment.
- **No local infrastructure required** — the business installs a Slack app and talks to Kit. That's it.
- **Trade-off acknowledged:** SaaS means no filesystem or shell access for the agent. This limits some power-user scenarios but drastically simplifies onboarding and operations.

### Tech Stack
- **Language:** Go — chosen for speed, low resource usage, concurrency, and LLM-friendliness. Accepts the trade-off of a smaller library ecosystem.
- **LLM:** Claude (Haiku for MVP) — balances speed and cost. Architecture should allow swapping models later.
- **Primary interface:** Slack (via Slack app install per org)
- **Future channels:** SMS, email, other chat platforms

### Agent Model
Kit runs as a single logical agent per organization with role-aware behavior:

```
Slack Message → Kit Platform → Role Resolution → Context Assembly → LLM → Tool Execution → Response
```

- **Role resolution:** Identify the user, determine their role(s) and permissions
- **Context assembly:** Load relevant knowledge, conversation history, and available tools scoped to the role
- **Tool execution:** Only tools authorized for the role are available to the LLM
- **Response:** Delivered back through the originating channel

### Three Modes of Operation

1. **Chat (reactive):** User sends a message, Kit responds with role-appropriate context and tool access
2. **Cron (scheduled):** Recurring tasks — daily reports, weekly summaries, shift reminders, metric checks
3. **Triggers (event-driven):** React to external events — new email, calendar update, form submission, webhook

### Knowledge & Context

- **Obsidian-style vault** as the knowledge layer — plain markdown files with YAML frontmatter for metadata and role tagging. Human-readable and editable, stored per-tenant.
- **Derived vector index** for semantic search — the agent queries the index, not the raw files, enabling fast retrieval across large knowledge bases.
- **Frontmatter-based access control:** Notes are tagged with roles/departments, and the platform filters what the agent can see based on the current user's role.

```yaml
---
title: Q1 Financial Summary
roles: [owner, board_member]
department: finance
---
```

- **Skills** for workflow and document/context retrieval — reusable instruction sets loaded on demand, keeping the base context lean.

### Integrations
Connected via tools and MCP where applicable. Initial targets:

| Integration | Purpose | Example Roles |
|---|---|---|
| Google Sheets | Lightweight CRM, data tracking | Sales, Manager |
| Google Calendar | Shifts, events, scheduling | All |
| Email (Gmail) | Monitoring, sending, triggers | Manager, Owner |
| Slack | Primary chat interface | All |
| POS system | Sales data, transactions | Manager, Owner |
| QuickBooks | Financial data, invoicing | Owner, Board |

### Role & Permission Model
- Roles are defined per-org by the owner through chat conversation
- Each role specifies: accessible knowledge tags, available tools/integrations, and data scopes
- Permissions are enforced at the platform layer before context reaches the LLM
- The LLM never sees data the user's role doesn't permit

### Onboarding Flow (Chat-Driven)
1. Business owner installs Kit Slack app
2. Kit introduces itself and asks about the business
3. Owner describes roles and who fills them (maps Slack users to roles)
4. Kit walks through connecting integrations one by one
5. Kit confirms setup and begins operating

## Inspiration

Kit draws architectural inspiration from **OpenClaw**, an open-source local-first AI agent platform:
- **Gateway architecture** separating message routing from agent reasoning
- **File-based persona/behavior definition** — behavior changes without code changes
- **On-demand skill loading** — only relevant capabilities per turn, preventing context bloat
- **Session-based security boundaries** for role isolation
- **Proactive heartbeat pattern** — scheduled self-directed action
- **Layered tool policies** — fine-grained per-role tool access

Kit diverges from OpenClaw by being SaaS-hosted, multi-tenant, and focused specifically on the small business use case rather than personal assistant use.

## What Kit Is Not (For Now)

- **Not a local agent.** No filesystem access, no shell commands. The trade-off for easy SaaS deployment.
- **Not a UI platform.** No dashboards, no web app. Chat is the interface.
- **Not multi-model.** Claude Haiku for MVP. Model flexibility comes later.
- **Not open source.** Built for fun and for specific orgs initially. Licensing decisions come later.
- **Not an enterprise product.** Small businesses and nonprofits. Simplicity over configurability.

## Future Considerations (Not MVP)

- Additional channels (SMS, email-as-interface, Discord)
- Model selection per org or per role (cost vs capability trade-offs)
- Local/self-hosted option for orgs that need it
- Web UI for complex admin tasks if chat-driven setup hits limits
- Agent-to-agent coordination across orgs
- Marketplace for community-built skills and integrations
