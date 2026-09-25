-- +goose Up

-- The break before the final becomes a setting, and it defaults to OFF.
--
-- It was added unconditionally on the theory that the final is the biggest
-- moment of the night and deserves the same run-up every other round gets:
-- standings up, a last drink, and the host starting it when the room is back.
-- That reasoning is sound for a long night and wrong for most of them. A
-- single board plus a final is about forty minutes, and stopping it dead one
-- question from the end is not a run-up, it is an interruption at the exact
-- point the room has finally stopped talking. The host already has "Add a
-- board" and can call a break whenever they want one; what they could not do
-- was skip this one.
--
-- So: off by default, on for the hosts running two or three boards who want
-- the beat. FALSE rather than TRUE as the column default because an existing
-- game mid-night should not change shape under its host on deploy, and
-- because off is the behaviour the room asked for.
ALTER TABLE app_trivia_games
    ADD COLUMN break_before_final BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down

ALTER TABLE app_trivia_games DROP COLUMN IF EXISTS break_before_final;
