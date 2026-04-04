---
name: Kit MVP
description: MVP scope definition for Kit - a role-aware knowledge base agent that answers questions via Slack, with skills engine, rules, and chat-driven onboarding.
---

# Kit MVP

## One-Line Summary

A role-aware knowledge base agent that answers questions via Slack, powered by composable skills and rules.

## What Success Looks Like

A business owner installs the Kit Slack app, tells Kit about their org and roles via chat, uploads some docs, and their team starts asking Kit questions — each person seeing answers scoped to their role. No web UI, no config files, no DevOps.

### Concrete Scenarios (MVP)

**Brewery:**
- Owner installs Kit, describes the business, defines bartender/manager/owner roles, maps Slack users
- Owner uploads the employee handbook PDF and tap room policies doc
- Owner tells Kit some rules: "Bartenders can't see financial info", "We're closed Mondays"
- Bartender DMs Kit: "What's the policy on tabs over $100?" → Kit searches skills, responds
- Manager asks in a channel: "@kit what are our cleaning procedures?" → Kit responds in-thread

**Marching Band:**
- Board member installs Kit, uploads the bylaws and festival schedule
- Parent asks: "When is the next competition?" → Kit finds it in the festival schedule skill
- Fundraising manager asks: "What's our policy on corporate sponsorships?" → Kit finds it in bylaws

## What's In

### 1. Slack App
- Distributed Slack app using Events API (HTTP webhooks)
- Respond 200 OK immediately on webhook, process async (Slack retries after 3s)
- OAuth install flow — org installs, Kit gets `team_id` + `bot_token`, creates tenant (upsert on re-install)
- `@kit` mention trigger in channels, all messages in DMs
- Thread-based sessions (`thread_ts` → session in Postgres)
- Thinking indicator (emoji reaction) while agent runs
- File upload handling (PDF, docx, markdown, zip)

### 2. Multi-Tenant Core
- Tenants, users, roles tables — all with `tenant_id`
- Role resolution on every message: Slack user → tenant → role(s)
- Platform defaults (rules, skills, system prompt) hardcoded in Go — evolve with releases, not DB migrations
- `is_admin` flag on users — installer automatically gets admin. Admins can write (create/update/delete skills, rules, roles). Non-admins are read-only.
- `setup_complete` flag on tenants — until true, non-admin users get "I'm still being set up, ask [installer] for help"
- Unrecognized users (no role assigned) get a user record created on first message, but limited response: "I don't have a role for you yet. Ask your admin to set you up."

### 3. Agent Loop
- Single loop shared across all interaction types
- Observe (Slack message) → Reason → Act (tool call) → Feed Back → Repeat/Stop
- Output always via tool call (`send_slack_message`)
- Configurable iteration limit as guardrail

### 4. Skills Engine
- Skills stored in Postgres (not filesystem) with FTS index
- Progressive disclosure: catalog (name + description) in stable prefix, full content loaded on demand
- Skill references for large content (handbook split into subtopics)
- Scopes via join table: tenant-wide, or restricted to specific roles
- Agent tools: `search_skills(query)`, `load_skill(skill_id)`, `load_reference(ref_id)`

### 5. Rules
- Always-on context injected into stable prefix
- Composable system prompt: platform + tenant + role + task-type rules
- Scopes via join table: multiple roles per rule, task-type scoping
- Created/edited via chat with owner

### 6. Knowledge Ingestion
- **Via chat**: "Kit, our return policy is..." → Kit creates/updates a skill
- **Via file upload**: Owner drops PDF/docx/markdown/zip in Slack → Kit converts to markdown via LLM call → creates skill(s)
- **Via chat edit**: "Kit, update the tap room policies to include..." → Kit modifies existing skill

### 7. Chat-Driven Onboarding
- After OAuth install, Kit DMs the installer
- Asks about the business (name, type, timezone)
- Walks through role creation (what roles, who fills them)
- Prompts for initial knowledge upload
- Confirms setup, begins operating

### 8. Memory
- User-scoped by default, org-scoped only when explicitly requested
- Explicit memory only for MVP: "Kit, remember that the WiFi password is ABC123" (no background extraction)
- Relevant memories retrieved via FTS and injected into prompt alongside rules
- Owner can ask Kit to forget specific memories
- No automated decay or pruning for MVP

