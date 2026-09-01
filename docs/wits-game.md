# Trivia — a Jeopardy × Wits & Wagers bar game app

Build spec. Everything below is decided; where a decision reverses an obvious
alternative, the reasoning is recorded so it does not get re-litigated or silently
undone.

## What this is

Kit already paints screens — `menu` renders a tap list for a wall TV, `kiosk` points
unattended displays at a URL — but nothing interactive. This adds a **live pub quiz**
running on three surfaces at once: a host console in the admin web UI, a phone in
every team's hand, and a big TV the whole bar is watching.

The shape is Jeopardy's board — categories across the top, point values down each
column — married to Wits & Wagers' round mechanic: *everyone* answers every question,
all guesses are revealed together, then everyone bets on whose guess is closest. No
buzzer race, no team sitting idle, no host adjudication — answers are numeric, so
"closest without going over" scores itself.

### The rules, as the host reads them out

This is the forcing function for scope. If it grows past six lines, cut something.

> 1. Everybody types a number. Closest **without going over** wins.
> 2. If everyone's too high, "smaller than all of these" wins.
> 3. Whoever wrote the winning answer takes the board money.
> 4. Then everyone bets: your $100 chip and your $200 chip, on **two different**
>    answers.
> 5. Chips on the winning answer pay their value. Wrong chips cost you nothing.
> 6. *(final wager on)* Last question: set your bet **when you answer**, before you
>    see anything. Then put it on whichever answer you like. Right doubles it, wrong
>    loses it.

Rules 1–5 are the whole game with the final switched off.

### Decided rules

- Every question runs the full W&W flow. There are no Jeopardy-style buzzer questions.
- Answers are **numeric only**. Reveal sorts ascending and prepends a "Smaller than
  all of these" slot for when every guess overshoots.
- The host drives the beats (pick the cell, reveal, next); **phase timers run
  automatically** — when the question appears the answer clock is already ticking.
- Each team gets **two tokens, $100 and $200**, and they **must go on two different
  answers**. **Wrong tokens lose nothing** — during the board, scores only go up.
- **No odds mat.** Every answer pays its token's face value.
- **Default board: 5 categories × 2 rows, cells worth $500 and $1000.** Ten questions.
- **One optional Final Wager** ends the game — the only round staking your own money.
- The team(s) who wrote the winning answer earn the **board cell's value**.
- **Team names show on the answer cards from reveal onward.** Knowing who is usually
  right is half the fun of deciding where to put your chips.
- The **question bank is per workspace** and grows. Creating a game picks topics from
  it — with an **Auto button** — preferring questions used least recently.
- Up to **20 teams**.
- A game's name is **three hyphenated common words** (`brave-otter-lamp`), typeable off
  a TV screen.
- Board size, cell values, token values, timers, and `final_wager` are **per-game
  settings**, never CSV columns. Timer defaults: 60s answering, 45s betting.

---

## Prerequisite (commit 0): SSE streams die at 30 seconds today

`cmd/kit/main.go` sets `WriteTimeout: 30 * time.Second`, and
`grep -rn "ResponseController\|SetWriteDeadline"` returns **nothing** across the tree.
`WriteTimeout` is an absolute deadline stamped on the connection when headers are read;
SSE keep-alive comments reset *nginx's* `proxy_read_timeout` but not Go's write
deadline. A trivia stream needs to live for an hour.

Fix inside `sse.New` (`internal/sse/writer.go`) — **not** by relaxing the server-wide
value, which is the slowloris guard for every other route:

```go
rc := http.NewResponseController(w)
if err := rc.SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
    return nil, fmt.Errorf("clearing sse write deadline: %w", err)
}
_ = rc.SetReadDeadline(time.Time{})
```

This also fixes a live latent bug: card chat (`internal/apps/cards/chat_web.go`) has
its stream torn down at 30s on long agent turns today. **Commit this first and verify
with a 90-second `curl` before writing any trivia code.**

---

## App name and URLs

Package `internal/apps/trivia`, `const AppName = "trivia"`. Implements
`DisplayName()`/`Description()`, so it is toggleable per workspace and the `gatingMux`
in `apps.RegisterAllRoutes` 404s every `{slug}` route when disabled.

| Surface | URL | Auth |
|---|---|---|
| Host pages | `/{slug}/web/trivia`, `/trivia/:id`, `/trivia/:id/live` | console SPA — **no new Go route** |
| Host JSON API | `/{slug}/api/trivia/...` | `console.JSON` (`X-Kit-Web`) |
| Host stream | `GET /{slug}/api/trivia/games/{id}/stream` | `console.JSON` |
| **Player page** | `GET /{slug}/trivia/{game}` | `auth.TenantFromPath` only |
| Player actions | `POST /{slug}/trivia/{game}/{join,answer}`, `PUT .../bets` | public + signed cookie |
| Player stream | `GET /{slug}/trivia/{game}/stream` | public; works with **no** cookie |
| **TV display** | `GET /{slug}/trivia/{game}/tv` | public |
| Display stream | `GET /{slug}/trivia/{game}/tv/stream` | public |

`/{slug}/trivia/...` follows the house convention (`/{slug}/menu`,
`/{slug}/kiosk/{key}`) and is short enough to read aloud. `trivia` was verified not to
collide with any currently-claimed slug-level segment.

> **Routing hazard.** `GET /{slug}/` is a catch-all served by the cards PWA
> (`internal/apps/cards/web.go`). Go 1.22 mux precedence gives these longer literal
> patterns priority, so they win — but **nothing in the tree tests that**, and the
> failure mode (the game page silently serving the card feed) is exactly the bug
> documented in `internal/apps/vault/urls.go`. **Add a routing test.**

---

## Data model — `internal/database/migrations/085_app_trivia.sql`

House style: substantial rationale prose above the DDL (see `070_app_events.sql`).
Every table carries `tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE`,
child tables included, and every query filters on it.

