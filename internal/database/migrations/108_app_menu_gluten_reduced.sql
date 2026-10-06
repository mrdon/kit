-- +goose Up

-- Which beers are gluten reduced.
--
-- It is a fact about a beer's recipe, not about a tap, so it is kept here by
-- beer name and outlives the beer going off tap: when a gluten reduced beer
-- comes back, the wall and the printed menu mark it again without anyone
-- remembering to. Untappd has no field for it, and the tap list is not ours
-- to annotate -- it is replaced wholesale on every sync -- so this is applied
-- at render time, the same way the New badge and happy hour are.
--
-- beers is a JSON array of names as the menu board shows them, matched
-- ignoring only case and spacing. A dietary mark that lands on the wrong beer
-- is worse than one that is missing, so there is no loose matching.
CREATE TABLE app_menu_gluten_reduced (
    tenant_id  UUID PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    beers      JSONB NOT NULL DEFAULT '[]'::jsonb,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose Down
DROP TABLE IF EXISTS app_menu_gluten_reduced;
