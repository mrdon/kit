-- +goose Up

-- Iterative negotiation: track round count and per-participant
-- free-form availability text. The free-form availability replaces
-- the discrete-slot-voting model — each participant's availability
-- is whatever natural language they've stated, accumulated across
-- rounds. The LLM is the constraint solver.

ALTER TABLE app_coordinations
    ADD COLUMN round_count INT NOT NULL DEFAULT 0;

ALTER TABLE app_coordination_participants
    ADD COLUMN availability TEXT NOT NULL DEFAULT '';

-- accepted_time records a participant's explicit yes to a specific
-- proposed time (RFC3339 string). Convergence requires all active
-- participants to have accepted_time pointing at the same string.
ALTER TABLE app_coordination_participants
    ADD COLUMN accepted_time TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE app_coordination_participants DROP COLUMN IF EXISTS accepted_time;
ALTER TABLE app_coordination_participants DROP COLUMN IF EXISTS availability;
ALTER TABLE app_coordinations DROP COLUMN IF EXISTS round_count;
