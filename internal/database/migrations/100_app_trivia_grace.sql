-- +goose Up

-- The beat everyone else got, given to the table that closed the phase.
--
-- Early close was a straight win for the room -- three tables should not sit
-- out sixty seconds of silence -- but it quietly robbed exactly one table
-- every single round. The last chip to land ended betting the instant it
-- landed, so the table that placed it never got to look at what it had done,
-- never saw the wall with its own money on it, and could not move the chip it
-- had just put down. Every other table had that beat; the decisive one did
-- not. Same story on the answer clock: the last table to type a number had it
-- taken off them mid-keystroke while everyone else had been free to revise.
--
-- So "everyone is in" stops being a close and becomes a SHORTENING: the
-- deadline is pulled in to now + grace_seconds, the countdown on every phone
-- and the ring on the TV visibly drop to it, and the phase then closes by the
-- ordinary timer path that all three sweeping layers already share. Nothing
-- new closes a phase; one thing now moves a clock.
--
-- 5 seconds by default: long enough to read the room's cards and move a chip,
-- short enough that nobody reads it as dead air. 0 is a legal value and means
-- exactly the old behaviour -- close the instant the last table is in --
-- which is why this setting is never filled in from a default when a client
-- omits it, unlike the other timers.
ALTER TABLE app_trivia_games
    ADD COLUMN grace_seconds INT NOT NULL DEFAULT 5;

-- +goose Down

ALTER TABLE app_trivia_games DROP COLUMN grace_seconds;
