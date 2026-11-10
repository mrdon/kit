-- +goose Up

-- Kit's edits to the Untappd tap list, read by the wall and the printed menu
-- alike: items Untappd does not carry (cans, sodas) and what is hidden.
--
-- items are the rows Kit adds, each with its section, sizes and prices, and a
-- `show` of print, board or both (empty is both). hidden lists beer and
-- section names kept off both menus until unhidden; unlike an 86 it survives
-- Untappd dropping the beer.
--
-- The items used to be the printed menu's `extras`, so they move here. Rows
-- that predate `show` were paper-only, and say so explicitly now that empty
-- means both.
CREATE TABLE app_menu_edits (
    tenant_id  UUID PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    items      JSONB NOT NULL DEFAULT '[]'::jsonb,
    hidden     JSONB NOT NULL DEFAULT '{"items": [], "sections": []}'::jsonb,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO app_menu_edits (tenant_id, items)
SELECT tenant_id,
       COALESCE((SELECT jsonb_agg(CASE WHEN COALESCE(e->>'show', '') = ''
                                       THEN e || '{"show": "print"}'::jsonb ELSE e END)
                   FROM jsonb_array_elements(config->'extras') AS e), '[]'::jsonb)
  FROM app_menu_print
 WHERE jsonb_typeof(config->'extras') = 'array';

UPDATE app_menu_print SET config = config - 'extras';

-- The printed menu's rows now come from the wall's own sync, which skips a
-- pull whose page is unchanged. Clearing the hash makes the first pull after
-- this deploy fill them in rather than waiting for the next keg change.
UPDATE app_menu_boards SET source_hash = '';

-- +goose Down
UPDATE app_menu_print p
   SET config = p.config || jsonb_build_object('extras', e.items)
  FROM app_menu_edits e
 WHERE e.tenant_id = p.tenant_id;
DROP TABLE IF EXISTS app_menu_edits;