### 9. Session Logging
- Every agent invocation logged: model used, tokens in/out, tool calls, skills loaded, duration, errors
- Enables per-tenant cost tracking and debugging ("what did Kit see when it gave that answer?")

### 10. Multi-Model Support
- Haiku for Q&A, onboarding, memory extraction (fast, cheap)
- Sonnet for file ingestion / PDF-to-markdown (needs stronger comprehension)
- Model selected by platform per-task, not by the LLM

### 11. Context Assembly
- Stable prefix: platform rules + tenant rules + role rules + relevant memories + skill catalog + tool descriptions
- Recent messages: verbatim from current thread
- No working memory or transcript summarization for MVP (sessions will be short Q&A)

### 12. Deployment
- Single Go binary, Dockerfile on Dokku (`apps.twdata.org`)
- Postgres 16 (pgvector image, extension not used yet)
- Let's Encrypt TLS
- Environment vars for Slack app credentials and Anthropic API key

## What's Out

| Feature | Why deferred |
|---|---|
| Cron/scheduled tasks | No integrations to report on yet |
| Triggers (email, webhooks) | Requires integration adapters |
| External integrations (Calendar, Sheets, Gmail, POS, QuickBooks) | MVP is knowledge-only |
| pgvector / semantic search | FTS sufficient for small skill sets |
| Background memory extraction | Explicit "remember this" is sufficient for MVP |
| Working memory / transcript summarization | Q&A sessions will be short |
| Approval gates | No dangerous tools in MVP |
| MCP servers per role | No external tools yet |
| Auth tokens per integration | No integrations yet |
| Additional channels (SMS, email, Discord) | Slack only for MVP |
| Per-tenant model selection | Platform picks model per-task for now |
| Web UI | Chat is the interface |

## Data Model (MVP)

```sql
-- Multi-tenant core
tenants (
    id UUID PRIMARY KEY,
    slack_team_id TEXT UNIQUE NOT NULL,
    name TEXT NOT NULL,
    bot_token TEXT NOT NULL,         -- encrypted at application layer
    business_type TEXT,
    timezone TEXT DEFAULT 'UTC',
    setup_complete BOOLEAN DEFAULT false,
    created_at TIMESTAMP
)

users (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants,
    slack_user_id TEXT NOT NULL,
    display_name TEXT,
    is_admin BOOLEAN DEFAULT false,
    created_at TIMESTAMP,
    UNIQUE(tenant_id, slack_user_id)
)

roles (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants,
    name TEXT NOT NULL,
    description TEXT,
    created_at TIMESTAMP,
    UNIQUE(tenant_id, name)
)

user_roles (
    tenant_id UUID NOT NULL REFERENCES tenants,
    user_id UUID NOT NULL REFERENCES users,
    role_id UUID NOT NULL REFERENCES roles,
    PRIMARY KEY(tenant_id, user_id, role_id)
)

-- Skills engine
skills (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants,
    name TEXT NOT NULL,
    description TEXT NOT NULL,
    content TEXT NOT NULL,
    user_invocable BOOLEAN DEFAULT true,
    source TEXT,                      -- 'chat', 'upload'
    created_at TIMESTAMP,
    updated_at TIMESTAMP
)

skill_references (
    id UUID PRIMARY KEY,
    skill_id UUID REFERENCES skills,
    tenant_id UUID NOT NULL REFERENCES tenants,
    filename TEXT NOT NULL,
    content TEXT NOT NULL
)

-- Default deny: a skill with no scope rows is invisible.
skill_scopes (
    tenant_id UUID NOT NULL REFERENCES tenants,
    skill_id UUID NOT NULL REFERENCES skills,
    scope_type TEXT NOT NULL,          -- 'tenant', 'role'
    scope_value TEXT NOT NULL,         -- '*' for tenant-wide, 'bartender' for role
    PRIMARY KEY(tenant_id, skill_id, scope_type, scope_value)
)

-- Rules
rules (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants,
    content TEXT NOT NULL,
    priority INT DEFAULT 0,
    created_at TIMESTAMP,
    updated_at TIMESTAMP
)

-- Default deny: a rule with no scope rows is invisible.
rule_scopes (
    tenant_id UUID NOT NULL REFERENCES tenants,
    rule_id UUID NOT NULL REFERENCES rules,
    scope_type TEXT NOT NULL,          -- 'tenant', 'role', 'task_type'
    scope_value TEXT NOT NULL,         -- '*' for tenant-wide, 'bartender' for role, 'cron' for task_type
    PRIMARY KEY(tenant_id, rule_id, scope_type, scope_value)
)

-- Memory (default deny — must be explicitly scoped)
memories (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants,
    content TEXT NOT NULL,
    scope_type TEXT NOT NULL,           -- 'user', 'role', 'tenant'
    scope_value TEXT NOT NULL,          -- slack_user_id, role name, or '*' for tenant-wide
    source_session_id UUID REFERENCES sessions,
    created_at TIMESTAMP,
    updated_at TIMESTAMP
)

-- Sessions
sessions (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants,
    slack_thread_ts TEXT NOT NULL,
    slack_channel_id TEXT NOT NULL,
    user_id UUID REFERENCES users,
    created_at TIMESTAMP,
    updated_at TIMESTAMP,
    UNIQUE(tenant_id, slack_channel_id, slack_thread_ts)
)

-- Session event log (JSONL-style, append-only)
-- No separate messages table — session_events is the single source of truth.
-- Transcript is reconstructed from message_received + message_sent events.
session_events (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants,
    session_id UUID NOT NULL REFERENCES sessions,
    event_type TEXT NOT NULL,           -- 'message_received', 'llm_request', 'llm_response',
                                       -- 'tool_call', 'tool_result', 'message_sent',
                                       -- 'context_assembled', 'error'
    data JSONB NOT NULL,
    created_at TIMESTAMP
)
```

