-- +goose Up

-- Make a played round self-contained, so history does not depend on a bank
-- row that somebody may want to delete later.
--
-- THE PROBLEM THIS FIXES. app_trivia_board_cells references questions with ON
-- DELETE RESTRICT, which was right on its own terms -- deleting the question
-- a cell is about would leave a game unable to say what it asked. But it also
-- meant a dataset became permanently undeletable the moment any game used it,
-- including a game finished months ago. A venue running a weekly quiz would
-- accumulate question sets it could never remove.
--
-- The fix is the one the `points` column already made. A round copies its
-- cell's value at the moment it opens, so that editing a board can never
-- retroactively change what an already-played round paid. The question text
-- and the answer deserve exactly the same treatment, for the same reason and
-- one better: a live round must not be able to have its answer changed
-- underneath it by somebody re-uploading a corrected sheet mid-game.
--
-- With the round carrying its own copy, the recap reads history from history,
-- scoring reads the answer the room was actually asked, and a question row is
-- no longer load-bearing once its round has started.
ALTER TABLE app_trivia_rounds
    ADD COLUMN prompt       TEXT NOT NULL DEFAULT '',
    ADD COLUMN answer_value DOUBLE PRECISION,
    ADD COLUMN answer_text  TEXT NOT NULL DEFAULT '';

-- Backfill from the bank while the link still exists.
UPDATE app_trivia_rounds r
   SET prompt = q.prompt,
       answer_value = q.answer_value,
       answer_text = q.answer_text
  FROM app_trivia_questions q
 WHERE q.id = r.question_id AND q.tenant_id = r.tenant_id;

-- The round no longer needs the question to survive. Keep the reference for
-- provenance ("which bank row was this?") but let it go null rather than
-- pinning the row forever.
ALTER TABLE app_trivia_rounds
    DROP CONSTRAINT IF EXISTS app_trivia_rounds_question_id_fkey;
ALTER TABLE app_trivia_rounds
    ALTER COLUMN question_id DROP NOT NULL;
ALTER TABLE app_trivia_rounds
    ADD CONSTRAINT app_trivia_rounds_question_id_fkey
    FOREIGN KEY (question_id) REFERENCES app_trivia_questions(id) ON DELETE SET NULL;

-- Board cells are the other half. A cell that has been PLAYED has a round
-- carrying everything the game needs, so the cell can go when its dataset
-- does. An UNPLAYED board is protected at the service layer instead, which
-- refuses to delete a dataset a game that has not finished is relying on --
-- a check SQL cannot express, because "still being played" is a phase, not a
-- foreign key.
ALTER TABLE app_trivia_board_cells
    DROP CONSTRAINT IF EXISTS app_trivia_board_cells_question_id_fkey;
ALTER TABLE app_trivia_board_cells
    ADD CONSTRAINT app_trivia_board_cells_question_id_fkey
    FOREIGN KEY (question_id) REFERENCES app_trivia_questions(id) ON DELETE CASCADE;

-- +goose Down

ALTER TABLE app_trivia_rounds DROP COLUMN IF EXISTS prompt;
ALTER TABLE app_trivia_rounds DROP COLUMN IF EXISTS answer_value;
ALTER TABLE app_trivia_rounds DROP COLUMN IF EXISTS answer_text;
