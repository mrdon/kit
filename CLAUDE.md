# Kit

Role-aware knowledge base agent for small businesses, delivered as SaaS via Slack.

## Quick Commands

```bash
make up          # Start Postgres via Docker Compose
make dev         # Start Postgres + hot reload (requires air)
make build       # Build binary to ./dist/kit
make test        # Run tests with race detection
make lint        # Run golangci-lint
make format      # Format code + tidy modules
make prepush     # format + lint + test + build
make db          # Connect to local Postgres
make db-reset    # Wipe and restart Postgres
```

## Deploying

- `git push origin main` — push to GitHub
- `git push dokku main` — deploy to Dokku (apps.twdata.org)
- Always push to both origin and dokku when deploying.
- Logs: `ssh dokku@apps.twdata.org 'dokku logs kit --num 100'`

## Tech Stack

- Go 1.25, Postgres 16 (pgvector image)
- Slack Events API + OAuth
- Claude API (Haiku for Q&A, Sonnet for file ingestion)
- Deployed on Dokku (apps.twdata.org)

## Code Rules

- **Run `make prepush` before every commit.** This formats, lints, tests, and builds. Do not commit code that fails prepush.
- No file over 500 lines. Split into focused files when approaching the limit.
- No function over 60 lines. Extract helpers when complexity grows.
- All DB queries MUST include `WHERE tenant_id = ?` — no exceptions. This is the tenant isolation boundary.
- All agent output goes through tool calls (`send_slack_message`), never direct text responses.
- Bot tokens are encrypted at rest (AES-256-GCM). Never log or expose decrypted tokens.
- Use `fmt.Errorf("doing thing: %w", err)` for error wrapping — always add context.
- Use `slog` for logging, never `fmt.Println` or `log.Println`.
- Parameterized queries only — never interpolate user input into SQL strings.
- Default deny for scoping: skills, rules, and memories with no scope rows are invisible.
- LLM agent tools (`internal/tools/`) and MCP tools (`internal/mcp/`) share tool metadata via `internal/services/`. Changes to one should be considered for the other.
- Format: `gofmt -s`. Lint: `golangci-lint` (see .golangci.yml). Tests: `go test -race -cover ./...`
- When adding user-facing features, update the relevant docs: user guide (`internal/skills/builtins/user-guide/SKILL.md`), landing page (`internal/web/templates/landing.html`). Keep additions proportional to the feature's importance.

## Architecture

- `cmd/kit/` — Entrypoint: config, DB, migrations, HTTP server, route wiring
- `internal/agent/` — Agent loop, context assembly, tool registry + implementations
- `internal/anthropic/` — Claude Messages API client (thin HTTP wrapper)
- `internal/app.go` — Core application: Slack event dispatch, file ingestion orchestration
- `internal/config/` — Env-based configuration
- `internal/crypto/` — AES-256-GCM for sensitive data
- `internal/database/` — pgxpool connection, goose migrations (embedded in `database/migrations/`)
- `internal/ingest/` — File upload processing (PDF via pdftotext, DOCX, markdown, ZIP)
- `internal/models/` — Data access layer. One file per table group (tenant, user, role, skill, rule, memory, task, session, session_event, scope)
- `internal/apps/` — Modular feature apps (self-registering via init). Each app contributes tools, system prompt, routes, and cron jobs.
- `internal/scheduler/` — Background task runner (cron + builtin tasks like profile sync)
- `internal/slack/` — Slack integration: event handler, OAuth flow, API client

## Data Model

14 core tables: tenants, users, roles, user_roles, skills, skill_references, skill_scopes, rules, rule_scopes, memories, tasks, task_scopes, sessions, session_events. Apps add their own tables prefixed with `app_`. FTS indexes on skills.content, skills.description, skill_references.content, memories.content.

## Production Debugging

### Logs
```bash
# Recent logs (adjust --num as needed)
ssh dokku@apps.twdata.org 'dokku logs kit --num 200'

# Filter for specific topics
ssh dokku@apps.twdata.org 'dokku logs kit --num 500' 2>&1 | grep -i "error\|task\|sync"
```

### Database queries
```bash
# Interactive psql session
ssh dokku@apps.twdata.org 'dokku postgres:connect kit-db'

# One-shot query
ssh dokku@apps.twdata.org 'dokku postgres:connect kit-db <<SQL
SELECT id, slack_team_id, name FROM tenants ORDER BY created_at;
SQL'
```

### MCP tools for debugging
- `list_sessions` / `get_session_events` — inspect agent session history (admin only)
- `run_task` — run any task immediately; use `dry_run: true` to capture messages without posting
- `find_user` — verify user display names and IDs

### Common checks
- **User has no display name?** Run the "Sync user profiles from Slack" builtin task via `run_task`, or the user's name will be fetched on their next Slack message.
- **Task misbehaving?** Use `list_sessions` to find the task session (thread_ts starts with `task-`), then `get_session_events` to see the full agent trace.
- **Tenant confusion?** Query `tenants` table to see all workspaces and their `slack_team_id`.

## Agent Loop

Observe (Slack message) -> Reason (Claude Haiku) -> Act (tool call) -> Feed Back -> Repeat or Stop. Max 10 iterations. Terminal tool: `send_slack_message`. Session history reconstructed from session_events. System prompt assembled from: platform rules + tenant info + user roles + DB rules + skill catalog + recent memories.