FTS indexes on `skills.content`, `skills.description`, `skill_references.content`, `memories.content`.

## Agent Tools (MVP)

The agent has these tools available:

Tools are hardcoded in Go for MVP (not DB-driven). All tools pass through the permission layer. Admin-only tools require `is_admin = true`. Non-admin users don't see admin tools in their tool definitions at all (scoping by omission).

`send_slack_message` receives channel and thread_ts from the platform context, not as LLM-provided arguments — the LLM just provides the message content.

**All users:**

| Tool | Purpose |
|---|---|
| `send_slack_message` | Post a response in the Slack thread (terminal action) |
| `search_skills` | FTS query across skill content, filtered by tenant + user's role scopes |
| `load_skill` | Load full skill content by ID (permission layer validates role scope) |
| `load_reference` | Load a skill reference file by ID |
| `save_memory` | Store a fact for future conversations (user-scoped by default) |
| `search_memories` | FTS query across memories for current tenant + user scope |

**Admin only:**

| Tool | Purpose |
|---|---|
| `list_roles` | List all roles for this tenant |
| `create_role` | Create a new role |
| `assign_role` | Map a Slack user to a role |
| `unassign_role` | Remove a role from a user |
| `list_skills` | List all skills (all scopes, not just current role) |
| `create_skill` | Create a new skill from content or uploaded file |
| `update_skill` | Edit an existing skill |
| `delete_skill` | Remove a skill |
| `list_rules` | List all rules (all scopes) |
| `create_rule` | Create a new rule with scopes |
| `update_rule` | Edit an existing rule |
| `delete_rule` | Remove a rule |
| `update_role` | Edit a role's description |
| `delete_role` | Remove a role |
| `forget_memory` | Delete a specific memory |

## Build Order

Suggested implementation sequence, each step producing something testable:

1. **Go project scaffold** — HTTP server, Postgres connection, migrations, Dockerfile, deploy to Dokku
2. **Slack webhook** — Receive events, verify signatures, respond with 200 OK
3. **OAuth install flow** — Install creates tenant, stores bot token
4. **Message handling** — Receive message events, resolve tenant, post reply in thread (hardcoded "Hello" response)
5. **Agent loop + session logging** — Haiku call, tool-call-only output via `send_slack_message`, log every invocation
6. **Permission layer** — Tool call validation (tenant, role, resource scoping) enforced in Go
7. **Onboarding flow** — Kit DMs installer, creates roles, maps users via chat
8. **Rules engine** — Store rules, compose system prompt from matching scopes
9. **Skills engine** — Store skills, catalog in prefix, `search_skills`/`load_skill` tools with role-scoped access
10. **Memory** — Explicit save/search/forget tools, FTS retrieval into prompt, user-scoped by default
11. **File ingestion** — Handle Slack file uploads, convert to skills via Sonnet
12. **Polish** — Error handling, startup recovery, rate limiting
