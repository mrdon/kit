-- +goose Up

-- Three changes for the host, landed together because they ship together.
--
-- 1. A question carries a difficulty, 1 to 10. One is a number everybody in
--    the room knows; ten is the jelly beans in the jar. NULL is "not rated",
--    and the board treats it as the middle of the scale. It exists so the
--    board can put the hard questions in the dear rows: a ladder down the
--    rows ($100, $200, ...) was refused for as long as the bank had no grade,
--    because without one the row a question landed in was chance.
ALTER TABLE app_trivia_questions
    ADD COLUMN difficulty SMALLINT CHECK (difficulty BETWEEN 1 AND 10);

-- 2. A practice round: one question played through every phase from the
--    lobby, so the room learns the game before anything counts. The round is
--    deleted when the practice ends (its answers, cards, chips and scores
--    cascade with it), so nothing downstream -- standings, mentions, the
--    question numbering, who is eligible from which question -- ever has to
--    learn that practice existed. The flag is only for the surfaces to say
--    "practice" while it is on the wall.
ALTER TABLE app_trivia_rounds
    ADD COLUMN is_practice BOOLEAN NOT NULL DEFAULT FALSE;

-- 3. Pause. A paused phase has NO deadline -- which takes it out of the
--    sweeper's query and every closing path at once -- and remembers how much
--    clock it had here. Resuming re-arms the deadline from it. Every phase
--    change clears it, so a pause cannot leak into the next phase.
ALTER TABLE app_trivia_games
    ADD COLUMN paused_remaining_ms INT;

-- New workspaces get the ladder. Existing games keep what they were created
-- with; the settings code turns a flat list into a ladder the next time a
-- game is created or saved.
ALTER TABLE app_trivia_games
    ALTER COLUMN cell_values SET DEFAULT '{100,200}';

-- +goose Down

ALTER TABLE app_trivia_games ALTER COLUMN cell_values SET DEFAULT '{500,1000}';
ALTER TABLE app_trivia_games DROP COLUMN IF EXISTS paused_remaining_ms;
ALTER TABLE app_trivia_rounds DROP COLUMN IF EXISTS is_practice;
ALTER TABLE app_trivia_questions DROP COLUMN IF EXISTS difficulty;