| Table | Purpose | Constraint that matters |
|---|---|---|
| `app_trivia_questions` | the **workspace** bank: `prompt`, `prompt_key`, `answer_value DOUBLE PRECISION`, `answer_text`, `last_used_at` | `UNIQUE (tenant_id, prompt_key)` — re-uploading a corrected CSV is a no-op, not a duplicate |
| `app_trivia_question_topics` | `(question_id, topic_key)` + display spelling | `PRIMARY KEY (question_id, topic_key)`; a plain index on `(tenant_id, topic_key)` is enough |
| `app_trivia_games` | name, title, `phase`, settings, `current_round_id`, `phase_deadline`, `state_version BIGINT` | `UNIQUE (tenant_id, name)` — the public URL contract |
| `app_trivia_board_cells` | `round_index` (default 0), `col_index`, `row_index`, `topic`, `points`, `question_id`, `played_at` | `UNIQUE (tenant_id, game_id, question_id)` — a multi-topic question appears on the board **once**; without it the room gets asked the same thing twice |
| `app_trivia_teams` | `name`, `name_key`, `token_hash`, `eligible_from_ordinal` | `UNIQUE (tenant_id, game_id, name_key)` — enforced by index, not app code, or two phones racing both pass |
| `app_trivia_rounds` | `cell_id` (**nullable** — a final has no cell), `is_final`, `question_id`, `ordinal`, `points`, `winning_slot_id` | `UNIQUE (tenant_id, cell_id)` — a double-clicked cell can't open two rounds; partial `UNIQUE (tenant_id, game_id) WHERE is_final` — at most one final |
| `app_trivia_answers` | `value`, `raw`, `stake` (null outside a final) | `UNIQUE (tenant_id, round_id, team_id)` — editing is an upsert |
| `app_trivia_slots` + `app_trivia_slot_teams` | the revealed cards; `position 0` is the "Smaller" pseudo-slot; `odds` stays `1` in v1 | `UNIQUE (tenant_id, round_id, position)` |
| `app_trivia_bets` | `token_index` (0/1; always 0 in a final), `amount`, `slot_id` | `UNIQUE (tenant_id, round_id, team_id, token_index)` — a chip is in one place, moving it is an UPDATE, so a double-tap can't double a team's money. **Plus `UNIQUE (tenant_id, round_id, team_id, slot_id)`** — the two chips must land on different answers, by index rather than a handler check two racing taps could both pass |
| `app_trivia_round_scores` | materialized per-round delta | leaderboard is a `SUM`, not a replay of the engine |

`app_trivia_games` settings columns: `board_rows` default 2, `board_columns` default 5,
`cell_values` default `{500,1000}`, `token_values` default `{100,200}`,
`final_wager BOOLEAN NOT NULL DEFAULT TRUE`, `answer_seconds` 60, `reveal_seconds` 15,
`bet_seconds` 45.

`phase` enum: `setup`, `lobby`, `board`, `question`, `reveal`, `betting`, `scoring`,
`podium`. **There is no separate final phase** — see the state machine.

Decisions worth stating explicitly:

- **`state_version BIGINT` on the game row**, bumped inside the same transaction as
  every mutation (`SET state_version = state_version + 1 ... RETURNING`). Not
  `updated_at` — a timestamp gives no atomicity under two concurrent host clicks. It
  is the SSE frame id, the poll fallback's cursor, and the display's staleness
  watchdog, for one column.
- **`answers.stake` lives on the answer, not on `bets`,** because in a final it is
  committed *with the answer*, before any bet exists.
- **`teams.eligible_from_ordinal`** excludes a team joining mid-question from that
  question's denominator. Without it, "12 of 20 answered" ticks *backwards* and the
  "everyone's in" early-close never fires. Easy to miss, visibly breaks the TV.
- **`DOUBLE PRECISION`, not `NUMERIC`.** Every integer below 2^53 is exact and trivia
  answers live far below that, and it keeps the pure scoring engine free of `pgtype`
  imports.
- **`board_cells.round_index`, default 0.** Nothing in v1 reads it. It is the one
  speculative column, kept because a second double-value board is the likeliest next
  feature, it belongs in the board's unique index anyway
  (`(game_id, round_index, col_index, row_index)`), and it is free in a table with no
  rows — whereas adding it later is a migration against live games.

**Slot dedupe removes tie-breaking entirely.** Two teams that both write `1969` share
one slot and both appear in its team list, so "who wins a tie" is never a case the
scoring engine has to reason about.

---

## Game state machine

```
setup ──open_lobby──▶ lobby ──start──▶ board
                                        │ host picks a cell   (arms answer_seconds)
                                        ▼
                                    question
                    timer expiry │ all eligible answered │ host
                                        ▼                (builds slots, arms reveal_seconds)
                                     reveal
                          timer expiry │ host
                                        ▼                (arms bet_seconds)
                                    betting
                   timer expiry │ all chips placed │ host
                                        ▼                (ScoreRound, persist, clear deadline)
                                    scoring
                     host "next" ───────┴─────── board empty
                          ▼                              │
                        board            final_wager on? ├── no ──▶ podium
                                                         │
                                          host "final" ──└──▶ question (is_final)
                                                                    │
                          ┌─────────────────────────────────────────┘
                          ▼   …reveal → betting → scoring, all identical…
                       podium
```

**The final introduces no new phase.** It re-enters `question` with
`rounds.is_final = true`; the only differences are that the answer screen also collects
a stake, the bet carries that amount rather than a fixed token value, and scoring
branches once. `finish` is legal from any phase and jumps straight to `podium`;
`podium` is terminal. The final's question is drawn from the bank by the host (or at
random) rather than from a cell, which is why `rounds.cell_id` is nullable.

**One host endpoint, not eight:** `POST /{slug}/api/trivia/games/{id}/action` with
`{action, from_phase, cell_id?, seconds?}`. Every host click is a guarded transition
needing the same conflict check; a `from_phase` mismatch returns `ErrPhaseConflict` and
the console re-renders from its stream. Without this, a double-clicked "Next" silently
skips a question.

### Timers

**Deadlines are absolute server timestamps in `games.phase_deadline`, set at phase
entry, never accepted from a client.** Three layers close a timed phase, all issuing
the *same* guarded conditional UPDATE, which is what makes running them concurrently
safe:

```sql
UPDATE app_trivia_games
   SET phase = 'reveal', state_version = state_version + 1
 WHERE tenant_id = $1 AND id = $2 AND phase = 'question'
   AND phase_deadline + interval '1500 milliseconds' <= now();
```

Zero rows updated means someone already advanced: do nothing, publish nothing.

1. **Lazy sweep — the correctness guarantee.** Every read and write path calls
   `svc.SweepDue(ctx, gameID)` first. Phases advance correctly with no background
   machinery running at all; a process restarted mid-round heals on the next request.
2. **One shared ticker — liveness only.** A single package-level goroutine for the
   whole app, started from `Init`, ticking every 500ms:
   ```sql
   SELECT id FROM app_trivia_games
    WHERE phase_deadline IS NOT NULL AND phase_deadline <= now();
   ```
   with a partial index `(phase_deadline) WHERE phase_deadline IS NOT NULL`, so a tick
   with no live game is an index scan over an empty set. Each returned game gets the
   same guarded UPDATE and, on a non-zero row count, a publish.
3. **Scheduled backstop** — `scheduler.RegisterScheduledTask` every minute in
   `schedule.go`.

**Why a ticker at all.** All game state already lives in Postgres; the goroutine stores
nothing and owns nothing. It exists only so a deadline passing with nobody interacting
still moves the TV — without it the game is correct but frozen until someone touches
it. The scheduler cannot do this job: cron is five-field, so one minute is its floor,
and a countdown people are watching needs about a second.

