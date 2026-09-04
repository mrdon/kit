-- +goose Up

-- The printed menu stops fetching at print time.
--
-- It used to scrape Untappd on the way to the PDF, which put a third party on
-- the critical path of somebody standing at a printer, and made every failure
-- silent: the sheet came out with no descriptions and nothing said why. Worse,
-- the descriptions live on untappd.com rather than on the digital board, and
-- that host answers a datacenter IP with a Cloudflare challenge -- so the fetch
-- could never succeed from the server at all, and the only symptom was prose
-- quietly missing from the paper.
--
-- So the tap list becomes config, like the wording and the colours already
-- were, and a Sync action fills it in. Printing reads what is stored and
-- touches nothing else. Sync is a deliberate act, done in front of a person,
-- and it reports what it could not reach.
--
-- rows is the tap list as of that sync. It sits beside config rather than
-- inside it because the two have different writers: a person edits config
-- through a form that knows nothing about beers, and a sync replaces the beers
-- without touching the wording. Folding them into one document would mean
-- saving the masthead could clobber the tap list, which is exactly the kind of
-- loss nobody notices until the sheet comes off the printer wrong.
--
-- synced_at and sync_error mirror the wall board's pair on purpose: the same
-- question is being answered -- when did this last agree with upstream, and
-- what went wrong if it did not -- and an operator should not have to learn
-- two shapes for one idea.
ALTER TABLE app_menu_print
    ADD COLUMN rows       JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN synced_at  TIMESTAMPTZ,
    ADD COLUMN sync_error TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE app_menu_print
    DROP COLUMN IF EXISTS rows,
    DROP COLUMN IF EXISTS synced_at,
    DROP COLUMN IF EXISTS sync_error;
