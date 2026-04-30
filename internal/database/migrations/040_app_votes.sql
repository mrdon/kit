-- +goose Up

-- Standalone proposal vote: organizer poses a yes/no/abstain question
-- to a named participant list. Each participant gets a decision card
-- in their swipe feed; on resolve their verdict (and any chat-attached
-- reason) is recorded against this row. When everyone's resolved or
-- the deadline hits, the engine surfaces a digest decision card to the
-- organizer who picks accept / reject (and optionally broadcasts a
-- briefing card to participants).
CREATE TABLE app_votes (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    organizer_id  UUID NOT NULL REFERENCES users(id),
    title         TEXT NOT NULL,
    proposal_text TEXT NOT NULL,
    context_notes TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT 'active'
                  CHECK (status IN ('active','confirmed','abandoned','cancelled')),
    deadline_at   TIMESTAMPTZ NOT NULL,
    -- Tally + organizer's resolution action populated when the digest
    -- card surfaces and is resolved. Shape: {tally:{approve:N,...},
    -- action:"accept"|"accept_and_share"|"reject"|"reject_and_announce"}
    outcome       JSONB,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_vote_tenant_status ON app_votes (tenant_id, status);
CREATE INDEX idx_vote_deadline      ON app_votes (tenant_id, deadline_at)
    WHERE status = 'active';

-- One row per participant. verdict + reason populated on card resolve;
-- vote_card_id is the decision card we surfaced for them at start
-- time (used for idempotency on re-runs and for reading the
-- card-scoped chat for the optional reason).
CREATE TABLE app_vote_participants (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    vote_id       UUID NOT NULL REFERENCES app_votes(id) ON DELETE CASCADE,
    identifier    TEXT NOT NULL,                          -- slack user id
    user_id       UUID REFERENCES users(id),              -- kit user, if known
    vote_card_id  UUID,                                   -- the per-participant decision card
    verdict       TEXT CHECK (verdict IN ('approve','object','abstain')),
    reason        TEXT NOT NULL DEFAULT '',
    responded_at  TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_vote_part_vote     ON app_vote_participants (tenant_id, vote_id);
CREATE INDEX idx_vote_part_card     ON app_vote_participants (tenant_id, vote_card_id)
    WHERE vote_card_id IS NOT NULL;

-- +goose Down
DROP TABLE IF EXISTS app_vote_participants;
DROP TABLE IF EXISTS app_votes;
