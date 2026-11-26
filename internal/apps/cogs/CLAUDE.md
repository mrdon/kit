# COGS app

Production tracking for a brewery: what beer is in which tank or barrel, what
was packaged from it, and what happened to it since. Gravity is the first
brewery using it, but **Gravity is seed data, not code**: tanks, beers,
formats and durations are all rows the brewery configures.

The full design (why it works the way it does, what's planned next) is in
`docs/cogs-spec.md`. Read the "Principles" and "Domain model" sections before
changing behaviour.

## Who uses what

- **The brewer**, on a paired iPad in the brewhouse: logs physical facts
  (brew day, dry hop, transfer, package, count). Screens under
  `web/console/src/pages/cogs/` (not `admin/`).
- **Admins**, in the web console: set up vessels, beers and formats, load
  day-one state, plan brews, clear the queue, fix mistakes. Screens under
  `web/console/src/pages/cogs/admin/`.
- **Kit's agent and MCP clients**: read-only, via `get_cogs_state` and
  `get_cogs_history` (`tools.go`).

## Rules that must not break

1. **The ledger is append-only.** Every brewer action writes rows to
   `app_cogs_events`. App code never UPDATEs or DELETEs those rows. Undo writes
   a `reversal` event (`undo.go`).
2. **Vessel state comes only from replay.** `Replay` in `ledger.go` works out
   what's in a tank from the events. The `app_cogs_vessels` state columns are
   a cache of that output. Never set them directly; add or change an event
   kind and its replay step instead.
3. **Replay is strict.** Something that can't have happened (taking more beer
   than the tank holds, filling a full tank) fails with `ErrConflict`. Don't
   loosen that to make an error go away; the error is usually right.
4. **Never block the brewer.** Unknown beer, a missing setting, logging a day
   late: save the entry and send the loose end to the admin queue.
5. **Blind counts.** The weekly count and packaged unit counts start at zero on
   purpose. Don't pre-fill them.
6. **No brewery-specific constants.** Never hard-code a tank name, beer, or
   keg size. Add a config column or settings field instead.
7. Every query on a `app_cogs_*` table filters on `tenant_id` (repo-wide rule).

## Where everything lives

| What | Where |
|---|---|
| App registration, prompt | `internal/apps/cogs/app.go` |
| Data types, event kinds, errors | `types.go` |
| The one write path (iPad, console, agent all use it) | `actions.go` and `actions_*.go`, one file per action group |
| Replay | `ledger.go` |
| Undo | `undo.go` |
| Database reads and writes | `db_config.go`, `db_ledger.go` |
| Read models for screens | `view_board.go`, `view_state.go`, `view_week.go`, `view_history.go` |
| Brewer HTTP API | `web.go` (capability `cogs.log`, so the iPad works) |
| Admin HTTP API | `web_setup.go`, `setup.go`, `plan.go` |
| Agent/MCP tools | `tools.go` |
| Tests | `service_test.go`, `review_test.go`, `ledger_test.go` |
| Database schema | `internal/database/migrations/128_app_cogs.sql` and later `*_app_cogs_*.sql` |
| Frontend API client | `web/console/src/api/cogs.ts` |
| Brewer screens | `web/console/src/pages/cogs/` (forms in `forms/`) |
| Admin screens | `web/console/src/pages/cogs/admin/` |
| Styles | `web/console/src/styles/cogs.css`, `cogs-admin.css` |
| Routes and nav (shared files, touch lightly) | `web/console/src/main.tsx`, `nav.ts`, `DeviceShell.tsx` |

## How a typical change flows

**Showing something new** (a number on a tile, a label on a screen) usually
needs no database change. Work it out in the `view_*.go` read model, add the
field to `api/cogs.ts`, display it in the page, and test the read model in
`service_test.go`.

**Adding a field the brewer enters** goes all the way down:

1. **Schema:** a *new* migration file, numbered one past the highest in
   `internal/database/migrations/` (e.g. `137_app_cogs_add_x.sql`). Never edit
   a migration that has already shipped.
2. **Types:** add it to the struct in `types.go` (or to `EventDetails` if it's
   per-event detail that isn't queried on).
3. **Write path:** accept it in `ActionInput` and the relevant `actions_*.go`.
4. **Read path:** include it in the `db_*.go` query and the `view_*.go` output.
5. **Frontend:** add it to `api/cogs.ts`, then the form or screen.
6. **Test:** add a case in `service_test.go` that does the action and checks
   the result.

## Running and testing

Needs Go, Node (with npm) and Docker installed.

```bash
make dev-setup                       # once: .env, local Postgres, build, a dev brewery with sample tanks
make serve                           # build and run Kit locally
go test -race ./internal/apps/cogs/  # just the COGS tests, fast
make prepush                         # everything; must pass before shipping
```

`make dev-setup` picks ports nothing else is using and prints the links to
open: a sign-in link (`.../dev-brewery/dev-login?user=U_DEV`), then the COGS
pages at `.../dev-brewery/web/cogs`. The port is `PORT` in `.env`. You're
signed in as an admin, so both the brewer and admin screens work.
`make serve` doesn't reload: stop it and run it again after a change.

Expected noise in the `make serve` log, safe to ignore: warnings that Slack
credentials aren't set, and errors from the poster renderer. Neither affects
COGS.

The sample brewery comes from `cmd/kit-seed/cogs.go`. Running
`make dev-setup` again is safe and won't duplicate data. `make db-reset` then
`make dev-setup` starts over, and wipes everything in the local database.

If setup still fails, re-run `make dev-setup` once (it's safe), then ask Don.
Never work around sign-in or auth to get a page to load.

## Working with a non-developer

The person asking for a change may know brewing well but not Go or React.

- Before writing code, restate the change in brewery terms and say which
  rule above, if any, it touches. Get a yes.
- If a request would break a rule (e.g. "let me edit a past entry directly"),
  explain why in plain words and offer the way that fits (e.g. undo and
  re-enter, or a correction from the console).
- After the change, say what to look at to check it, in plain words.

## What needs Don

Stop and ask rather than doing it when a change would:

- touch code outside the files listed above (shared Kit code: `internal/`
  outside this folder, auth, the scheduler, other apps);
- change or remove an event kind, or change how replay handles an existing
  one (old ledger rows must still replay the same way);
- edit an existing migration or delete data;
- add a new agent/MCP tool (repo rule: both surfaces in the same commit).

## Shipping

Don't push to `main` or to the `dokku` remote. Use the `/kit-change` skill: it
works on a branch, runs `make prepush`, and opens a pull request for Don to
review and deploy.
