-- +goose Up

-- Hoppers: a frog race for a bar. Every phone flicks its own frog along a
-- track of beer coasters on the TV, a race at a time, over a night of rounds.
--
-- Unlike trivia, the game's rules live in a pure Go engine
-- (internal/apps/hoppers/engine), so a game is ONE ROW holding the engine's
-- whole saved state as JSONB. Every change is load -> apply -> save inside a
-- transaction that holds the row lock, which keeps the engine free of SQL and
-- lets the same engine run headless in the simulator. The row is still the
-- authority: any web process can pick a game up from it, and a restart
-- mid-race loses nothing.
CREATE TABLE app_hoppers_games (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    -- The verb-animal name in the URL (/{slug}/hoppers/jumping-lion).
    name           TEXT NOT NULL,
    title          TEXT NOT NULL DEFAULT '',
    -- The short code behind the QR (/j/h/{code}). Global, because that URL
    -- carries no workspace.
    join_code      TEXT NOT NULL,
    -- Which built-in pack the night was made from, for the console's list.
    pack           TEXT NOT NULL DEFAULT '',
    -- engine.Game.Save(): rules, cup, players, RNG and every timer.
    engine         JSONB NOT NULL,
    -- engine.Game.Version(): the one counter behind the SSE id, the poll
    -- cursor and every client's stale-frame check. Mirrored out of the blob
    -- so a poll can answer 204 without decoding it.
    state_version  BIGINT NOT NULL DEFAULT 0,
    -- engine.Phase (lobby, racing, final), mirrored for listing.
    phase          TEXT NOT NULL,
    -- engine.Game.NextDeadline(): when the game next moves on its own (a
    -- countdown, a bot's hop, a flick timeout, a phone timing out). NULL when
    -- it is waiting on a person. The sweeper sleeps until the earliest one.
    next_due_at    TIMESTAMPTZ,
    created_by     UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_app_hoppers_games_name ON app_hoppers_games (tenant_id, name);
CREATE UNIQUE INDEX idx_app_hoppers_games_join_code ON app_hoppers_games (join_code);
CREATE INDEX idx_app_hoppers_games_recent ON app_hoppers_games (tenant_id, created_at DESC);
-- The sweeper's query: only games something is waiting on.
CREATE INDEX idx_app_hoppers_games_due ON app_hoppers_games (next_due_at)
    WHERE next_due_at IS NOT NULL;

-- One row per phone that joined. The frog itself (name, colour, hat, score)
-- is in the engine; this is the identity behind it. The row's id is the
-- engine's player id.
--
-- token_hash is the SHA-256 of the phone's cookie token (livegame), never
-- the token. A host-issued reclaim code replaces it until redeemed.
--
-- last_seen_at is presence: every open stream touches it on each ping, so a
-- heartbeat is one small UPDATE here rather than a rewrite of the engine
-- blob. The next engine transaction replays it into the engine.
CREATE TABLE app_hoppers_players (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    game_id       UUID NOT NULL REFERENCES app_hoppers_games(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    token_hash    TEXT NOT NULL,
    last_seen_at  TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_app_hoppers_players_game ON app_hoppers_players (tenant_id, game_id);

-- +goose Down

DROP TABLE app_hoppers_players;
DROP TABLE app_hoppers_games;
