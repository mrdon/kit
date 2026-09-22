-- +goose Up

-- A second board, and a break in the middle of the night.
--
-- One board plus a final was a half-hour game. A pub quiz is an hour, and the
-- hour wants a seam in it: people get up, get a drink, argue about question
-- four. Filling the hour by making ONE board bigger was the wrong lever --
-- five rows meant five cell values, and the ladder that implies (100 up to
-- 500) is a lie in this game, because questions carry no difficulty. A
-- question's row is decided by a shuffle, so a dearer row is not a harder
-- one, and a $500 cell against a $200 chip quietly inverts the whole design:
-- only the table that WROTE the winning answer takes a cell, but every table
-- bets every round, so the betting is meant to be the bigger channel.
--
-- So the night grows along the round axis instead. Each round is its own
-- small board with its own categories, every cell in it worth the same -- no
-- ladder, nothing to imply a difficulty that is not there -- and the whole
-- round doubles: round one is $100 cells and $100/$200 chips, round two is
-- $200 cells and $200/$400 chips. That is Double Jeopardy's actual job,
-- which is comeback potential: a table that had a bad first half is still
-- live after the break because everything after it swings twice as hard.
--
-- Crucially this adds NO RULE. The room is told the same five lines; it just
-- plays them twice with a break. See Rules() -- if the game cannot be
-- explained in those lines, the game is too complicated.
--
-- Defaults to 1, so every game that already exists is exactly what it was.
ALTER TABLE app_trivia_games
    ADD COLUMN board_rounds INT NOT NULL DEFAULT 1;

-- +goose Down

ALTER TABLE app_trivia_games DROP COLUMN board_rounds;
