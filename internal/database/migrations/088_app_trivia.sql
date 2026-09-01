-- +goose Up

-- Trivia: a live pub quiz running on three surfaces at once -- a host console
-- in the admin web UI, a phone in every team's hand, and a big TV the room is
-- watching. The board is Jeopardy's (categories across, point values down);
-- the round mechanic is Wits & Wagers' (everyone answers every question, all
-- guesses are revealed together, then everyone bets on whose guess is
-- closest). Answers are numeric, so "closest without going over" scores
-- itself and there is no host adjudication.
--
-- The state that matters is all here rather than in a process. Three surfaces
-- and up to twenty phones have to agree about what phase the game is in and
-- how long is left, a host's laptop may close mid-round, and the Go process
-- may restart between two questions -- so the row is the authority and every
-- reader recomputes from it. Nothing below is a cache of something held in
-- memory.

-- The workspace question bank. Not per-game: a bank is a thing a venue grows
-- over months, and creating a game draws from it preferring questions used
-- least recently, so a weekly quiz doesn't repeat itself.
--
-- answer_value is DOUBLE PRECISION, not NUMERIC. Every integer below 2^53 is
-- exact and trivia answers live far below that ("what year", "how many feet",
-- "what percent"), and it keeps the pure scoring engine free of pgtype
-- imports -- the engine is a plain function over float64 and stays testable
-- with no database in sight. answer_text is the display spelling ("1969",
-- "$1,200") so the TV can show what a human wrote rather than %g output.
--
-- prompt_key is the case- and space-folded prompt, and it exists so
-- re-uploading a corrected CSV is a no-op on the rows that didn't change
-- rather than a duplicate bank. Hosts do this constantly.
CREATE TABLE app_trivia_questions (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    prompt        TEXT NOT NULL,
    prompt_key    TEXT NOT NULL,
    answer_value  DOUBLE PRECISION NOT NULL,
    answer_text   TEXT NOT NULL DEFAULT '',
    -- Set when a board that used this question is built, so the next board
    -- prefers what the room has not heard recently. NULL = never used.
    last_used_at  TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Re-importing a CSV is an upsert, not a duplicate.
CREATE UNIQUE INDEX idx_app_trivia_questions_key
    ON app_trivia_questions (tenant_id, prompt_key);

-- The board build prefers least-recently-used questions, so it reads in this
-- order. NULLs first is the point: never-asked questions come out ahead of
-- everything.
CREATE INDEX idx_app_trivia_questions_lru
    ON app_trivia_questions (tenant_id, last_used_at NULLS FIRST);

-- A question carries one to five topics, and a topic maps to a board column.
-- Many-to-many because the interesting questions genuinely belong to two
-- categories, and forcing a single one is what makes a bank unable to fill a
-- board that a matching could have filled.
--
-- topic_key is folded for grouping; topic is the first display spelling seen,
-- because "Sports" and "sports" in the same sheet must be one column while
-- the host still sees the spelling they typed.
CREATE TABLE app_trivia_question_topics (
    tenant_id     UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    question_id   UUID NOT NULL REFERENCES app_trivia_questions(id) ON DELETE CASCADE,
    topic_key     TEXT NOT NULL,
    topic         TEXT NOT NULL,
    PRIMARY KEY (question_id, topic_key)
);

-- The setup page's histogram and the board builder's per-topic bank read
-- this way. A plain index is enough -- the (question_id, topic_key) primary
-- key already covers uniqueness.
CREATE INDEX idx_app_trivia_question_topics_topic
    ON app_trivia_question_topics (tenant_id, topic_key);

-- One game night. `name` is three hyphenated common words (brave-otter-lamp),
-- chosen so it is typeable off a TV screen across a loud room, and it is the
-- public URL contract: /{slug}/trivia/{name}. Names are never recycled, for
-- the same reason an event slug isn't -- this one may be written on a
-- whiteboard behind the bar.
--
-- state_version is the spine of the real-time layer, and it is a BIGINT
-- counter rather than an updated_at timestamp on purpose: a timestamp gives
-- no atomicity, and two host clicks landing in the same millisecond would be
-- indistinguishable. Bumped inside the same transaction as every mutation
-- (SET state_version = state_version + 1 ... RETURNING), it serves as the SSE
-- frame id, the poll fallback's cursor, and the display's staleness watchdog
-- from one column.
--
-- phase_deadline is an absolute server timestamp written at phase entry and
-- never accepted from a client. It is what makes the host a controller rather
-- than an authority: close the host's laptop mid-question and the phase still
-- ends on time, because everything that reads the game sweeps it first.
--
-- Board shape, cell values, token values, timers and final_wager are settings
-- on the game, deliberately not columns on a CSV and not a "game mode". A
-- quick game and a long game are the same code with different numbers; that
-- is what keeps a second ruleset from ever needing to exist.
CREATE TABLE app_trivia_games (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    title           TEXT NOT NULL DEFAULT '',
    -- setup | lobby | board | question | reveal | betting | scoring | podium.
    -- There is no separate final phase: the final re-enters `question` with
    -- rounds.is_final set. See the state machine in the app package.
    phase           TEXT NOT NULL DEFAULT 'setup',
    board_rows      INT NOT NULL DEFAULT 2,
    board_columns   INT NOT NULL DEFAULT 5,
    -- One value per row, cheapest first. Cell values sit well above token
    -- values on purpose: only the team(s) who WROTE the winning answer take
    -- the cell, so with a full room most teams earn nothing from that channel
    -- in most rounds, and betting is the only income they reliably have. At
    -- $500/$1000 against $100/$200 tokens, writing the winning answer is
    -- worth about five good bets, so the trivia is the game rather than
    -- decoration on a betting game.
    cell_values     INT[] NOT NULL DEFAULT '{500,1000}',
    -- The two chips each team places, which must go on two DIFFERENT answers.
    -- Only one answer wins, so betting income is capped at ONE chip: $200 a
    -- round, not $300. Easy to size the economy against $300 by mistake.
    token_values    INT[] NOT NULL DEFAULT '{100,200}',
    final_wager     BOOLEAN NOT NULL DEFAULT TRUE,
    answer_seconds  INT NOT NULL DEFAULT 60,
    reveal_seconds  INT NOT NULL DEFAULT 15,
    bet_seconds     INT NOT NULL DEFAULT 45,
    current_round_id UUID,
    phase_deadline  TIMESTAMPTZ,
    state_version   BIGINT NOT NULL DEFAULT 1,
    created_by      UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The public URL contract.
CREATE UNIQUE INDEX idx_app_trivia_games_name
    ON app_trivia_games (tenant_id, name);

CREATE INDEX idx_app_trivia_games_recent
    ON app_trivia_games (tenant_id, created_at DESC);

-- The liveness sweeper's query, once every 500ms for the whole process. The
-- partial index is what makes a tick with no live game an index scan over an
-- empty set rather than a sequential scan of every game ever played.
--
-- Deliberately NOT tenant-scoped: the sweeper is process-wide infrastructure
-- looking for any expired deadline anywhere, and every write it then issues
-- is tenant-filtered. This is the one read in the app that isn't.
CREATE INDEX idx_app_trivia_games_deadline
    ON app_trivia_games (phase_deadline) WHERE phase_deadline IS NOT NULL;

-- The materialised board: one row per cell, resolved once when the game is
-- built rather than chosen live. A host at 7pm should discover an
-- under-supplied topic while setting up, not three questions into the night.
CREATE TABLE app_trivia_board_cells (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    game_id      UUID NOT NULL REFERENCES app_trivia_games(id) ON DELETE CASCADE,
    -- Nothing in v1 reads this. It is the one speculative column, kept
    -- because a second double-value board is the likeliest next feature, it
    -- belongs in the board's unique index anyway, and it is free in a table
    -- with no rows -- whereas adding it later is a migration against live
    -- games.
    round_index  INT NOT NULL DEFAULT 0,
    col_index    INT NOT NULL,
    row_index    INT NOT NULL,
    topic        TEXT NOT NULL,
    points       INT NOT NULL,
    question_id  UUID NOT NULL REFERENCES app_trivia_questions(id) ON DELETE RESTRICT,
    played_at    TIMESTAMPTZ
);

CREATE UNIQUE INDEX idx_app_trivia_board_cells_pos
    ON app_trivia_board_cells (game_id, round_index, col_index, row_index);

-- A question carrying two topics is eligible for two columns. Without this
-- the board builder could place it in both and the room would be asked the
-- same thing twice -- which reads as a bug to every person in it.
CREATE UNIQUE INDEX idx_app_trivia_board_cells_question
    ON app_trivia_board_cells (tenant_id, game_id, question_id);

-- One team, which is a table of people sharing a phone.
--
-- name_key uniqueness is enforced by the index rather than a handler check
-- because two phones typing "Bar Flies" at the same moment would both pass a
-- read-then-write.
--
-- token_hash is all that is stored of the team's identity cookie; the cookie
-- itself is an HMAC the server re-derives. A team that loses its cookie is
-- re-admitted by the host from the console, never by picking a name off a
-- list -- with twenty names on a TV screen that would be an impersonation
-- hole.
--
-- eligible_from_ordinal is the round ordinal this team first counts toward.
-- Without it a team joining mid-question lands in that question's
-- denominator, "12 of 20 answered" ticks BACKWARDS on the TV, and the
-- everyone's-in early close never fires.
CREATE TABLE app_trivia_teams (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id             UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    game_id               UUID NOT NULL REFERENCES app_trivia_games(id) ON DELETE CASCADE,
    name                  TEXT NOT NULL,
    name_key              TEXT NOT NULL,
    token_hash            TEXT NOT NULL,
    eligible_from_ordinal INT NOT NULL DEFAULT 0,
    joined_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_app_trivia_teams_name
    ON app_trivia_teams (tenant_id, game_id, name_key);

CREATE INDEX idx_app_trivia_teams_game
    ON app_trivia_teams (tenant_id, game_id, joined_at);

-- One question in play. cell_id is NULLABLE because a final has no cell: its
-- question is drawn from the bank by the host rather than from the board.
-- That nullability is the whole cost of the final -- no new phase, no new
-- table, one boolean and one branch in a pure function.
--
-- points is copied from the cell rather than joined at scoring time, so
-- editing a board mid-game can never retroactively change what an already
-- played round paid.
CREATE TABLE app_trivia_rounds (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    game_id         UUID NOT NULL REFERENCES app_trivia_games(id) ON DELETE CASCADE,
    cell_id         UUID REFERENCES app_trivia_board_cells(id) ON DELETE SET NULL,
    question_id     UUID NOT NULL REFERENCES app_trivia_questions(id) ON DELETE RESTRICT,
    is_final        BOOLEAN NOT NULL DEFAULT FALSE,
    ordinal         INT NOT NULL,
    points          INT NOT NULL DEFAULT 0,
    winning_slot_id UUID,
    started_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    scored_at       TIMESTAMPTZ
);

-- A double-clicked cell cannot open two rounds.
CREATE UNIQUE INDEX idx_app_trivia_rounds_cell
    ON app_trivia_rounds (tenant_id, cell_id) WHERE cell_id IS NOT NULL;

-- At most one final per game, enforced here rather than in the action
-- handler, which two racing clicks could both pass.
CREATE UNIQUE INDEX idx_app_trivia_rounds_final
    ON app_trivia_rounds (tenant_id, game_id) WHERE is_final;

CREATE UNIQUE INDEX idx_app_trivia_rounds_ordinal
    ON app_trivia_rounds (tenant_id, game_id, ordinal);

-- What a team typed. `raw` keeps the original text so the phone can echo back
-- "we read that as 1200" and the TV can show the spelling; `value` is what
-- the engine sorts and compares.
--
-- stake lives HERE, not on bets, because in a final it is committed WITH the
-- answer -- before any bet exists to hang it on, and before the team has seen
-- anyone else's number. That ordering is the point of the final: locking the
-- amount early makes it a wager rather than a calculation.
CREATE TABLE app_trivia_answers (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    round_id     UUID NOT NULL REFERENCES app_trivia_rounds(id) ON DELETE CASCADE,
    team_id      UUID NOT NULL REFERENCES app_trivia_teams(id) ON DELETE CASCADE,
    value        DOUBLE PRECISION NOT NULL,
    raw          TEXT NOT NULL DEFAULT '',
    stake        INT,
    submitted_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Editing your answer before time is up is an upsert, not a second row.
CREATE UNIQUE INDEX idx_app_trivia_answers_team
    ON app_trivia_answers (tenant_id, round_id, team_id);

-- The revealed cards, sorted ascending, with position 0 always the "Smaller
-- than all of these" pseudo-slot for when every guess overshoots.
--
-- Distinct VALUES, not distinct teams: two tables that both write 1969 share
-- one card and both appear in its team list. That dedupe is what removes tie
-- breaking from the game entirely -- "who wins a tie" is never a case the
-- scoring engine has to reason about, and both tables take full cell value,
-- because splitting it would punish agreement.
--
-- odds stays 1 in v1. The original Wits & Wagers prints a 2:1 -> 6:1 ladder
-- and its designer removed it from both editions aimed at casual crowds: odds
-- MULTIPLY a stake, and multiplying an already-larger bank is positive
-- feedback toward a runaway leader, which is the opposite of what the mat is
-- assumed to do. The column stays as the seam for an optional Casino mode; no
-- balance is priced into it.
CREATE TABLE app_trivia_slots (
    id        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    round_id  UUID NOT NULL REFERENCES app_trivia_rounds(id) ON DELETE CASCADE,
    position  INT NOT NULL,
    -- NULL on the pseudo-slot, which stands for "smaller than every guess".
    value     DOUBLE PRECISION,
    label     TEXT NOT NULL DEFAULT '',
    odds      INT NOT NULL DEFAULT 1
);

CREATE UNIQUE INDEX idx_app_trivia_slots_pos
    ON app_trivia_slots (tenant_id, round_id, position);

-- Which teams wrote a given card. Team names show on the cards from reveal
-- onward -- knowing who is usually right is half the fun of deciding where to
-- put your chips.
CREATE TABLE app_trivia_slot_teams (
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    slot_id   UUID NOT NULL REFERENCES app_trivia_slots(id) ON DELETE CASCADE,
    team_id   UUID NOT NULL REFERENCES app_trivia_teams(id) ON DELETE CASCADE,
    PRIMARY KEY (slot_id, team_id)
);

CREATE INDEX idx_app_trivia_slot_teams_team
    ON app_trivia_slot_teams (tenant_id, team_id);

-- Chips on cards. token_index is 0 or 1 during the board (the $100 and the
-- $200), and always 0 in a final, where a team has one bet of a self-chosen
-- amount.
--
-- Two unique indexes, and both are load-bearing under two fast taps:
--   * (round, team, token_index) makes moving a chip an UPDATE, so a
--     double-tap cannot double a team's money.
--   * (round, team, slot_id) is the two-different-answers rule, enforced by
--     index rather than a handler check that two racing requests could both
--     pass. The forced spread is what makes the denominations a decision:
--     with free stacking both chips optimally go on the single likeliest
--     answer and $100 vs $200 means nothing.
CREATE TABLE app_trivia_bets (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    round_id    UUID NOT NULL REFERENCES app_trivia_rounds(id) ON DELETE CASCADE,
    team_id     UUID NOT NULL REFERENCES app_trivia_teams(id) ON DELETE CASCADE,
    token_index INT NOT NULL DEFAULT 0,
    amount      INT NOT NULL,
    slot_id     UUID NOT NULL REFERENCES app_trivia_slots(id) ON DELETE CASCADE,
    placed_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_app_trivia_bets_token
    ON app_trivia_bets (tenant_id, round_id, team_id, token_index);

CREATE UNIQUE INDEX idx_app_trivia_bets_spread
    ON app_trivia_bets (tenant_id, round_id, team_id, slot_id);

-- The materialised per-round delta for one team, written once when the round
-- is scored. The leaderboard is then a SUM over this table rather than a
-- replay of the scoring engine, so the TV, the phones and the host console
-- cannot disagree about a total, and a fixed bug in the engine cannot
-- silently restate a game that already happened.
CREATE TABLE app_trivia_round_scores (
    tenant_id    UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    round_id     UUID NOT NULL REFERENCES app_trivia_rounds(id) ON DELETE CASCADE,
    team_id      UUID NOT NULL REFERENCES app_trivia_teams(id) ON DELETE CASCADE,
    board_points INT NOT NULL DEFAULT 0,
    bet_delta    INT NOT NULL DEFAULT 0,
    PRIMARY KEY (round_id, team_id)
);

CREATE INDEX idx_app_trivia_round_scores_team
    ON app_trivia_round_scores (tenant_id, team_id);

-- +goose Down

DROP TABLE IF EXISTS app_trivia_round_scores;
DROP TABLE IF EXISTS app_trivia_bets;
DROP TABLE IF EXISTS app_trivia_slot_teams;
DROP TABLE IF EXISTS app_trivia_slots;
DROP TABLE IF EXISTS app_trivia_answers;
DROP TABLE IF EXISTS app_trivia_rounds;
DROP TABLE IF EXISTS app_trivia_teams;
DROP TABLE IF EXISTS app_trivia_board_cells;
DROP TABLE IF EXISTS app_trivia_games;
DROP TABLE IF EXISTS app_trivia_question_topics;
DROP TABLE IF EXISTS app_trivia_questions;
