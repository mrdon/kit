---
name: Kit Architecture
description: Technical architecture for Kit - covering the agent loop, memory model, knowledge retrieval, tool system, and multi-tenant design.
---

# Kit Architecture

## Agent Loop

All three modes of operation — chat, cron, and triggers — share a single agent loop. They differ only in how initial context is assembled and how results are delivered.

```
Observe → Reason → Act (tool call) → Feed Back → (repeat or stop)
```

**Output is always a tool call, never a final text response.** Even "reply to the user" is a tool call (`send_slack_message`, `send_sms`, etc.). This means:
- The agent loop has one consistent shape — it always ends with a tool call
- Output routing is explicit and auditable (which channel, which user, what was sent)
- Cron and trigger modes don't need special output handling — they use the same `send_slack_message` or `send_email` tools as chat mode
- Multi-step flows naturally compose (look up data → format report → send to channel — all tool calls)

The loop runs until the agent calls a terminal output tool (e.g., `send_slack_message`) or hits a configurable iteration limit (guardrail against runaway loops).

**Mode differences are only in the initial observation:**
- **Chat mode:** Observation is the user's Slack message.
- **Cron mode:** Observation is the scheduled task definition ("generate daily sales summary for #managers").
- **Trigger mode:** Observation is the external event (new email, calendar change, webhook).

## Slack Integration

Slack is the primary interface. Kit is a **distributed Slack app** using the **Events API** (HTTP webhooks), not Socket Mode. This is required for multi-tenant — each org installs the same Slack app, and Slack sends events to Kit's public endpoint with a `team_id` that maps to the tenant.

### Thread-Based Sessions
Each Slack thread is a session (proven pattern from NanoClaw):
- Inbound message → extract `thread_ts` (or `ts` for top-level messages)
- Look up existing session by `(tenant_id, thread_ts)` → resume, or create new
- All replies stay in-thread, maintaining conversational context
- Users can return to a thread hours or days later; session state persists in Postgres

### UX Pattern
1. User message arrives → Kit posts a thinking indicator (emoji reaction)
2. Agent loop runs → produces tool calls → final `send_slack_message` call
3. Thinking indicator removed, real response posted in-thread

### Trigger Pattern
Kit listens for `@kit` mentions in channels. In DMs, no trigger needed — all messages are processed. This prevents Kit from responding to every message in a busy channel.

### OAuth Install Flow
Org installs Kit via Slack's OAuth flow → Kit receives `team_id`, `bot_token`, stores per-tenant. This is the onboarding entry point — after install, Kit introduces itself in a DM to the installer.

## Memory Model (Three-Tier)

Haiku's smaller context window makes aggressive context management essential. Kit uses a three-tier model:

### 1. Stable Prefix (cached across turns)
- System prompt
- Tenant configuration (org name, business type, timezone)
- Current user's role definition and permissions
- Available tool descriptions (scoped to role)
- Compact knowledge index (document titles/summaries for discovery)

This portion is identical across turns in a session and benefits from Claude's prompt caching.

### 2. Working Memory (updated, not appended)
A running summary of the current conversation's state, rewritten each turn:

```
"User (bartender role) asked about Thursday's shift schedule.
Retrieved schedule showing they work 4pm-close.
User asked to swap with Jamie. Awaiting Jamie's confirmation."
```

This lets Kit pick up long-running Slack threads hours or days later without re-reading the entire history. Stored as a per-thread row in Postgres, updated each turn.

### 3. Compact Transcript
- Recent N messages: verbatim
- Older messages: summarized (using the working memory summary)
- Full transcript stored in Postgres (append-only) for audit and reconstruction

The boundary between "recent" and "older" is dynamic based on token budget remaining after the stable prefix.

## Skills Engine

Skills are the unified knowledge and behavior layer. Following the Agent Skills open standard, a skill is markdown content with YAML frontmatter. Skills serve dual purpose:

