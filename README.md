# Kit

The information glue for small businesses.

Kit connects your team's knowledge, automation, and tools into one place — accessible from any interface. No more answers trapped in docs, threads, and people's heads. No more repetitive tasks that should run themselves. One API that any client can consume: Slack today, any MCP-compatible AI client, and whatever comes next.

## How It Works

Kit manages knowledge and tasks, all scoped by role so people see what's relevant to them:

- **Skills** — Knowledge articles and executable instructions in markdown
- **Rules** — Behavioral guidelines that shape how Kit responds
- **Memories** — Persistent facts Kit remembers across conversations
- **Tasks** — Scheduled and one-time automation via cron expressions
- **Roles** — Scope any content to specific team roles (bartender, manager, etc.)

Your team reaches Kit through interfaces:

- **Slack** — Ask questions, manage content, and trigger tasks through natural conversation
- **MCP Server** — Connect from Claude Code, Cursor, or any MCP-compatible AI client
- **API** — Everything is accessible programmatically for integrations

Kit owns the knowledge and automation layer. Interfaces are how people and tools reach it.

## Prerequisites

- Go 1.25+
- PostgreSQL 16 (pgvector image)
- A Slack app ([create one](https://api.slack.com/apps))
- A domain with HTTPS (for OAuth callbacks)

## Quick Start

```bash
git clone https://github.com/mrdon/kit.git
cd kit
cp .env.example .env  # edit with your values
make up               # start Postgres via Docker
make dev              # start with hot reload
```

## Environment Variables

| Variable | Required | Description |
|----------|----------|-------------|
| `DATABASE_URL` | Yes | PostgreSQL connection string |
| `SLACK_CLIENT_ID` | Yes | Slack app client ID |
| `SLACK_CLIENT_SECRET` | Yes | Slack app client secret |
| `SLACK_SIGNING_SECRET` | Yes | Slack app signing secret |
| `ANTHROPIC_API_KEY` | Yes | Claude API key |
| `ENCRYPTION_KEY` | Yes | 32-byte hex key for encrypting bot tokens |
| `BASE_URL` | Yes | Public URL (e.g. `https://your-domain.com`) |
| `PORT` | No | HTTP port (default: 8080) |
| `REDIS_URL` | No | Redis for web fetch caching |

## Slack App Setup

1. Create a Slack app at https://api.slack.com/apps
2. Under **OAuth & Permissions**, add bot scopes: `app_mentions:read`, `chat:write`, `channels:history`, `groups:history`, `im:history`, `im:write`, `mpim:history`, `files:read`, `users:read`, `reactions:write`
3. Under **OpenID Connect**, add scopes: `openid`, `profile` (for MCP sign-in)
4. Set the OAuth redirect URL to `https://your-domain.com/slack/oauth/callback`
5. Set the OpenID Connect redirect URL to `https://your-domain.com/oauth/callback`
6. Under **Event Subscriptions**, set the request URL to `https://your-domain.com/slack/events` and subscribe to: `message.channels`, `message.groups`, `message.im`, `message.mpim`, `app_mention`
7. Install the app to your workspace via `https://your-domain.com/slack/install`

## MCP Setup

Add Kit to any MCP-compatible client:

```json
{
  "mcpServers": {
    "kit": {
      "type": "streamable-http",
      "url": "https://your-domain.com/mcp"
    }
  }
}
```

On first connection, your client will open a browser for Slack sign-in. After that, the token is cached automatically.

## Make Commands

```bash
make up          # Start Postgres via Docker Compose
make dev         # Start Postgres + hot reload
make build       # Build binary to ./dist/kit
make test        # Run tests with race detection
make lint        # Run golangci-lint
make format      # Format code + tidy modules
make prepush     # format + lint + test + build
make db          # Connect to local Postgres
make db-reset    # Wipe and restart Postgres
```

## Architecture

```
cmd/kit/           Entry point, HTTP server, route wiring
internal/
  agent/           Agent loop, system prompt assembly
  anthropic/       Claude API client
  auth/            OAuth for MCP (Sign in with Slack)
  config/          Environment-based configuration
  crypto/          AES-256-GCM encryption
  database/        Postgres connection + migrations
  ingest/          File upload processing
  mcp/             MCP server, tool handlers, resources
  models/          Data access layer (one file per table)
  scheduler/       Background task runner
  services/        Business logic + authorization boundary
  slack/           Slack integration (events, OAuth, API)
  tools/           Agent tool handlers (Slack adapter)
  web/             Web fetcher, landing page
```

## License

Private.
