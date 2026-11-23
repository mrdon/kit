-- +goose Up

-- Where a workspace's end-of-night Hoppers ratings are posted. Its own
-- setting, separate from trivia's: the two games can report to different
-- channels. Ratings are not stored; Slack is the record.
CREATE TABLE app_hoppers_settings (
    tenant_id             UUID PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    feedback_channel_id   TEXT NOT NULL DEFAULT '',
    feedback_channel_name TEXT NOT NULL DEFAULT '',
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One row per finished night: written in the same transaction that takes
-- the engine to its final standings. The standings are copied out (names
-- included) rather than read back from the engine, so the history outlives
-- the game: deleting a game leaves its night here, with game_id cleared.
CREATE TABLE app_hoppers_results (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    game_id      UUID REFERENCES app_hoppers_games(id) ON DELETE SET NULL,
    title        TEXT NOT NULL,
    -- [{name, rank, points, wins}], best first, people only.
    standings    JSONB NOT NULL,
    finished_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_app_hoppers_results_recent ON app_hoppers_results (tenant_id, finished_at DESC);

-- +goose Down

DROP TABLE app_hoppers_results;
DROP TABLE app_hoppers_settings;
