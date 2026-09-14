-- +goose Up

-- Stop a weekly quiz asking the same question twice.
--
-- last_used_at was the old answer and it was the wrong one. It is stamped
-- when a question lands on a BOARD, which means a question nobody ever opened
-- -- half of every board, on a night that runs short -- was burned anyway,
-- and there was no way to get it back short of editing the database. It stays
-- (a board still prefers what the room has not heard recently, as a
-- tiebreak), but it is no longer what "asked before" means.
--
-- ASKED = A ROUND ROW IN A GAME THAT STILL EXISTS. A round is only written
-- when the host opens a cell, so a question that sat unopened on a board is
-- untouched, and deleting a game gives its questions back through the cascade
-- -- no reset button, no second source of truth.
ALTER TABLE app_trivia_games
    ADD COLUMN repeat_questions BOOLEAN NOT NULL DEFAULT FALSE;

-- Rounds already copy their question's prompt and answer so history survives
-- the bank row (migration 090). The COMPARISON KEY belongs in the same copy,
-- for the same reason and one more: matching on question_id would ask the
-- same question again the moment it appears in a second pack, because those
-- are two rows saying one thing. prompt_key is what the bank already dedupes
-- on, so a question asked from the Christmas pack is spent in the general one
-- too.
ALTER TABLE app_trivia_rounds
    ADD COLUMN prompt_key TEXT NOT NULL DEFAULT '';

UPDATE app_trivia_rounds r
   SET prompt_key = q.prompt_key
  FROM app_trivia_questions q
 WHERE q.id = r.question_id AND q.tenant_id = r.tenant_id;

-- Rounds whose bank row is already gone have only the copied prompt to fold.
-- This is the ASCII half of FoldKey, which is what every shipped pack is;
-- getting an accented prompt slightly wrong in backfilled history is a far
-- smaller cost than leaving it blank and calling the question fresh.
UPDATE app_trivia_rounds
   SET prompt_key = trim(regexp_replace(lower(prompt), '[^a-z0-9]+', ' ', 'g'))
 WHERE prompt_key = '' AND prompt <> '';

-- Every draw now asks "has this key been asked in this workspace?", on the
-- board build, the final's draw and the setup page's per-topic counts.
CREATE INDEX idx_app_trivia_rounds_prompt_key
    ON app_trivia_rounds (tenant_id, prompt_key);

-- +goose Down

DROP INDEX IF EXISTS idx_app_trivia_rounds_prompt_key;
ALTER TABLE app_trivia_rounds DROP COLUMN IF EXISTS prompt_key;
ALTER TABLE app_trivia_games DROP COLUMN IF EXISTS repeat_questions;
