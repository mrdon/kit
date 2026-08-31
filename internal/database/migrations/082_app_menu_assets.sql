-- +goose Up

-- Images a menu board shows, stored once and inlined into the page at render.
--
-- This table exists because the alternative was worse in both directions. The
-- board must not fetch anything at paint time -- it hangs unattended on a wall
-- and a failed request is a broken graphic nobody is there to notice -- so the
-- image has to be inline in the HTML. But carrying it as a base64 data URI
-- inside the board payload meant a 60KB document every time somebody changed a
-- beer price, for an image that changes once a season.
--
-- So the bytes live here, keyed, and a panel refers to one by `asset:<key>`.
-- Tap lists stay small and text-only; the poster is uploaded when it changes.
--
-- Bytes in Postgres rather than object storage: these are a handful of small
-- images per workspace, and a bytea column is one fewer service to configure,
-- back up, and lose credentials for. Revisit if this ever holds real volume.
CREATE TABLE app_menu_assets (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    key        TEXT NOT NULL,
    mime       TEXT NOT NULL,
    bytes      BYTEA NOT NULL,
    source_url TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- One asset per (tenant, key); the render path looks them up by key.
CREATE UNIQUE INDEX idx_app_menu_assets_key
    ON app_menu_assets (tenant_id, key);

-- +goose Down
DROP TABLE IF EXISTS app_menu_assets;
