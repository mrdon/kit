-- +goose Up

-- Who picks the next category, decided by the server rather than by whoever
-- is holding the microphone.
--
-- Before this the console derived the sentence from the last scored round
-- ("whoever wrote the winning card picks"), which is fine right up until the
-- three cases a bar actually produces: two tables wrote the same winning
-- number, nobody wrote it at all (the pseudo-slot took the round), and the
-- very first question of the night, where there is no last round and the host
-- was quietly picking for themselves. A derived sentence cannot settle any of
-- those, and three surfaces deriving it separately would settle them
-- differently.
--
-- So the picker becomes STATE: one team id, chosen in the same transaction
-- that scores the round, and read by the TV, the phones and the console
-- alike. ON DELETE SET NULL rather than CASCADE — a team leaving the game
-- should cost the room its pick, not the game row.
ALTER TABLE app_trivia_games
    ADD COLUMN picker_team_id UUID NULL REFERENCES app_trivia_teams(id) ON DELETE SET NULL;

-- Why that table is holding the pick, so each surface can phrase it without
-- re-deriving the rule: 'drawn' (the wheel at the start of the night),
-- 'wrote_winner', 'lowest'. Empty string means nobody has the pick yet, which
-- is every game before it starts.
ALTER TABLE app_trivia_games
    ADD COLUMN picker_reason TEXT NOT NULL DEFAULT '';

-- +goose Down

ALTER TABLE app_trivia_games DROP COLUMN picker_reason;
ALTER TABLE app_trivia_games DROP COLUMN picker_team_id;
