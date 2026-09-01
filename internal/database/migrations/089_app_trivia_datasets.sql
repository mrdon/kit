-- +goose Up

-- Datasets: the one concept for "a set of questions".
--
-- Before this there was a single per-workspace bank and a CSV upload dumped
-- into it, which meant a venue running a Christmas quiz and an ordinary
-- Tuesday had one undifferentiated pile and no way to keep them apart. It
-- also made the shipped starter questions a special case -- something the
-- code knew about that a host could not see, edit or remove.
--
-- Now there is exactly one kind of thing. A dataset is a named collection of
-- questions; an upload creates or replaces one; the starter pack is seeded as
-- an ordinary dataset and is deletable like any other. A game points at one
-- or more of them and draws its board from those.
--
-- Nothing reads questions from anywhere but these rows, so there is no
-- "built-in plus uploaded" union anywhere in the code.
CREATE TABLE app_trivia_datasets (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    name_key    TEXT NOT NULL,
    -- Free-text note: where the questions came from, who wrote them, which
    -- night they are for.
    notes       TEXT NOT NULL DEFAULT '',
    -- Set when this dataset was seeded from a pack Kit ships, so the UI can
    -- say so and can offer to refresh it. It is NOT a different kind of row:
    -- a seeded dataset can be edited and deleted like any other, and clearing
    -- this column is not something anything depends on.
    builtin_key TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One dataset per name per workspace, so re-uploading "Christmas 2026"
-- replaces it rather than making a second one with the same label.
CREATE UNIQUE INDEX idx_app_trivia_datasets_name
    ON app_trivia_datasets (tenant_id, name_key);

-- Every question belongs to exactly one dataset. NOT NULL, so there is no
-- "loose" question floating outside the model, and ON DELETE CASCADE, so
-- deleting a dataset takes its questions with it.
ALTER TABLE app_trivia_questions
    ADD COLUMN dataset_id UUID REFERENCES app_trivia_datasets(id) ON DELETE CASCADE;

-- Backfill: any workspace that already uploaded questions gets one dataset
-- holding them, named for what it is. Done before the NOT NULL below so the
-- constraint can be added without a window where existing rows violate it.
INSERT INTO app_trivia_datasets (tenant_id, name, name_key, notes)
SELECT DISTINCT tenant_id, 'Imported questions', 'imported questions',
       'Everything uploaded before datasets existed.'
  FROM app_trivia_questions;

UPDATE app_trivia_questions q
   SET dataset_id = d.id
  FROM app_trivia_datasets d
 WHERE d.tenant_id = q.tenant_id
   AND d.name_key = 'imported questions'
   AND q.dataset_id IS NULL;

ALTER TABLE app_trivia_questions
    ALTER COLUMN dataset_id SET NOT NULL;

-- Uniqueness moves from the workspace to the dataset. Two datasets may
-- legitimately contain the same question -- a general pack and a sports pack
-- can both ask how many holes are on a golf course -- and forbidding that
-- would make the second upload silently lossy. The board builder dedupes on
-- the question text instead, so a game drawing on both still only asks it
-- once.
DROP INDEX IF EXISTS idx_app_trivia_questions_key;
CREATE UNIQUE INDEX idx_app_trivia_questions_key
    ON app_trivia_questions (tenant_id, dataset_id, prompt_key);

CREATE INDEX idx_app_trivia_questions_dataset
    ON app_trivia_questions (tenant_id, dataset_id);

-- Which datasets a game draws from.
--
-- NO ROWS MEANS EVERY DATASET, deliberately. A game created before its
-- datasets existed, or one whose only selected dataset was later deleted,
-- must still be able to build a board rather than becoming unplayable; and a
-- host who never opens the picker gets the sensible default instead of an
-- empty board. Narrowing is the explicit act.
CREATE TABLE app_trivia_game_datasets (
    tenant_id  UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    game_id    UUID NOT NULL REFERENCES app_trivia_games(id) ON DELETE CASCADE,
    dataset_id UUID NOT NULL REFERENCES app_trivia_datasets(id) ON DELETE CASCADE,
    PRIMARY KEY (game_id, dataset_id)
);

CREATE INDEX idx_app_trivia_game_datasets_tenant
    ON app_trivia_game_datasets (tenant_id, dataset_id);

-- +goose Down

DROP TABLE IF EXISTS app_trivia_game_datasets;
DROP INDEX IF EXISTS idx_app_trivia_questions_dataset;
DROP INDEX IF EXISTS idx_app_trivia_questions_key;
ALTER TABLE app_trivia_questions DROP COLUMN IF EXISTS dataset_id;
CREATE UNIQUE INDEX idx_app_trivia_questions_key
    ON app_trivia_questions (tenant_id, prompt_key);
DROP TABLE IF EXISTS app_trivia_datasets;