- **Reference skills**: Knowledge the agent should know when relevant (brewery handbook, band policies, event calendar). Marked `user-invocable: false`.
- **Task skills**: Step-by-step instructions for specific actions (how to process a return, how to handle a shift swap).

### Progressive Disclosure

Not all skills are loaded into every conversation. Three tiers minimize context usage:

| Tier | What | When | Token Cost |
|------|------|------|------------|
| **Catalog** | name + description only | Every conversation (stable prefix) | ~50-100 per skill |
| **Instructions** | Full SKILL.md body | When agent decides skill is relevant | <5000 recommended |
| **References** | Supporting files | When instructions reference them | Varies |

The agent sees the catalog, decides what's relevant to the user's question, and loads only what it needs. A 200-page handbook doesn't bloat a "what time do we open?" conversation.

### Skill Structure
```
tap-room-policies/
  SKILL.md              # Overview + navigation (<500 lines)
  references/
    drink-menu.md        # Loaded only when discussing menu
    tab-policies.md      # Loaded only for payment questions
    safety-protocols.md  # Loaded only when safety comes up
```

### Storage (Postgres, not filesystem)
Since Kit is SaaS, skills are stored in Postgres, not on disk:

```sql
skills (
    id UUID,
    tenant_id UUID,
    name TEXT,
    description TEXT,
    content TEXT,           -- SKILL.md body (markdown)
    user_invocable BOOLEAN,
    created_at TIMESTAMP,
    updated_at TIMESTAMP
)

skill_references (
    id UUID,
    skill_id UUID,
    filename TEXT,
    content TEXT,           -- reference file body (markdown)
)
```

FTS index on `skills.content`, `skills.description`, and `skill_references.content` for search.

### Skill Scoping
**Default deny.** A skill with no scopes is visible to nobody. Every skill must be explicitly scoped.

```sql
skill_scopes (
    tenant_id UUID,
    skill_id UUID,
    scope_type TEXT,    -- 'tenant', 'role'
    scope_value TEXT    -- '*' for tenant-wide, 'bartender' for role, etc.
)
```

- **Platform skills** — hardcoded in Go (or embedded markdown). These are part of the application, not tenant data. They evolve with releases, not DB migrations.
- **Tenant-wide skills** — scoped with `scope_type='tenant', scope_value='*'`. Visible to all roles in that org.
- **Role-scoped skills** — scoped to one or more roles (e.g., safety protocols scoped to `[role:bartender, role:manager]`).

A skill scoped to `[role:bartender, role:manager]` only appears for those roles. A skill with no scope rows is invisible — this prevents accidental exposure of sensitive content.

### Ingestion
Skills are created via:
- **Chat**: "Kit, our return policy is: ..." → Kit creates/updates a skill
- **File upload**: Owner drops a PDF/docx/markdown/zip in Slack → Kit converts to markdown (via LLM), creates skill(s)
- **Direct edit**: Owner asks Kit to update existing skills via chat

### Retrieval
1. **Catalog in stable prefix**: Agent sees all tenant skill names + descriptions
2. **Agent decides relevance**: Based on user's question, agent calls `load_skill(skill_id)` tool
3. **FTS fallback**: If catalog descriptions don't surface the right skill, agent can `search_skills(query)` using full-text search over content
4. **Future**: pgvector semantic search for when FTS misses due to vocabulary mismatch

## Memory

Memory is persistent context that accumulates over time across conversations. It complements rules (deliberately authored) and skills (reference/procedural knowledge) with facts the agent learns organically.

### Memory Types (Cognitive Architecture)

| Type | What | Kit Implementation |
|---|---|---|
| **Working** | Current conversation context | Thread-based session history |
| **Procedural** | How to do things | Rules + Skills |
| **Semantic** | Facts about the world | **Memory system** (this section) |
| **Episodic** | What happened when | Deferred — needs temporal reasoning |

### What Gets Remembered
- Business facts: "WiFi password is ABC123", "Henderson account is at risk"
- Corrections: "The handbook says we close at 10, but we actually close at 11 on Fridays"
- User preferences: "Don prefers bullet point reports"
- Temporal context: "Health inspection scheduled for next Tuesday"

