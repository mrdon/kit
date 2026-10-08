-- +goose Up

-- A pending device pairing: a browser opened /{slug}/pair and is waiting
-- for an admin, signed in on their own phone, to tap the picture it shows.
--
-- The browser holds device_code (random, in an HttpOnly cookie); only its
-- hash is stored, so reading the picture or the code off the screen lets
-- you APPROVE the pairing but never TAKE the credential: the session is
-- issued only to the poll that carries the cookie. Rows are minutes-lived
-- and swept opportunistically; this is a table rather than Redis so there
-- is one code path and tests need nothing but Postgres.
CREATE TABLE app_device_pairings (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    device_code_hash TEXT NOT NULL UNIQUE,
    user_code        TEXT NOT NULL,
    picture          TEXT NOT NULL,
    status           TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'cancelled')),
    actor_id         UUID REFERENCES actors(id) ON DELETE CASCADE,
    client_ip        TEXT NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at       TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_app_device_pairings_tenant ON app_device_pairings(tenant_id, status);

-- +goose Down
DROP TABLE IF EXISTS app_device_pairings;
