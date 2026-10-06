-- +goose Up

-- Happy hour: one setting that both the register and the wall follow.
--
-- A happy hour lives in two places that used to be configured by hand and
-- separately: Square, which rings the discount, and the menu board, which
-- tells the room. Setting them apart is how a wall ends up advertising a $5
-- pint the register charges $6.50 for. So the schedule, the price and the
-- beers are stored once, here, and both sides are derived from it.
--
-- config is the setting itself: days, start and end time, the price, the pour
-- size and the beers. The board reads it on every render, so the screen flips
-- on and off with the clock and needs nothing pushed to it.
--
-- Square is different: it is written, not read, and only when someone asks.
-- square_ids are the catalog objects the last sync created (the time period,
-- and a discount, product set and pricing rule per discount amount). The next
-- sync creates their replacements first and only then deletes these, so a
-- failed sync leaves the last good happy hour ringing rather than none.
--
-- synced_hash is the config as of that sync, so the console can say "Square is
-- out of date" when the setting has moved since, and sync_log is the whole
-- output of the last attempt -- including Square's own error body -- because
-- the first failure here is expected to be a token scope, and that is only
-- debuggable if somebody can read what Square actually said.
CREATE TABLE app_menu_happy_hour (
    tenant_id   UUID PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    config      JSONB NOT NULL DEFAULT '{}'::jsonb,
    square_ids  JSONB NOT NULL DEFAULT '[]'::jsonb,
    synced_hash TEXT NOT NULL DEFAULT '',
    synced_at   TIMESTAMPTZ,
    sync_log    TEXT NOT NULL DEFAULT '',
    sync_ok     BOOLEAN NOT NULL DEFAULT false,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose Down
DROP TABLE IF EXISTS app_menu_happy_hour;