### Storage
```sql
memories (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants,
    content TEXT NOT NULL,
    scope_type TEXT NOT NULL,   -- 'user', 'role', 'tenant'
    scope_value TEXT NOT NULL,  -- slack_user_id, role name, or '*' for tenant-wide
    source_session_id UUID,    -- which conversation it came from
    created_at TIMESTAMP,
    updated_at TIMESTAMP
)
```

FTS index on `memories.content`.

### Lifecycle
- **Creation**: Background extraction after each conversation — a lightweight LLM pass identifies facts worth remembering. Also explicit: "Kit, remember that..."
- **Update**: Agent detects a correction that conflicts with an existing memory → updates rather than duplicates
- **Deletion**: Owner can ask Kit to forget specific memories. No automated decay for MVP.
- **No-op**: Most conversations produce no memories. Only persist genuinely new or corrected information.

### Retrieval
On each new conversation, Kit queries memories relevant to the current context:
1. Filter by tenant
2. Filter by scope (org-wide + current user's scoped memories)
3. FTS match against the user's message (pgvector later for semantic matching)
4. Inject top-N relevant memories into the prompt alongside rules

Memories sit between rules (always loaded) and skills (loaded on demand) — retrieved by relevance but not always present.

### Scoping
Same model as rules and skills, but **user-scoped by default**. Memories only widen in scope when the agent is confident the information is meant to be shared, or the user explicitly asks.

- **User-scoped** (default): only applies when that user is talking — "Don likes bullet points", casual facts mentioned in conversation
- **Tenant-scoped**: visible to all users — only when explicitly requested ("Kit, everyone should know we're changing hours") or when the agent is highly confident it's a shared business fact
- **Role-scoped**: applies to all users with that role — only when explicitly requested ("Kit, all bartenders should know the new happy hour is 3-5pm")

**Conservative by default.** Better to miss a shared memory than to leak one. The background extraction prompt should classify scope and default to user when uncertain.

## Rules

Rules are always-on context — constraints, tone, identity, and behavioral guidelines that must never be missed. Unlike skills (loaded on demand), rules are injected into the stable prefix on every interaction that matches their scope.

### Composable System Prompt
Rather than a single static CLAUDE.md, Kit dynamically composes the system prompt by collecting all matching rules at context assembly time:

```
System prompt =
  Platform rules (hardcoded in Go — evolve with releases)
  + Tenant rules ("We are Thunderbird Brewing, a craft brewery in Louisville, CO")
  + Role rules ("Bartenders cannot see financial data. Refer them to a manager.")
  + Task-type rules ("All cron reports include date range and week-over-week comparison")
```

### Scope Types

| Scope | Where it lives | Loaded when | Example |
|---|---|---|---|
| `platform` | Go code / embedded files | Always, all tenants | "You are Kit, a helpful assistant for this organization." |
| `tenant` | DB, `scope_type='tenant', scope_value='*'` | Always, this org | "We are a marching band booster club. Our fiscal year starts July 1." |
| `role` | DB, `scope_type='role'` | User has matching role | "Never share customer personal info in public channels" (scoped to [bartender, manager]) |
| `task_type` | DB, `scope_type='task_type'` | Matching mode (chat/cron/trigger) | "Cron reports should be concise with bullet points and comparison to prior period." |

**Default deny.** A rule with no scopes is visible to nobody. Every DB rule must be explicitly scoped (tenant-wide with `scope_value='*'`, or to specific roles/task types).

### Storage
```sql
rules (
    id UUID,
    tenant_id UUID NOT NULL,
    content TEXT,
    priority INT,       -- ordering within scope
    created_at TIMESTAMP,
    updated_at TIMESTAMP
)

rule_scopes (
    tenant_id UUID,
    rule_id UUID,
    scope_type TEXT,    -- 'tenant', 'role', 'task_type'
    scope_value TEXT    -- '*' for tenant-wide, 'bartender' for role, etc.
)
```

Rules are small (a sentence or two each). Even 20 rules is ~500 tokens — negligible in the stable prefix.

### Management
Rules are created and edited via chat:
- "Kit, remember that we're closed every Monday"
- "Kit, bartenders should never discuss pricing changes with customers"
- "Kit, for weekly reports always include month-to-date totals"

Kit infers the appropriate scope from context, confirms with the owner, and stores the rule.

## Tool System

### Declarative Tool Definitions
Tools are configuration data, not hardcoded Go. Stored per-tenant with role access mappings:

```yaml
tools:
  - name: check_schedule
    description: "Look up shift schedule for a date range"
    roles: [bartender, manager, owner]
    integration: google_calendar
    approval_required: false

  - name: send_email
    description: "Send an email on behalf of the business"
    roles: [manager, owner]
    integration: gmail
    approval_required: true

  - name: view_financials
    description: "Query financial data from QuickBooks"
    roles: [owner, board_member]
    integration: quickbooks
    approval_required: false
```

### Permission Layer
Every tool call passes through a permission layer before execution. This is enforced in Go, not by the LLM — the LLM can request anything, but the platform validates and blocks unauthorized access.

**Layer 1 — Context assembly (scoping by omission):**
At context assembly time, only include tools, skill catalog entries, and rules that match the current user's role. A bartender's agent loop doesn't have `view_financials` in its tool definitions — it can't call what it doesn't know exists. Admin tools (create/update/delete skills, rules, roles) only appear for admin users. The skill catalog only contains skills whose scopes match the user's role.

**Layer 2 — Permission validation (defense in depth):**
Every tool call is still validated before execution, in case the LLM references something it shouldn't:

1. **Tenant check:** Is this tool/resource scoped to the current tenant?
2. **Role/admin check:** Does the user's role (or admin status) permit this tool?
3. **Argument validation:** Are inputs well-formed and within bounds?
4. **Resource-level scoping:** If the tool loads a specific resource (e.g., `load_skill(skill_id)`), verify the resource's scopes allow access for the current role.
5. **Approval gate:** Does this action require human confirmation? If so, send a Slack interactive message and pause.
6. **Execute:** Call the integration and return results to the agent loop.

Malformed or unauthorized tool calls return clear error messages to the agent in-loop so it can self-correct on the next iteration. They never crash the session.

### Approval Gates
High-stakes actions (sending emails, modifying records, spending money) trigger Slack interactive messages (buttons) for user confirmation before execution. The agent loop pauses and resumes when the user responds.

## Multi-Tenant Design

### Tenant Isolation
- **Every table has a `tenant_id` column** — no exceptions. Platform-level records use a sentinel value or dedicated platform tenant, never null. This allows every query to include `WHERE tenant_id = $1` unconditionally.
- A tool call can never leak data across tenants
- Knowledge indices are filtered by tenant before any search
- Conversation history is tenant-scoped

### Session Management
- Sessions map to Slack threads (one thread = one session)
- Full history captured in session event log (append-only JSONL)
- Working memory stored separately, updated each turn (post-MVP)
- Sessions persist across Slack's asynchronous nature — users can reply hours or days later

### Session Rehydration
When a user replies in an old thread, Kit rebuilds context from the event log:

1. Look up session by `(tenant_id, channel_id, thread_ts)`
2. Query `session_events` for that session, extract `message_received` and `message_sent` events to reconstruct the transcript
3. Re-assemble **current** rules, skill catalog, and relevant memories (these may have changed since the original conversation)
4. Feed reconstructed transcript + fresh context into the agent loop

For MVP (short Q&A threads), replaying raw messages is sufficient. For longer threads post-MVP, working memory summarization compresses older messages to stay within context limits. The event log also enables replaying `context_assembled` events to see exactly what the agent knew at each step — useful for debugging.

### Role Resolution
On each message:
1. Identify Slack user
2. Look up user → tenant mapping
3. Look up user → role(s) mapping within that tenant
4. Assemble tool whitelist, knowledge access set, and system prompt for that role

## Integrations

Integrations are adapters that normalize external services into Kit's tool interface:

```
Kit Tool Call → Integration Adapter → External API (Google Sheets, Gmail, etc.)
                                    ← Normalized Response
```

Each integration adapter handles:
- Authentication (OAuth tokens stored per-tenant)
- API translation (Kit's tool schema → external API calls)
- Response normalization (external format → Kit's internal format)
- Error handling and rate limiting

MCP can be used where applicable, but integrations don't require it.

### MVP Integration
- **Slack** — Primary interface (both input and output)

### Post-MVP Integrations
- Google Calendar — Shifts, events, scheduling
- Google Sheets — Lightweight CRM, data tracking
- Gmail — Email monitoring and sending
- POS and QuickBooks as fast-follows

## Scheduled Tasks (Post-MVP)

Scheduled tasks are full agent invocations, not simple cron jobs (pattern from NanoClaw). A scheduled task spawns the same agent loop with the task definition as the initial observation. This means a "daily sales summary" task can reason, search knowledge, call tools, and compose a message — not just run a static query.

- Three schedule types: `cron` (timezone-aware), `interval`, `once` (one-shot)
- Tasks tracked in Postgres: next_run, last_run, status, error logs
- Tasks execute with a specific role's permissions (e.g., a manager-scoped report)
- Can be created/managed via chat ("Kit, send me a sales summary every Monday at 9am")

## Error Handling and Recovery

- **Fail-fast validation:** Tool calls that fail validation return clear errors to the agent, allowing self-correction in the next loop iteration
- **Session persistence:** Full conversation state (messages + working memory) stored in Postgres. Cron jobs and triggers can resume from last known state after failures
- **Iteration limits:** Agent loop has a configurable max iteration count to prevent runaway execution
- **Graceful degradation:** If an integration is down, the agent tells the user rather than retrying silently
- **Startup recovery:** On restart, check for unprocessed messages and re-queue them (pattern from NanoClaw)

## Observability

### Session Event Log
Every step of the agent loop is logged as an append-only JSONL-style event stream:

```sql
session_events (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants,
    session_id UUID NOT NULL REFERENCES sessions,
    event_type TEXT NOT NULL,
    data JSONB NOT NULL,
    created_at TIMESTAMP
)
```

Event types:
| Event | Data |
|---|---|
| `message_received` | user_id, content, channel, thread_ts |
| `context_assembled` | rules loaded, skill catalog, memories injected |
| `llm_request` | model, system prompt (or hash), messages, tools provided, input_tokens |
| `llm_response` | output_tokens, tool_calls requested, duration_ms |
| `tool_call` | tool name, arguments, role check result |
| `tool_result` | tool name, result (or error), duration_ms |
| `message_sent` | channel, thread_ts, content |
| `error` | error message, stack trace |

This provides: full session replay for debugging, per-tenant cost attribution (sum token counts), and usage patterns. Append-only, never updated — matches the natural shape of the agent loop.

## Model Selection

Kit supports multiple Claude models, selected per-task based on complexity and cost:

| Task | Model | Rationale |
|---|---|---|
| Q&A from skills | Haiku | Fast, cheap, sufficient for retrieval + answer |
| File ingestion (PDF → markdown) | Sonnet | Needs stronger comprehension for complex docs |
| Memory extraction | Haiku | Lightweight classification task |
| Onboarding conversation | Haiku | Conversational, not complex reasoning |

Model is selected by the platform before calling the API, not by the LLM itself. The agent loop is model-agnostic — it handles tool calls the same regardless of which model produced them.

## Deployment

- **Platform:** Dokku on DigitalOcean VPS (`apps.twdata.org`)
- **Database:** Postgres 16 with pgvector (`pgvector/pgvector` image, pgvector available but not required for MVP)
- **App:** Single Go binary, Dockerfile-based deployment
- **TLS:** Let's Encrypt via Dokku plugin
- **Public URL required:** Slack Events API sends webhooks to Kit's endpoint
