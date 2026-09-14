-- +goose Up

-- Let both chips land on the same answer.
--
-- 088 forced the spread with a unique index on (round, team, slot_id), on the
-- theory that free stacking collapses the decision: if nothing is ever lost,
-- both chips optimally go on the single likeliest card and $100 vs $200 means
-- nothing. That is true of the *optimal* line and false of the room. At a bar
-- table the spread reads as the game refusing a perfectly sensible bet — "we
-- are sure, we want both on 1969" — and a rule you meet by being rejected is
-- the worst kind. So: stacking is allowed, and the denominations become a
-- confidence dial (all in on one, or hedge across two) rather than a forced
-- split.
--
-- The token index stays unique. That one is not a rule, it is the concurrency
-- guard: a chip is in exactly one place, moving it is an UPDATE, and a
-- double-tap therefore cannot double a team's money.
DROP INDEX IF EXISTS idx_app_trivia_bets_spread;

-- +goose Down

-- NOTE: this Down FAILS if any team has stacked two chips on one card, which
-- is exactly what the Up permits. Recreating a unique index over rows that
-- violate it is a duplicate-key error, and that is the honest outcome: rolling
-- back past this point means deciding which of a team's chips to throw away,
-- and no migration should make that choice silently. Delete the stacked rows
-- by hand first if you really mean to go back.
CREATE UNIQUE INDEX idx_app_trivia_bets_spread
    ON app_trivia_bets (tenant_id, round_id, team_id, slot_id);
