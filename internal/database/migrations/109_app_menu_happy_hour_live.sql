-- +goose Up

-- Start now / End now.
--
-- The schedule says when happy hour switches on and off; these say the same
-- thing by hand, and the most recent of the two wins. live holds the last
-- press -- {"on": true, "at": "..."} -- or NULL when nobody has pressed one.
-- It is kept out of config on purpose: config is what Square is built from,
-- and ending happy hour early is not a change to which beers are on it.
ALTER TABLE app_menu_happy_hour ADD COLUMN live JSONB;

-- +goose Down
ALTER TABLE app_menu_happy_hour DROP COLUMN IF EXISTS live;
