-- +goose Up

-- Drop the "setup" phase. A game is joinable the moment it exists.
--
-- There were two phases before a question was asked: `setup`, where the host
-- built the board, and `lobby`, where teams could join -- with an "Open the
-- lobby" button between them. That button asked the host to announce a state
-- change that nothing actually depended on: the board is built from the setup
-- page whenever they like, and there is no reason a table scanning the QR
-- early should be turned away.
--
-- So a game now starts in `lobby` and the host has exactly two controls:
-- start the game, and end it. `setup` stays in the phase vocabulary because
-- old rows may carry it and the code still reads it, but nothing creates it.
ALTER TABLE app_trivia_games ALTER COLUMN phase SET DEFAULT 'lobby';

UPDATE app_trivia_games SET phase = 'lobby' WHERE phase = 'setup';

-- +goose Down
ALTER TABLE app_trivia_games ALTER COLUMN phase SET DEFAULT 'setup';

-- Every game gets a human name. The two-word slug is a URL token, not a
-- label, and it was leaking into the console and onto the TV wherever a title
-- had not been typed -- so the biggest text on a wall could be "vague jaguar
-- coin", which means nothing to anybody in the room.
--
-- Backfilling rather than falling back in the UI: a fallback is a branch that
-- every surface has to remember, and one of them always forgets.
UPDATE app_trivia_games SET title = 'Quiz night' WHERE title = '';
