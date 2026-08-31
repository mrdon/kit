-- +goose Up

-- Menu boards: a rendered tap list for a screen in the taproom.
--
-- The whole board is one JSONB document rather than normalised tap/panel
-- tables, and that is a deliberate v1 choice. The authoring surface today is a
-- file in another repo that gets pushed up whole; there is no console UI that
-- edits one beer, so rows would buy nothing and cost a schema migration every
-- time the board grows a field. When editing moves into the console, that is
-- the moment to normalise -- not before.
--
-- The public endpoint is DELIBERATELY UNAUTHENTICATED, for the same reason as
-- app_kiosk_boards: the screen it feeds has no credential store, and the thing
-- being protected is a list of beers and their prices that is painted ten feet
-- tall on a wall. Anyone who guesses /{slug}/menu/taproom learns what is on
-- tap. Do not put anything in `payload` that is not already public to anyone
-- standing in the room.
--
-- `key` is the stable half of the contract, exactly as in the kiosk app: it
-- goes into a kiosk board's URL field and must survive the board being
-- renamed. `name` is the human label and is free to change.
CREATE TABLE app_menu_boards (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    key        TEXT NOT NULL,
    name       TEXT NOT NULL,
    payload    JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- The lookup the public endpoint does on every screen load, and the
-- uniqueness the URL contract depends on: one board per (tenant, key).
CREATE UNIQUE INDEX idx_app_menu_boards_key
    ON app_menu_boards (tenant_id, key);

-- +goose Down
DROP TABLE IF EXISTS app_menu_boards;
