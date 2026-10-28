-- +goose Up

-- Beers that are 86'd: still on the Untappd board, but out.
--
-- Untappd's API is closed to us, so a blown keg stays on the board until
-- somebody edits it there, and that is not something done mid-rush. This list
-- hides a beer from the wall and the printed menu until Untappd catches up.
-- It is about a keg, not a recipe, so unlike the gluten reduced list it does
-- not outlive the tap: a sync that no longer sees a beer drops it from here,
-- and the next keg of it is not hidden.
--
-- beers is a JSON array of names as the menu board shows them, matched
-- ignoring only case and spacing.
CREATE TABLE app_menu_eighty_six (
    tenant_id  UUID PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    beers      JSONB NOT NULL DEFAULT '[]'::jsonb,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose Down
DROP TABLE IF EXISTS app_menu_eighty_six;