**One shared ticker, not one timer per game.** One goroutine for the process regardless
of game count, one bounded indexed query per tick, no per-game lifecycle to leak, and
nothing to reconstruct after a restart.

> **CLAUDE.md carve-out — get explicit sign-off before implementing.** The rule is
> "recurring work is declared with `RegisterScheduledTask` … never a goroutine ticker."
> That rule exists so tenant-scoped business work has run history, `last_error`, and
> audit — and the minute-granularity backstop *does* go in the scheduler as required.
> The 500ms sweep cannot: cron's floor is one minute, and per-tick job rows would be
> pure noise in a table meant for auditable work. It is a single bounded loop that owns
> no state and whose failure costs latency, not correctness. **Say exactly that in the
> comment above it**, so the next reader sees a considered exception rather than
> someone who did not read CLAUDE.md.

**Grace window:** close at `phase_deadline + 1500ms` while the countdown shows zero at
the deadline. A phone on bar wifi submitting at T−0.2s deserves to land, and the TV
holds a "TIME!" beat that reads as drama rather than lag.

**Host's browser closed at expiry:** nothing changes. The host is a controller, never
an authority. The only phase that stalls is one with no deadline (waiting for the next
cell) — correct, because the game should wait for a human.

---

## Real-time transport: SSE + an in-process broker + a Redis relay

**Use `EventSource`, not the hand-rolled `readSSE`.** `web/shared/src/chat/sse.ts`
exists only because chat POSTs. These endpoints are GET, so `EventSource` gives browser
exponential reconnect for free — and, decisively, it **sends cookies automatically but
cannot set headers**, which is what forces the player identity to be a cookie. Leave
`readSSE` alone for chat.

Not WebSocket (new dep, new proxy surface, nothing to buy — client→server traffic is
three small POSTs). Not the menu's version-poll (right for a tap list that changes twice
a week; 21 devices at 1 Hz is both laggy on the reveal and self-inflicted load).

`internal/apps/trivia/broker.go` (~140 lines; keep it in the app, promote to
`internal/live/` only when a second app wants it):

```go
func NewBroker() *Broker
func (b *Broker) Subscribe(gameID uuid.UUID) (<-chan *Snapshot, func()) // cancel is idempotent, MUST be deferred
func (b *Broker) Publish(gameID uuid.UUID, snap *Snapshot)              // never blocks, never drops a client
```

**Latest-wins mailbox of capacity 1, not a buffered channel with a drop policy.**
Because every frame is a full snapshot, a subscriber that is behind only needs the
newest one — so `offer()` drains then sends, and "slow consumer" stops being a failure
class. `cancel()` deletes the subscriber; emptying a game's set deletes the map entry,
so it cannot grow unbounded. Test under `-race`.

**Handler ordering matters: subscribe before snapshotting.** The reverse loses any
event published in the gap; this order can only deliver a *stale* snapshot after a
fresh one, which the sequence check discards.

**Full snapshots, never deltas.** A phone joining mid-game renders correctly with no
separate bootstrap path that would drift; reconnect is free; ordering bugs vanish.
Payload is 3–6 KB. Emit `id: <seq>` for `curl` legibility, but **comment that the
server ignores `Last-Event-ID`** so nobody later assumes replay works.

**Publish only after the transaction commits**, reading the committed snapshot — never
from inside, or a rollback has already been fanned out.

### Projections

**Three endpoints, three projections, not one endpoint with a query param** — a shared
endpoint filtered by a param is one typo from leaking the answer. The broker fans out
the typed `*Snapshot`; each connection projects and marshals for itself.

| Field | Display | Player | Host |
|---|---|---|---|
| `question.text` | from `question` | from `question` | always |
| `deadlineMs` + `serverNow` | every frame | every frame | every frame |
| `answers[].value` **and team names** | from `reveal` | from `reveal` | always |
| `scoring.correctAnswer` | **`scoring` only** | **`scoring` only** | always |
| `you` (own team, submitted value, chips, stake) | — | always | — |

Make `scoring` a **nilable pointer to a distinct type**, not an `if` inside one flat
struct, so the answer cannot be populated early by accident. Back it with the one test
that matters:

```go
// TestProjectionsNeverLeakTheAnswer marshals both public projections for a snapshot
// in every pre-scoring phase and asserts the correct answer's digits appear nowhere
// in the bytes. The answer lives in Snapshot by necessity; this is the only thing
// standing between it and twenty phones.
```

### Clock skew and watchdog

Every frame carries `deadlineMs` (absolute epoch ms) and `serverNow`. The client
computes `skew = serverNow - Date.now()` per frame and renders
`deadlineMs - (Date.now() + skew)`, ticked locally at 100ms. Taking the latest sample
folds one-way delay in as a conservative bias, so the phone runs slightly *ahead* of
the server — the right direction to be wrong in. **Never send countdown ticks over
SSE.**

Client watchdog: no frame or keep-alive for 20s → close and reopen. Also force a
reconnect on `visibilitychange → visible`; a suspended iOS `EventSource` frequently
looks open and is dead.

### Multi-process: what breaks and the fix

Kit runs as **one process** today: no `Procfile`, no formation block in `app.json`,
single `CMD ["kit"]` in the `Dockerfile`, one `http.Server` in `main.go`.

At two web processes, exactly one thing breaks: **the fan-out.** nginx round-robins the
long-lived streams with no affinity, so a TV lands on process A while a host click
lands on B; B publishes to B's subscribers only and the TV freezes mid-question.
Everything else survives:

- **The sweeper is safe by construction.** Both processes tick, both issue the same
  guarded UPDATE; the loser updates 0 rows and does nothing.
- **All game state is in Postgres**, so nothing is pinned to a process.
- Player cookies, CSV import, and scoring are stateless.

**Fix with a Redis relay, built up front (~60 lines).** Redis is deployed: `REDIS_URL`
is set on the production Dokku app with a linked `kit-redis` service, and
`compose.yaml` provides one for local dev. The "optional" wording at
`cmd/kit/main.go:167` describes graceful degradation in code, not an absent dependency.

- **The in-memory broker stays the base layer** and remains the only thing an SSE
  handler talks to.
- When `rdb != nil`, `Publish` additionally does `PUBLISH trivia:<game_id>` with the
  snapshot, and one goroutine per process holds a `PSUBSCRIBE trivia:*` feeding
  received snapshots into the local broker. `Publish` is the only method that changes.
- **Suppress loopback** by stamping each snapshot with the publishing process's ID.

Redis over Postgres `LISTEN`/`NOTIFY`: purpose-built for this, `go-redis/v9` is already
a direct dependency, and `LISTEN` would mean holding a connection out of `pgxpool` in
every process with its own reconnect handling — real work to avoid infrastructure
already running. Fire-and-forget delivery is a non-issue precisely because every frame
is a full snapshot and the mailbox already discards stale ones.

