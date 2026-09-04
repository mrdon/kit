-- +goose Up

-- The printed menu's own state, kept apart from the wall board's.
--
-- Both are renderings of the same tap list, but they need different things and
-- change on different clocks. The board is a screen that must never be stale;
-- the paper is a document somebody prints on a Thursday and puts on tables for
-- a week. Folding the paper's settings into the board payload would mean every
-- price change rewrote the footer text, and every layout tweak touched the row
-- a TV depends on. So the board keeps its shape and the paper gets a table.
--
-- config holds the chrome the tap list cannot supply: the masthead wording,
-- the flight strap, the wifi and social lines, a colour per section, the
-- brewery's untappd.com slug, and the rows Untappd has no opinion about --
-- canned non-alcoholics, sodas, juice boxes, which are typed once and printed
-- alongside the beers.
--
-- notes caches beer descriptions, keyed by a normalised beer name. They come
-- from a second scrape against the consumer site, because the digital board's
-- template carries no prose at all. Caching them here means a beer is fetched
-- once rather than on every print, and it makes the stored copy authoritative:
-- a description corrected in Kit is not overwritten from upstream.
CREATE TABLE app_menu_print (
    tenant_id  UUID PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    config     JSONB NOT NULL DEFAULT '{}'::jsonb,
    notes      JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose Down
DROP TABLE IF EXISTS app_menu_print;
