-- +goose Up

-- Multi-party coordination engine: one row per workflow instance
-- (meeting scheduling, AP approval, decision quorum). Phase 1 implements
-- the meeting kind only.
CREATE TABLE app_coordinations (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organizer_id      UUID NOT NULL REFERENCES users(id),
    kind              TEXT NOT NULL CHECK (kind IN ('meeting')),
    status            TEXT NOT NULL DEFAULT 'active'
                      CHECK (status IN ('active','converged','confirmed','abandoned','cancelled')),
    config            JSONB NOT NULL,         -- kind-specific: title, duration, date range, candidate slots, organizer_windows, auto_approve
    result            JSONB,                  -- kind-specific: chosen slot, etc.
    deadline_at       TIMESTAMPTZ,
    shepherd_task_id  UUID REFERENCES tasks(id),  -- long-lived task that owns wake-ups via decision card resolution
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_coord_tenant_status ON app_coordinations (tenant_id, status);
CREATE INDEX idx_coord_deadline      ON app_coordinations (tenant_id, deadline_at)
    WHERE status = 'active' AND deadline_at IS NOT NULL;

-- Per-counterparty state machine. Outbound + inbound message audit lives
-- in session_events (via the Messenger primitive); this table holds the
-- structured per-round state needed for re-engagement decisions
-- ("what slots did we propose in round 2?").
CREATE TABLE app_coordination_participants (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    coordination_id UUID NOT NULL REFERENCES app_coordinations(id) ON DELETE CASCADE,
    identifier      TEXT NOT NULL,            -- slack user id (Phase 1)
    user_id         UUID REFERENCES users(id),  -- if the participant is a Kit user
    session_id      UUID REFERENCES sessions(id),  -- Messenger-managed session anchoring this conversation; populated on first send
    channel         TEXT NOT NULL DEFAULT 'slack',
    status          TEXT NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending','contacted','responded','timed_out','declined')),
    rounds          JSONB NOT NULL DEFAULT '[]'::jsonb,   -- [{round, asked_at, asked_slots, responded_at}, ...]
    constraints     JSONB NOT NULL DEFAULT '{}'::jsonb,   -- CURRENT (replaced, not accumulated) parser-computed constraint set
    nudge_count     INT NOT NULL DEFAULT 0,
    next_nudge_at   TIMESTAMPTZ,                          -- cron's wake-up condition
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_coord_part_wakeup  ON app_coordination_participants (tenant_id, status, next_nudge_at);
CREATE INDEX idx_coord_part_session ON app_coordination_participants (tenant_id, session_id)
    WHERE session_id IS NOT NULL AND status IN ('contacted','responded');
CREATE INDEX idx_coord_part_coord   ON app_coordination_participants (tenant_id, coordination_id);

-- +goose Down
DROP TABLE IF EXISTS app_coordination_participants;
DROP TABLE IF EXISTS app_coordinations;