**Degradation is preserved.** With Redis down or unconfigured the relay is simply
absent and fan-out is per-process — exactly correct at `web=1`, and at `web=2` the
staleness watchdog plus the `GET .../state?since={v}` poll fallback hold the game at
~5s latency rather than a frozen screen. Nothing requires Redis to be up.

---

## Scoring engine

`internal/apps/trivia/scoring.go` and `slots.go` — **pure, no DB, no ctx.** That is
what makes the rules testable in isolation. **No SQL in either file.**

```go
func BuildSlots(answers []TeamAnswer) []Slot   // dedupe on value, sort asc, prepend pseudo-slot 0
func ScoreRound(in RoundInput) RoundResult
```

1. **Closest without going over.** Winner = the slot with the largest `Value <= Correct`.
2. If no guess is `<= Correct`, the **"Smaller than all of these"** pseudo-slot wins.
   It always exists at position 0, so the winner is never zero-valued.
3. **Ties are structurally impossible** — dedupe happened in `BuildSlots`.
4. **Board points:** `CellPoints` to every team in the winning slot. Zero when the
   pseudo-slot wins, since nobody wrote it. Ties take full value each — splitting would
   punish agreement.
5. **Payout:** a chip on the winning slot pays its face value (`Amount × Slot.Odds`,
   with `Odds` always `1` in v1 — the multiply stays so a Casino mode is a data change,
   not a code change). **A chip anywhere else pays 0 and costs nothing.** Bets on the
   pseudo-slot pay normally.
6. A team that answered nothing may still bet; a team that bet nothing loses nothing.
7. **Final rounds are the one branch.** With `IsFinal`, each team has a single bet of a
   self-chosen amount: on the winning slot it pays `+amount`, anywhere else `−amount`.
   This is the only path in the app where a score decreases, which is why it lives in
   one clearly-named branch of a pure function rather than spread across the service
   layer. A team can reach exactly $0 but never below, because the stake was clamped to
   its bank when locked.

### Why there is no odds mat

