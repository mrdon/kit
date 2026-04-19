-- +goose Up
-- Per-workspace PWA URLs: each tenant gets a URL slug (Slack workspace
-- domain) and its Slack team icon bytes cached for manifest serving.
--
-- Slug charset is enforced here as defense in depth — the Go layer also
-- validates before INSERT. Reserved words are blocked so a Slack workspace
-- domain like "oauth" can't shadow a top-level route.
--
-- All api_tokens are wiped: the session signing key is being bumped to v2
-- in the same deploy, so every token needs a re-login anyway. This is the
-- cookie-path migration cleanup — avoids stale Path=/ cookies persisting
-- into the new Path=/{slug}/ regime.

ALTER TABLE tenants ADD COLUMN slug TEXT;
ALTER TABLE tenants ADD COLUMN icon_192 BYTEA;
ALTER TABLE tenants ADD COLUMN icon_512 BYTEA;

-- Backfill: the current solo tenant is gravity-brewing. Any other rows
-- get a deterministic team-id-derived fallback; OAuth reinstall will
-- later overwrite with the real domain. The regex_replace calls strip
-- chars that would trip the CHECK constraint (notably underscores in
-- test-fixture slack_team_id values).
UPDATE tenants SET slug = 'gravity-brewing' WHERE slug IS NULL AND name ILIKE '%gravity%';
UPDATE tenants SET slug = trim(BOTH '-' FROM
        regexp_replace(
            regexp_replace(lower('ws-' || slack_team_id), '[^a-z0-9-]+', '-', 'g'),
            '-+', '-', 'g'
        )
    )
    WHERE slug IS NULL;

ALTER TABLE tenants ALTER COLUMN slug SET NOT NULL;
ALTER TABLE tenants ADD CONSTRAINT tenants_slug_unique UNIQUE (slug);
ALTER TABLE tenants ADD CONSTRAINT tenants_slug_format
    CHECK (slug ~ '^[a-z0-9][a-z0-9-]{0,62}$');
ALTER TABLE tenants ADD CONSTRAINT tenants_slug_reserved
    CHECK (slug NOT IN (
        'slack', 'mcp', 'oauth', 'health', 'api', 'app',
        'assets', 'admin', 'well-known', 'static', 'login'
    ));

DELETE FROM api_tokens;

-- +goose Down
ALTER TABLE tenants DROP CONSTRAINT IF EXISTS tenants_slug_reserved;
ALTER TABLE tenants DROP CONSTRAINT IF EXISTS tenants_slug_format;
ALTER TABLE tenants DROP CONSTRAINT IF EXISTS tenants_slug_unique;
ALTER TABLE tenants DROP COLUMN IF EXISTS icon_512;
ALTER TABLE tenants DROP COLUMN IF EXISTS icon_192;
ALTER TABLE tenants DROP COLUMN IF EXISTS slug;
