-- +goose Up

-- Kiosk boards: named screens whose content a workspace admin can repoint
-- without touching the machine. Each row is one screen ("Lobby TV"), and its
-- public URL /{slug}/kiosk/{key} 302s to whatever `url` currently holds.
--
-- The public endpoint is DELIBERATELY UNAUTHENTICATED. A board URL is a
-- workspace-visible pointer, not a secret: the kiosk machines are on a shop
-- floor with no credential store, and the thing being protected -- a dashboard
-- URL -- is already reachable by anyone who can read the screen. Anyone who
-- guesses /{slug}/kiosk/lobby learns where the lobby TV points. Do not put a
-- URL here that is itself a bearer credential (a signed share link, a
-- pre-authenticated dashboard); the redirect target leaks to anyone who asks.
--
-- `key` is the stable half of the contract and the reason this table has a
-- slug at all: it is baked into the kiosk's browser homepage and the poller's
-- config, so renaming a board must not break the machine. `name` is the
-- human label and is free to change.
--
-- `url` is nullable, and a board with no URL is a legitimate steady state --
-- a screen provisioned before anyone decided what it shows. The endpoint
-- serves a placeholder page in that case rather than 404ing, so a freshly
-- imaged kiosk pointed at its board displays something intentional.
--
-- last_seen_at is written by the public endpoint on every poll. It is the
-- only health signal the design affords: the kiosk never reports in any other
-- way, so "when did this board last get asked for" is how an admin
-- distinguishes a dead screen from a boring one. Best-effort -- a failed
-- touch never fails the redirect.
CREATE TABLE app_kiosk_boards (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    key         TEXT NOT NULL,
    name        TEXT NOT NULL,
    url         TEXT,
    notes       TEXT NOT NULL DEFAULT '',
    last_seen_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- The lookup the public endpoint does on every poll, and the uniqueness the
-- URL contract depends on: one board per (tenant, key).
CREATE UNIQUE INDEX idx_app_kiosk_boards_key
    ON app_kiosk_boards (tenant_id, key);

-- +goose Down
DROP TABLE IF EXISTS app_kiosk_boards;
