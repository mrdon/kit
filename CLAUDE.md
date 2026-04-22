# Kit

Role-aware knowledge base agent for small businesses, delivered as SaaS via Slack.

## Product Shape

Kit is the **ambient surface** for quick interactions — todos, decisions, briefings, and workflows consumed via Slack, voice, and the swipe web UI. For **deep/long-running work**, users point a harness (Claude Code, Cowork) at Kit's MCP to pull todos, skills, memories, and decisions as context; Kit is not trying to be the workbench. Workflows (tasks + skills) can surface one or more decisions over time, resolved asynchronously in the swipe feed.

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

- This project does **not** use pull requests — commits land directly on `main`.
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
- Every tenant-scoped table MUST have a `tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE` column — including child tables (one-to-one extensions, option lists, join tables). The only exceptions are the `tenants` table itself and globally-shared tables like `oauth_clients`. This lets every query filter by tenant_id directly without joining through a parent.
- All DB queries against tenant-scoped tables MUST include `WHERE tenant_id = ?` — no exceptions. This is the tenant isolation boundary. INSERTs must set tenant_id; UPDATEs and DELETEs must filter on it even when the primary key alone would suffice.
- All agent output goes through tool calls (`send_slack_message`), never direct text responses.
- Bot tokens are encrypted at rest (AES-256-GCM). Never log or expose decrypted tokens.
- Use `fmt.Errorf("doing thing: %w", err)` for error wrapping — always add context.
- Use `slog` for logging, never `fmt.Println` or `log.Println`.
- Parameterized queries only — never interpolate user input into SQL strings.
- Default deny for scoping: skills, rules, and memories with no scope rows are invisible.
- LLM agent tools (`internal/tools/`) and MCP tools (`internal/mcp/`) share tool metadata via `internal/services/`. Changes to one should be considered for the other.
- **Gated tools** (`DefaultPolicy: tools.PolicyGate`) must have the tool handler as the **only** entry point to the underlying dangerous operation. Either make the operation package-private, or guard it on a ctx marker set only by the registry path. No direct callers from `internal/apps/builder/action_builtins.go`, no MCP handlers that bypass `tools.Registry.Execute`. See `.claude/skills/gated-tools-guide.md`.
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
- `internal/apps/builder/` — Scriptable app substrate. Admins use admin-only meta-tools (via MCP) to create "builder apps" — named bundles of scripts, schedules, and exposed tools. Scripts run as sandboxed Python via Monty (vendored WASM at `internal/apps/builder/runtime/monty.wasm`, Rust source under `third_party/monty-wasm/`, rebuilt via `make monty-wasm`). Data lives in `app_items` (MongoDB-shaped jsonb, tenant + builder_app scoped, with a temporal `app_items_history` trigger for rollback).
- `third_party/monty-wasm/` — Forked Rust shim for pydantic's Monty interpreter. Regenerate `monty.wasm` via `make monty-wasm` when bumping the Monty version pinned in `crates/monty-wasm/Cargo.toml` (Docker-isolated Rust toolchain — host doesn't need Rust).
- `internal/scheduler/` — Background task runner (cron + builtin tasks like profile sync)
- `internal/slack/` — Slack integration: event handler, OAuth flow, API client
- `internal/sse/` — Server-Sent Events writer (used by card chat today; reusable for future ambient-feed pushes)
- `internal/transcribe/` — Voice transcription via local whisper.cpp (optional; gated on `WHISPER_BIN`/`WHISPER_MODEL`)
- `internal/chat/` — Card-scoped chat orchestration for the web UI: long-press a card, type or hold-to-talk; session keyed by `(card, user)` so follow-ups attach

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
- `list_sessions` / `get_session_events` — inspect your own agent session history. For debugging another user's sessions, query the DB directly (`dokku postgres:connect kit-db`) — the MCP surface is scoped to the caller so admins can't read other users' email/memory traces.
- `run_task` — run a task you created; `dry_run: true` captures messages without posting. Admins cannot run another user's task via MCP (the scheduled agent would act as that user's identity); for SRE-style one-off triggers on someone else's task, go through the DB or operator CLI.
- `find_user` — verify user display names and IDs

### Common checks
- **User has no display name?** Ask them to send a Slack message, or trigger the "Sync user profiles from Slack" builtin task yourself (you'll need to own that task — builtins belong to whoever installed them).
- **Task misbehaving?** After the task posts its first message, its session's `slack_thread_ts` is the real Slack message ts (not a synthetic `task-*` value). If the task is yours, use `list_sessions` → `get_session_events`. If it belongs to another user, query `sessions` + `session_events` in the DB.
- **Tenant confusion?** Query `tenants` table to see all workspaces and their `slack_team_id`.

### Voice transcription (optional)
Card chat's mic button requires a local whisper.cpp install plus ffmpeg on the PATH. Typed chat works without either.
```bash
# One-time setup on the host (Dokku example)
git clone https://github.com/ggerganov/whisper.cpp /opt/whisper.cpp
make -C /opt/whisper.cpp
curl -L -o /opt/whisper.cpp/models/ggml-base.en.bin \
  https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-base.en.bin
apt-get install -y ffmpeg
```
Then set these env vars on the deploy:
- `WHISPER_BIN=/opt/whisper.cpp/main` (or `whisper-cli` build)
- `WHISPER_MODEL=/opt/whisper.cpp/models/ggml-base.en.bin`
- `FFMPEG_BIN=ffmpeg` (defaults to PATH lookup)
Unset any of these and the mic button is hidden in the PWA.

## Agent Loop

Observe (Slack message) -> Reason (Claude Haiku) -> Act (tool call) -> Feed Back -> Repeat or Stop. Max 10 iterations. Terminal tool: `send_slack_message`. Session history reconstructed from session_events. System prompt assembled from: platform rules + tenant info + user roles + DB rules + skill catalog + recent memories.
