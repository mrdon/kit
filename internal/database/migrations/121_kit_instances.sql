-- +goose Up

-- One row per running Kit process, refreshed by a heartbeat. It is how a
-- process learns that a newer build is up and stops doing background work:
-- an orphaned container left behind by an unfinished deploy kept claiming
-- scheduled jobs for two weeks on code that could no longer talk to the
-- Anthropic API, and retired builtins it had never heard of.
--
-- Global rather than tenant-scoped: a process serves every tenant, so there
-- is no tenant to own the row.
CREATE TABLE kit_instances (
    id           UUID PRIMARY KEY,
    build        TEXT NOT NULL,
    built_at     TIMESTAMPTZ,
    hostname     TEXT NOT NULL,
    started_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    heartbeat_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_kit_instances_heartbeat ON kit_instances (heartbeat_at);

-- +goose Down
DROP TABLE IF EXISTS kit_instances;
