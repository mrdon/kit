-- +goose Up

-- Who, other than a person, ran a session. A scheduled job runs as its
-- owner (user_id stays the accountable human), but the record should say
-- "Morning briefing job (Don)", not "Don". NULL for sessions a person
-- drove themselves.
ALTER TABLE sessions
    ADD COLUMN actor_kind  TEXT,
    ADD COLUMN actor_label TEXT;

-- +goose Down
ALTER TABLE sessions
    DROP COLUMN IF EXISTS actor_label,
    DROP COLUMN IF EXISTS actor_kind;
