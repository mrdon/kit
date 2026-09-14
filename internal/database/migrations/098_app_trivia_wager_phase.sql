-- +goose Up

-- The final's wager is committed BEFORE the question is read out.
--
-- 088 put `stake` on app_trivia_answers and said why: in a final the amount is
-- committed WITH the answer, before the team has seen anyone else's number, so
-- the answer row is the natural place to hang it. That ordering was already
-- the right instinct and it did not go far enough. A table that has read the
-- question knows how hard it is, and "how confident am I about this particular
-- question" is precisely the calculation the wager is supposed to be free of.
-- Jeopardy makes the bet blind — category only, question unseen — and that is
-- the half of it worth copying. The TARGET still gets chosen after the reveal,
-- which is this game's own half: reading the room.
--
-- So the final grows a phase in front of it. `wager` shows the category and a
-- clock, every table locks an amount, and only then does the question appear.
-- Which means the stake can no longer live on the answer row: at the moment it
-- is committed there is no answer to hang it on, and there may never be one --
-- a table can wager and then fail to type a number, and its chip still plays.
-- Hence a table of its own, keyed on the ROUND rather than the answer.
--
-- answers.stake stays where it is, unwritten from here on. Dropping it would
-- restate every final already played -- the recap and any export read those
-- rows -- and a column nobody writes costs four bytes a row. The code stops
-- reading it (see chipAmount); this comment is the reason it is still there.
ALTER TABLE app_trivia_games
    ADD COLUMN wager_seconds INT NOT NULL DEFAULT 30;

-- What one table put up, locked before it saw the question.
--
-- Editing until the clock runs out is an upsert, the same bargain the answer
-- makes: on a thirty-second clock fat-finger anxiety costs more than a late
-- change does. A team with NO row here never locked one, which is a $0 bet
-- rather than an error -- see chipAmount.
CREATE TABLE app_trivia_wagers (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    round_id   UUID NOT NULL REFERENCES app_trivia_rounds(id) ON DELETE CASCADE,
    team_id    UUID NOT NULL REFERENCES app_trivia_teams(id) ON DELETE CASCADE,
    amount     INT NOT NULL,
    locked_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Changing your wager before time is up is an upsert, not a second row.
CREATE UNIQUE INDEX idx_app_trivia_wagers_team
    ON app_trivia_wagers (tenant_id, round_id, team_id);

-- The category, copied onto the round like the prompt and the answer already
-- are. The wager screen shows it and MUST NOT show the prompt, so the round
-- has to be able to say "Space" while still withholding the question -- and
-- joining out to the question's topics at render time would let a re-upload
-- change what a played round was filed under. A board round takes its cell's
-- column; a final takes its question's first topic.
ALTER TABLE app_trivia_rounds
    ADD COLUMN topic TEXT NOT NULL DEFAULT '';

-- +goose Down

DROP TABLE IF EXISTS app_trivia_wagers;

ALTER TABLE app_trivia_rounds DROP COLUMN IF EXISTS topic;

ALTER TABLE app_trivia_games DROP COLUMN IF EXISTS wager_seconds;
