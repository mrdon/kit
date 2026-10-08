-- +goose Up

-- Actors: the things that act on Kit that are not people.
--
-- Until now every caller was squeezed into the shape of a Slack user. A
-- paired device (the trivia laptop, the bar iPad) is not a person: nobody
-- signs in on it, it holds a short list of capabilities instead of roles,
-- and the human who approved it is a sponsor, not the one accountable for
-- each tap. Users are NOT migrated into this table; a user_id anywhere
-- still means a person.
CREATE TABLE actors (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    kind            TEXT NOT NULL CHECK (kind IN ('device', 'agent', 'widget')),
    label           TEXT NOT NULL,
    capabilities    TEXT[] NOT NULL DEFAULT '{}',
    sponsor_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at    TIMESTAMPTZ,
    revoked_at      TIMESTAMPTZ
);

CREATE INDEX idx_actors_tenant ON actors(tenant_id);

-- A device holds an ordinary api_tokens row (one cookie, one resolver), so
-- the row learns whose it is: a user OR an actor, never both or neither.
-- kind and label are what a "your sessions" page would list.
ALTER TABLE api_tokens
    ADD COLUMN kind         TEXT NOT NULL DEFAULT 'session' CHECK (kind IN ('session', 'mcp', 'device')),
    ADD COLUMN label        TEXT NOT NULL DEFAULT '',
    ADD COLUMN actor_id     UUID REFERENCES actors(id) ON DELETE CASCADE,
    ADD COLUMN last_used_at TIMESTAMPTZ,
    ADD COLUMN revoked_at   TIMESTAMPTZ;

ALTER TABLE api_tokens ALTER COLUMN user_id DROP NOT NULL;

ALTER TABLE api_tokens
    ADD CONSTRAINT api_tokens_one_principal CHECK (num_nonnulls(user_id, actor_id) = 1);

-- Existing rows: the OAuth token endpoint mints 90-day tokens, sessions
-- 30 days or less, so lifetime tells them apart.
UPDATE api_tokens SET kind = 'mcp' WHERE expires_at - created_at > interval '60 days';

CREATE INDEX idx_api_tokens_actor ON api_tokens(actor_id) WHERE actor_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_api_tokens_actor;
ALTER TABLE api_tokens DROP CONSTRAINT IF EXISTS api_tokens_one_principal;
DELETE FROM api_tokens WHERE user_id IS NULL;
ALTER TABLE api_tokens ALTER COLUMN user_id SET NOT NULL;
ALTER TABLE api_tokens
    DROP COLUMN IF EXISTS revoked_at,
    DROP COLUMN IF EXISTS last_used_at,
    DROP COLUMN IF EXISTS actor_id,
    DROP COLUMN IF EXISTS label,
    DROP COLUMN IF EXISTS kind;
DROP TABLE IF EXISTS actors;