The original Wits & Wagers prints a 2:1 → 6:1 ladder on its betting mat, widely assumed
to help trailing teams. Its designer, Dominic Crapuchettes, says the opposite
([BGG 1168587](https://boardgamegeek.com/thread/1168587)): *"the odds leads to an
increase likely hood of having a run-away leader."* The mechanism: odds **multiply** a
stake, and multiplying an already-larger bank is positive feedback. He also names it an
explainability problem ([BGG 1378531](https://boardgamegeek.com/thread/1378531)):
*"'2:1' is gibberish to many people… they more than double the complication of the game
to non-gamers."* He removed the mat from **both** editions aimed at casual crowds,
Party and Family.

Odds therefore fail both criteria at once — harder to explain *and* worse for the
problem they appear to solve. Keep `app_trivia_slots.odds` defaulting to `1` as the
seam for an optional Casino mode later, and price nothing into it.

### Betting: two tokens, two different answers

$100 and $200, one each on two distinct answers. Payout is face value; no
multiplication anywhere.

The forced spread is what makes the denominations a decision rather than decoration.
Under a no-loss rule with free stacking, both chips optimally go on the single likeliest
answer and the denominations change nothing. Requiring two different answers turns the
round into *name your top two, and decide which deserves the big chip* — the same
decision the odds mat would have bought, with no arithmetic and one fewer concept.

This is Wits & Wagers Party's betting (two tokens, $100/$200, nothing lost) plus one
clause.

### Board size and the value balance

**Default: 5 categories × 2 rows, cells $500 and $1000. Ten questions.**

Deliberately not Jeopardy's shape. Jeopardy's 30-clue rounds work because one person
buzzes and answers in seconds; here *every* team types an answer, sees a reveal, and
places bets, so a question costs ~3 minutes end to end. Ten questions is ~30 minutes of
board plus a lobby and a final — a bar game people finish.

**Cell values must sit well above token values, and that is a 20-team effect.** Only the
team(s) who wrote the winning answer take the cell, so with a full room most teams earn
nothing from that channel in most rounds — betting is the only income they reliably
have. If cells were $100/$200 against up to $200/round from betting, the quiz would be
decoration on a betting game. At $500/$1000 against $100/$200 tokens, writing the
winning answer is worth about five good bets, so the trivia is the game.

This self-corrects somewhat: identical guesses collapse into one slot and *everyone* in
it takes full cell value, so at 20 teams popular round-number answers spread board
points across several tables.

**The forced spread caps betting income at one chip.** Two chips on two different
answers, only one answer wins, so at most the $200 chip pays: **$200 per round
maximum**, not $300. State this in the balance comments — it is easy to size the economy
against $300 by mistake.

### The Final Wager

The game's ending, and the only round staking your own money.

After the board empties, one last question. On the answer screen a team types its number
**and** sets its stake — $0 up to its whole bank — **before seeing anyone else's
answer**. Answers are then revealed and each team puts its already-locked stake on one
of them. Right **doubles** it; wrong **loses** it. Board points for writing the winning
answer work exactly as in any round.

**You stake on someone's answer, not your own — forced by the format, not preference.**
Final Jeopardy can ask you to wager on yourself because roughly half the field knows the
answer. Here the question is "is my number the closest without going over out of twenty
guesses," which is close to a lottery: 19 of 20 teams would lose their stake every time
and the final would be luck with extra steps. Putting the stake on the betting step
keeps it a judgement — you can back the plausible number, and identical guesses collapse
so several teams share a winner. It also keeps the climax true to the game: *who do you
back*, not *did you personally know it*.

- **Up to your whole bank, not a flat cap.** Staking a fraction of your *own* score
  scales the maximum swing with the standings — the thing fixed tokens structurally
  cannot do — and preserves the value of everything before it: a team on $8,000 can
  reach $16,000 while a team on $2,000 can only reach $4,000. A flat cap is what makes
  earlier rounds feel pointless. The lock threshold becomes **leader > 2× second**,
  Jeopardy's number, where ~70% of games reach the final still live.
- **Wrong loses the whole wager, not half.** Lose-half is gentler but removes the
  leader's dilemma: at half risk the leader simply bets big too, ratios are preserved,
  and the mechanism stops working. Losing it all is also the *simpler* sentence, and a
  team wanting safety has a real option: bet $0.
- **The stake is locked before the reveal, the placement after.** If the amount were
  chosen after seeing the field it would be a calculation rather than a wager. Locking
  it early costs no new screen — the player is already on the answer screen.

**No new phases, no new tables.** A `rounds` row with `is_final = true` and a null
`cell_id`, running the ordinary question → reveal → betting → scoring flow.

**Optional per game.** `games.final_wager BOOLEAN NOT NULL DEFAULT TRUE`. With it off,
an emptied board transitions straight to `podium`, the host console never offers the
"Final question" action, and neither public UI renders a stake control — scores only go
up and rule 6 comes off the host's card. Because the whole feature is one flag plus one
branch in a pure function, "off" is genuinely the absence of the mechanic rather than a
special case threaded through the app. **Test both settings**; the off path is what a
first-ever night should run.

---

## Board build — bipartite matching, not greedy

```go
func BuildBoard(topics []string, rows int, values []int, bank []Question, seed int64) ([]Cell, error)
```

A question carries N topics; the board materialises one `(column, row) → question`
assignment once. **Greedy fails on realistic banks** — filling a common topic first
strands a rare one whose only questions were just consumed — and hands the host a
baffling error at 7pm. Matching with augmenting paths is ~50 lines, instant at this
size, and either produces a full board or proves none exists. Errors carry the
shortfall: `topic "Space" has 3 questions but the board needs 5`. `seed` makes it
deterministic in tests.

Column choice is a host decision in setup — **"Sports" vs "Sportsball" in a CSV is a real
thing and the host has to see and fix it** — defaulted to the N topics with the most
unused questions, with the **Auto button** picking randomly among viable ones. Prefer
questions with the oldest `last_used_at` so a weekly quiz doesn't repeat.

---

## CSV import

`POST /{slug}/api/trivia/questions/import`, multipart field `csv`, 2 MB cap via
`http.MaxBytesReader` + `ParseMultipartForm`, following `internal/apps/events/poster.go`.

Parsing lives in a **pure** function, testable without a DB or a request:

```go
func ParseCSV(r io.Reader) (ImportPlan, error)   // Rows, Skipped, Errors, Truncated
```

- Strip a UTF-8 BOM. `csv.Reader` with `TrimLeadingSpace`, `FieldsPerRecord = -1`
  (validate arity per row so one ragged line doesn't abort the file).
- **Header required.** Match names case-insensitively and position-independently:
  `question` (alias `prompt`), `topics` (aliases `topic`/`category`/`categories`),
  `answer`. A missing required column returns one error naming found-vs-expected.
  **Never fall back to positional columns** — a headerless file silently importing
  question text as answers is the worst available failure.
- Per row: prompt non-empty, ≤500 chars; topics split on `;`, trimmed, empties dropped,
  folded to `topic_key`, first display spelling wins, cap 5, require ≥1; answer
  normalised by stripping `$ , % _` and whitespace, then `ParseFloat` that must consume
  the **whole** string.
- Dedupe within the file and against the bank on `prompt_key`. Cap errors at 50.
- Import the good rows and report the bad — a 300-row sheet with two typos is not a
  total failure. The response carries `imported`, `skipped_duplicates`, a line-numbered
  `errors` list, and the **topic histogram**, which is what the setup page needs to
  offer column choices. It must come back from the same call, or the host uploads and
  has no idea what they got.

---

## Three-word game name

`internal/apps/trivia/words.go` — `adjectives`, `animals`, `objects`, ~256 each →
16.7M combinations. Criteria in the file header: 4–7 letters, lowercase ASCII,
unambiguous spoken across a loud room, no homophone pairs (*bear/bare*), no profanity.

```go
func randomName() string                             // crypto/rand
func UniqueName(ctx, pool, tenantID) (string, error) // 10 tries, then -2/-3 suffix
```

`crypto/rand` not for secrecy but because a process restarting twice during a deploy
must not hand two games the same seed. The loop is an optimisation; `UNIQUE
(tenant_id, name)` plus retry-on-23505 is the guarantee. **Names are never recycled** —
same reasoning as an event slug that may already be on a poster; here, on a whiteboard
behind the bar.

---

## QR code

No QR library exists in Go or npm in this repo. Add **`rsc.io/qr`** (tiny, pure Go, no
transitive deps) and render the module grid as **inline SVG rects**, injected as
`template.HTML`, ~50 lines in `qr.go`.

SVG over a PNG data URI because it scales to any panel without resampling blur, and scan
rate from fifteen feet across a bar measurably depends on that. Give it a generous quiet
zone — TV panels blur and a tight QR will not scan. Hand-rolling QR is ~600 lines of
Reed–Solomon and mask-penalty scoring for zero product value; a client-side JS lib can't
be inlined into a self-contained TV page. If a raster is ever wanted for print,
`skip2/go-qrcode` is the drop-in.

Join URL is built from the `baseURL` passed to `trivia.Configure(signer, baseURL)`, same
as `kiosk.Configure`.

---

## The three UIs

### Host console — React, in the existing SPA

Authenticated, dense, CRUD-heavy, desktop: exactly what `web/console` is for, and it
inherits the Shell, auth, and launcher tile for free.

```
web/console/src/pages/Trivia.tsx            game list + create
web/console/src/pages/trivia/setup.tsx      CSV drop, import report, settings, column picker + Auto, board preview
web/console/src/pages/trivia/live.tsx       the live driver
web/console/src/pages/trivia/useStream.ts   EventSource + skew + poll fallback
web/console/src/pages/trivia/common.ts      phase labels, formatters (mirrors pages/tasks/common.ts)
```

`live.tsx`: board grid on the left (click an unplayed cell); phase panel on the right
showing **the correct answer** (host-only, always), a large countdown, and 20 team chips
lighting as answers and bets land; leaderboard below; one big primary button labelled by
phase (`Reveal answers` / `Open betting` / `Score round` / `Next`), plus `+15s` and
`End game`. Types and methods in `api.ts`, nav entry in `nav.ts` with `app: 'trivia'`,
routes in `main.tsx`.

### TV display — server-rendered `html/template`

Follow `internal/apps/menu/` closely, with a vanilla `EventSource` script. This is a
kiosk on a cheap TV stick: fonts inlined as base64 data URIs so it paints identically on
flaky wifi (`menu/render.go`'s comment is the earned lesson), and no dependence on
`/console/assets/` being reachable. The animations are big, dumb, CSS-driven transforms
and the page is `render(state)` over seven screens — React earns nothing here. Split the
JS into `templates/display.js` injected as `template.JS`; the menu's inline script is
already at the edge of readable and this one is ~400 lines.

Use `template.CSS` / `template.URL` / `template.HTML` typed fields, or data URIs get
rewritten to `#ZgotmplZ` and you get a blank wall with nothing in the logs — the trap
`menu/render.go` documents.

**Fixed 1920×1080 stage, scaled — not `vw` layout:**

```css
#fit { width: 1920px; height: 1080px; transform-origin: top left; position: absolute; }
```
```js
fit.style.transform = 'scale(' + Math.min(innerWidth/1920, innerHeight/1080) + ')';
```

Every size becomes a plain px in a known coordinate system, so the layout is
byte-identical on a laptop and on the bar's 55" panel. Where text must shrink, prefer the
menu's **measured** loop (`board.html.tmpl`'s `fitColumnHeight`/`fitNames`) over
`clamp()` — `clamp` guesses, the loop measures.

Type scale in stage coordinates: question 96px/700 (≤3 lines, floor 64px), countdown
numeral 200px `tabular-nums`, answer values 120px, cell values 88px, category headers
42px tracked uppercase, team chips 32px, **absolute floor 28px** — anything wanting to be
smaller gets cut instead. Reuse the menu's tokens (`--navy #1b2b38`, `--accent #e8543a`,
`--cream #f4efe7`) so two screens in the same room look like one product. ≥7:1 contrast,
dark-only, no `prefers-color-scheme` (`board.css` is the precedent).

The screens:

1. **Join** — 55/45 split. Left: `BRAVE · OTTER · LAMP` one word per line at 180px, URL
   beneath at 48px, "7 TEAMS IN" at 120px. Right: QR at 640×640 with a 48px quiet zone.
   Bottom: team pills entering `scale(.6) translateY(20px)` → rest over 350ms.
2. **Board** — CSS grid, 5 columns × 2 rows at the default, so each tile is huge; used
   cells struck through at 25% opacity. Cell selection is the signature move: FLIP the
   tile — measure its rect, position a clone absolutely, transform to full-stage over
   600ms, swap in the question. ~40 lines of vanilla JS and it's what makes it feel like
   Jeopardy.
3. **Question** — prompt centred at 96px; an **SVG countdown ring** (animated
   `stroke-dashoffset`) around the numeral, because a ring reads from across a room and a
   bare number doesn't; accent → orange → pulsing red under 5s. Bottom strip: one bar per
   team filling as its answer lands, plus "12 OF 20 IN". Per-team bars beat a counter
   because the room can see *which table* is holding everyone up.
4. **Reveal** — cards sorted ascending (sorting is what makes W&W betting spatial),
   flipping in on a 120ms stagger, **each carrying the team name(s) who wrote it**.
   Identical numbers collapse into one card. Nothing else on the card.
5. **Betting** — same cards with a chip tray beneath; chips settle with a small overshoot
   and stack at 12px offsets like real casino chips; $100 and $200 in visibly different
   colours and sizes; live pot per card.
6. **Scoring** — the TV owns the choreography off `setTimeout`: dim everything but the
   cards; at 800ms the correct answer slams in as a band, the winning card gets gold and
   `scale(1.08)`, losing chips fall off; at 2200ms a leaderboard rail slides in with
   deltas counting up via `requestAnimationFrame` and rows reordering by FLIP so an
   overtake is *visible as motion*. ~5s total. **If the next server frame arrives
   mid-choreography, cancel the timers and render it immediately — the server always
   wins.** Never drive animation beats from the server; that couples timing to bar wifi.
7. **The final** — the empty board gives way to a full-screen *"FINAL QUESTION"* beat,
   then the ordinary question → reveal → betting screens styled hotter. The one addition
   is on the question screen: alongside the answered strip, each team's pip flips to
   **LOCKED** as its stake comes in — **without showing the amount**. Not knowing whether
   the leader defended or sat out is most of the tension; keep it hidden until scoring.
8. **Podium** — ranks 3, 2, 1 bottom-up at 900ms intervals; the winner gets a crown, a
   gold gradient, a scale overshoot, and a CSS particle burst (20 absolutely positioned
   divs on randomised keyframes, no library). Holds forever.

Always-on chrome: a "reconnecting" dot after 20s of silence, and a clock — keep the clock
for exactly the reason `board.html.tmpl` gives, that a frozen clock is the only way
someone walking past can tell the screen is dead. **No `<meta http-equiv="refresh">`**;
it would kill the stream.

### Player — React, a third Vite entry

The opposite call from the TV, on purpose. The phone is genuinely *interactive*: numeric
entry with live validation, chip placement with undo, optimistic submit with rollback,
eight stateful screens. framer-motion is already a dependency and its drag gesture is the
exact idiom in `web/app/src/stack/SwipeCard.tsx`.

> **The multi-entry Vite pattern is no longer in the tree.** `intake.html` + `IntakeHTML`
> + a second `rollupOptions.input` went away when the expense app was retired;
> `web/console/vite.config.ts` is back to a single `main` input and there is no
> `intake_web.go`. This work **re-establishes** that pattern rather than following a live
> one. The live analogues to copy verbatim are `IndexHTML`'s
> `__KIT_BASE__`/`__KIT_TITLE__` substitution and `StaticFileHandler` in
> `web/console/assets.go`.

Add `play: path.resolve(__dirname, 'play.html')` to `rollupOptions.input`, add
`PlayHTML(slug, title)` to `web/console/assets.go` mirroring `IndexHTML`, serve at
`GET /{slug}/trivia/{game}` behind `auth.TenantFromPath` only.

**Identity is an HMAC-signed cookie, not a localStorage bearer token.** The deciding fact
is that `EventSource` cannot set headers, so a bearer token would have to ride in the
query string — into access logs and any screenshot of the URL. A cookie is sent
automatically on the stream GET, survives a phone lock and a browser kill, and
`Path`-scoping lets one phone hold tokens for two different games.

```
Set-Cookie: kit_trivia=<hmac{gameId,teamId,nonce}>;
  Path=/{slug}/trivia/{game}; HttpOnly; Secure; SameSite=Lax; Max-Age=21600
```

Only the hash is stored (`teams.token_hash`). Mirror `{gameId, teamId, teamName}` —
**not** the token — into `localStorage` purely so the UI can render "rejoining as Bar
Flies…" before the first round-trip. Rejoin is invisible: page load → `GET .../me` →
`{teamId, name}` or 204.

**Cookie genuinely lost** (private tab, cleared cookies): do **not** offer "pick your team
from this list" — that is an impersonation hole with 20 names on a TV screen. The host
reissues from the console: tap a team, get a 4-digit code. The trust boundary belongs with
the person standing in the room who can see who's asking.

**Spectator mode is a hard requirement on the handler:** someone opening the URL with no
cookie gets the full read-only stream plus a banner, with join disabled if the game is
full. **The player stream must work with no cookie, not 401.**

Screens: **Join** (game name huge so they can confirm they scanned the right thing; live
team list from the spectator stream so a latecomer sees the party is real) → **Waiting** →
**Answer** → **Submitted** → **Betting** → **Waiting for scoring** → **Result** (delta as
the hero, counted up) → **Podium**.

In the final, the **answer screen** grows a stake control — the one place a player commits
real money, so it gets the most care: preset buttons (`$0`, `Half`, `All in`) alongside a
slider, because a slider alone is imprecise with a thumb; both outcomes spelled out before
committing (*"win → $3,200 · lose → $0"*); and a confirm step, since this is the only
irreversible action in the game. Clamp to the team's bank server-side and mirror the clamp
in the UI. `$0` is a first-class choice, not a fallback — it is the leader's defensive play
and should read that way.

Interaction details that decide whether it actually works:

- `<input type="text" inputmode="decimal" enterkeyhint="send">`. **`decimal`, not
  `numeric`** — answers can be non-integers and `numeric` gives no decimal point on iOS.
  **`text`, not `number`** — `number` brings spinners, silently drops non-numeric paste,
  and has messy locale handling. Parse in JS, reusing the CSV normaliser, and **echo the
  parsed value back before submit** ("we read that as 1200") — without it you get silent
  zeros and an argument at the bar.
- Resubmit allowed until the deadline, and *say so*: "you can change it until time's up"
  removes fat-finger anxiety.
- **Tap-to-place is primary, drag is the affordance.** Tap a chip to arm it, tap a row to
  place; drag also works via framer-motion `dragSnapToOrigin` + hit-testing in
  `onDragEnd`. In a dark bar with greasy hands, drag fights a scrolling list.
  `PUT .../bets {chip, slotId}` — a PUT of the desired placement per chip, so every retry
  is idempotent.
- **The two chips are visually distinct and the spread is enforced in the UI, not just the
  API.** Once the $200 chip is on a row, that row stops accepting the $100 chip and says
  why in three words. A rule you discover by being rejected is a bad rule; a rule the
  interface makes obvious is not felt as a rule at all.
- Guard double-dispatch with the synchronous `runningRef` pattern from `SwipeCard.tsx` —
  `setState` is async and two fast taps both see `busy === false`.
- `viewport-fit=cover`; **`100dvh`, never `100vh`** (iOS Safari's collapsing toolbar
  otherwise hides the submit button under the URL bar — the single most likely "it doesn't
  work on my phone" bug); `overscroll-behavior: none` to kill pull-to-refresh mid-question;
  `touch-action: none` on the drag surface; `navigator.wakeLock.request('screen')`, guarded
  and re-requested on `visibilitychange` — the difference between a smooth game and 20
  people unlocking their phones every 90 seconds.

---

## Agent and MCP tools

Two read-only tools, and **no `SystemPrompt`**. Nothing about "pick cell 3, reveal now" is
improved by routing through an LLM in a loud bar, and a mis-fired tool during a live game
is destructive and unrecoverable — `kiosk` contributes zero tools for exactly this reason.
But asking in Slack the next morning how it went is real value:

| Tool | Purpose |
|---|---|
| `trivia_status` | recent games: phase, team count, current leader |
| `trivia_results` | final leaderboard and per-round recap for a finished game |

Wired as `tools.go` (metas) + `core.go` (one `dispatchCore` switch) + thin `agent.go` /
`mcp.go` adapters — the `internal/apps/events` shape, where the shared dispatcher makes
parity structural rather than a review item. Both surfaces in the same commit, per
CLAUDE.md.

---

## Commit sequence

Each commit builds and passes `make prepush`.

0. **`sse.New` write-deadline fix** + a 90-second `curl` check. Nothing works without it.
1. **Migration 085 + data access** — `app.go`, `models.go`, `models_live.go`, and
   `cmd/kit/main.go` wiring. App registers, registers no routes.
2. **Pure logic + tests** — `csv.go`, `board.go`, `slots.go`, `scoring.go`, `name.go`,
   `words.go` and every `_test.go`. Zero wiring, highest test density, no risk.
   `ScoreRound`'s `IsFinal` branch lands here — the only path where a score can fall gets
   its tests written before anything depends on it.
3. **Engine** — `service.go`, `service_live.go`, `snapshot.go`, `projection.go` + **the
   withholding test**, `broker.go` + `broker_redis.go` (`-race`), `sweeper.go`,
   `schedule.go`. Still no HTTP. This is the contract; do it before any UI.
4. **Console API** — `web_console.go`, the `action` endpoint, host stream, `qr.go`, the
   `rsc.io/qr` dep. Verifiable end-to-end with curl.
5. **Console UI** — `Trivia.tsx`, `trivia/setup.tsx`, `api.ts`, `nav.ts`, `main.tsx`. Host
   can import a CSV and build a board.
6. **TV display** — `render.go` + templates, built against `testdata/snapshot_*.json`
   fixtures so every screen can be developed without a working host console. Mirror
   `internal/apps/menu/preview_test.go`'s env-gated `TestPreview`.
7. **Player** — `web_public.go`, `web_join.go`, `token.go`, `play.html`, the third Vite
   entry, `PlayHTML`, `src/play/*`.
8. **Live host console** — `trivia/live.tsx`, `useStream.ts`. The loop closes here.
9. **Tools + docs** — `tools.go`/`core.go`/`agent.go`/`mcp.go`, user guide
   (`internal/skills/builtins/user-guide/SKILL.md`), landing page
   (`internal/web/templates/landing.html`).

Files are pre-split to stay under the 500-line limit; keep functions under 60.

---

## Verification

### Unit (`go test -race -cover ./internal/apps/trivia/...`)

- `scoring_test.go` — exact hit; closest-under; **every guess too high → pseudo-slot wins
  and nobody takes board points**; two teams on the same number both take full board
  points; a team betting on its own answer; a team that bet but never answered; zero bets.
- `scoring_test.go` **final cases**, the only place a score can fall: a winning wager
  doubles the stake; a losing wager costs all of it; a $0 wager is legal and a no-op; **a
  team can reach exactly $0 but never go negative**; a team that staked but never answered
  still scores its bet; board points land normally in a final.
- `slots_test.go` — `1969` and `1969.0` collapse; ascending order; pseudo-slot always
  position 0; zero answers → only the pseudo-slot.
- `board_test.go` — no question reused, every cell filled; **a bank where greedy fails but
  a matching exists** (the regression that justifies the algorithm); an under-supplied
  topic errors naming the topic and shortfall.
- `csv_test.go` — header aliases, BOM, `$1,200` / `1.5%` / `-40` / `1e3`, missing column,
  non-numeric answer reports the right line, CRLF, ragged row, 51 errors sets `Truncated`.
- `name_test.go` — shape and charset; **no duplicates within a list and no overlap across
  the three lists** (a real and easy bug).
- `broker_test.go` — subscribe/publish/coalesce/cleanup; a subscriber that never reads does
  not block a publisher; unsubscribe racing a publish, under `-race`. Plus the relay: a
  snapshot published on one broker reaches a subscriber on a second broker sharing a Redis,
  **and a process does not re-deliver its own relayed message**.
- `projection_test.go` — **the withholding test.**
- `service_round_test.go` — a stake above the team's bank is clamped, not rejected; a stake
  cannot change once the answer phase closes; a team joining during the final cannot stake;
  and **with `final_wager` off, an emptied board goes straight to `podium` and the "final"
  action is refused**.
- `web_public_test.go` — no cookie can't answer or bet but *can* stream; another team's
  cookie can't submit your answer; the 21st team is refused; **placing both chips on one
  answer is refused**, and two racing requests that would land both chips on the same slot
  can't both succeed (the unique index, not a handler check); a game name in tenant A is
  invisible from tenant B.
- A **routing test** that `GET /{slug}/trivia/{game}` reaches this app and not the cards
  SPA catch-all.

### End-to-end, locally (`make db-reset && make dev`, plus `make console-build`)

1. Log in at `/{slug}/dev-login`; enable **Trivia** under Admin → Apps.
2. Upload ~40 questions across 6 topics with several multi-topic rows and one bad row.
   Confirm counts, the bad line's message, and the topic histogram.
3. Create a game; hit **Auto**; set timers to 20/8/20 for fast iteration; build the board
   and confirm the preview shows 5 categories × 2 rows at $500/$1000, 10 distinct questions.
4. Open the TV URL fullscreen and the player URL in three private windows (or real phones
   over the LAN). **Scan the QR from across the room** — that is the actual test.
5. Join three teams; each appears on the TV within ~1s. Start, pick a cell; all surfaces
   flip within ~200ms and the phone countdowns match the TV ring.
6. Answer from two phones, leave one silent, let the timer expire. Confirm auto-advance
   with nobody clicking, and that the silent team has no slot.
7. Reveal — ascending order, team names on the cards, "Smaller than all of these" leftmost.
   Try to put both chips on one answer and confirm the phone refuses before the request is
   sent. Place chips properly, let betting expire, then **re-derive every number by hand**
   and compare all three surfaces.
8. Empty the board, then play the final: from one phone stake `All in`, from another `$0`.
   Confirm the TV shows **LOCKED** without revealing amounts, the losing wager goes to zero
   and the winning one doubles, and a team on $0 finishes at $0 rather than negative. Then
   edit a request by hand to stake more than the bank and confirm the server clamps it.
9. Run a second game with **`final_wager` off** and confirm the emptied board goes straight
   to the podium, the host console offers no "Final question" action, and no stake control
   appears on any phone.
10. **Kill the Go process mid-round and restart.** Phase and remaining time must be correct
    from the DB alone — the proof that the deadline is server-authoritative.
11. Airplane-mode a phone for 30s; it resyncs from the snapshot without rejoining.
12. Double-click the host's advance button; confirm exactly one transition.
13. Throttle a phone to Slow 3G; confirm the watchdog and poll fallback keep it playable.
14. Run **two local processes** on different ports against the same Postgres and Redis, TV
    on one and phones on the other. Every surface must stay in step. Then stop Redis
    mid-game and confirm it degrades to per-process fan-out plus polling rather than
    freezing.
15. Play the board out; check the leaderboard against a hand-summed total.
16. Disable the app in Admin → Apps; confirm all three public URLs 404.

**MCP check** after commit 9: `trivia_status` and `trivia_results` return byte-identical
text on the agent and MCP surfaces.

---

## Deliberately not built

The goal for night one is a game a stranger understands in thirty seconds. Ship the six
rules, see whether it gets a following, and grow it only if it does.

**There is no game-mode system, and adding one would be the overbuild.** Everything on the
roadmap — a second board worth double, Double-Jeopardy spaces, an odds mat — is *additive*:
more cells, a flag on a cell, numbers in a column that already exists. None is an
alternative ruleset, so none needs a "which rules is this game playing" abstraction. A mode
switch would make each cost twice as much, because each would have to work in every mode,
and it would multiply the scoring test matrix.

**Game length is a setting, not a mode**: `board_rows`, `board_columns`, and `final_wager`
on/off. A quick game and a long game are the same code with different numbers. That
distinction is what keeps this cheap, and it is worth defending whenever the next
structural idea arrives.

Evaluated and deferred:

- **A second, double-value board** (`board_count: 2` — the Jeopardy shape). The final keeps
  the *ending* live; a double board keeps the *middle* live, which is the answer to
  Crapuchettes' own complaint that with an all-in final *"75% of the game rests upon the
  last question."* Real, but it is the second anti-runaway mechanic and neither has been
  tried in a bar yet. At ~3 minutes a question it also means two *smaller* boards.
  `board_cells.round_index` is the seam.
- **A "Casino mode" odds mat.** Not as a runaway fix — see above — but as optional flavour.
  `slots.odds` is the seam; nothing else changes.
- **Wagering on your own answer**, pure Final Jeopardy. Ruled out on arithmetic, not taste:
  at 20 teams it is a lottery, not a bet.
- **Wrong bets losing the token** during the board (true Wits & Wagers). Rejected on
  purpose, and the designer agrees: *"Players cannot end the game with fewer chips than
  they started with (which leaves a sour taste)."* Keep every board question risk-free and
  put risk on exactly one question at the end.
- **Double-Jeopardy spaces** (one cell where you wager your own score). A pure addition on
  top of per-cell `points`; changes no scoring.
- **Team-size handicaps.** A real problem for a 20-team bar — a table of eight beats a
  couple on a date — but orthogonal to runaways. Most venues just make oversized teams
  prize-ineligible, which needs no code.

---

## Open risks

1. **`WriteTimeout: 30s`** — a hard blocker, and it is truncating card chat today. Commit 0
   exists for this.
2. **Multi-process fan-out.** Scaling to two web processes breaks only the SSE fan-out; the
   sweeper and all state are safe. The Redis relay is built up front, so this is closed
   rather than deferred — but **exercise it deliberately** (two local processes behind a
   round-robin) rather than assuming.
3. **New dependency** — `rsc.io/qr` is the only addition to `go.mod`.
4. **Re-establishing multi-entry Vite** rather than following a live pattern, since the
   expense app was retired. Recoverable from history but not copy-paste.
5. **CLAUDE.md's "never a goroutine ticker"** vs the single 500ms deadline sweep. Cron's
   one-minute floor makes the scheduler structurally unable to do this job, and the
   minute-granularity backstop is registered there as the rule requires — but the exception
   needs an explicit comment and human sign-off.
6. **A lost or dead phone loses a team.** The cookie covers refresh and wifi drops but not a
   dead battery. The host-issued 4-digit reclaim code covers it; if that is scope creep it
   can slip to a follow-up.
7. **Bar wifi.** 21 long-lived connections through one AP is usually fine, but captive
   portals and AP client-isolation break SSE in ways that look like application bugs. Test
   on the venue's actual wifi before the first real night, not the week after.
8. **Balance is unproven.** With 10 rounds and $200/round maximum betting income, teams will
   finish bunched and the final will decide most games. That is dramatic and probably right
   for a half-hour game, but it is the shape Crapuchettes warned about. If the first night
   feels like the board didn't matter, the fix is more board (another row, or the double
   board), not a smaller final.
