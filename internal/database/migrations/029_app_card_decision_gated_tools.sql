-- +goose Up
-- Decision cards can gate a concrete tool call: an option carries the name
-- of a registered tool plus its JSON arguments, and approving the option
-- executes the tool via tools.Registry.Execute with an approval token.
-- See /home/mrdon/.claude/plans/look-at-the-decision-synchronous-comet.md

-- Per-option gated-tool payload. tool_name is the registered tool (e.g.
-- 'send_email'); tool_arguments is the JSON arg blob passed to its handler.
-- Both NULL => option is a plain prompt-only option (today's shape).
ALTER TABLE app_card_decision_options
    ADD COLUMN tool_name      TEXT,
    ADD COLUMN tool_arguments JSONB;

-- Decision-level resolve-time machinery:
--   is_gate_artifact   - true when the card was minted as the gate for a
--                        PolicyGate tool call (either by Registry.Execute
--                        auto-wrapping or by an agent's explicit
--                        create_decision with a PolicyGate tool_name).
--                        Resolve re-checks this before executing.
--   resolved_tool_result - full output of the executed tool (the resume path
--                          inlines a 2KB-truncated version into events).
--   resolved_at        - timestamp of successful resolve (distinct from
--                        terminal_at on the parent card; useful for audit).
--   resolving_deadline - now() + 5min when state='resolving'; the scheduler
--                        sweep flips past-deadline cards back to 'pending'.
--   resolve_token      - idempotency key passed to the tool handler; if the
--                        sweep requeues a wedged card, the handler uses
--                        this to avoid double-executing.
--   last_error         - error message from a failed resolve, or
--                        "timed out after Xs - requeued" from the sweep.
ALTER TABLE app_card_decisions
    ADD COLUMN is_gate_artifact    BOOLEAN     NOT NULL DEFAULT FALSE,
    ADD COLUMN resolved_tool_result TEXT,
    ADD COLUMN resolved_at         TIMESTAMPTZ,
    ADD COLUMN resolving_deadline  TIMESTAMPTZ,
    ADD COLUMN resolve_token       UUID,
    ADD COLUMN last_error          TEXT;

-- Sweep index: small/partial, only the in-flight rows.
CREATE INDEX idx_app_card_decisions_resolving_deadline
    ON app_card_decisions(resolving_deadline)
    WHERE resolving_deadline IS NOT NULL;

-- Extend app_cards.state CHECK to include 'resolving' as a transient state
-- between pending and resolved while the gated tool is executing. Postgres
-- requires drop+recreate for CHECK constraints.
ALTER TABLE app_cards DROP CONSTRAINT IF EXISTS app_cards_state_check;
ALTER TABLE app_cards
    ADD CONSTRAINT app_cards_state_check
    CHECK (state IN ('pending', 'resolving', 'resolved',
                     'archived', 'dismissed', 'saved', 'cancelled'));

-- +goose Down
ALTER TABLE app_cards DROP CONSTRAINT IF EXISTS app_cards_state_check;
ALTER TABLE app_cards
    ADD CONSTRAINT app_cards_state_check
    CHECK (state IN ('pending', 'resolved',
                     'archived', 'dismissed', 'saved', 'cancelled'));

DROP INDEX IF EXISTS idx_app_card_decisions_resolving_deadline;

ALTER TABLE app_card_decisions
    DROP COLUMN IF EXISTS last_error,
    DROP COLUMN IF EXISTS resolve_token,
    DROP COLUMN IF EXISTS resolving_deadline,
    DROP COLUMN IF EXISTS resolved_at,
    DROP COLUMN IF EXISTS resolved_tool_result,
    DROP COLUMN IF EXISTS is_gate_artifact;

ALTER TABLE app_card_decision_options
    DROP COLUMN IF EXISTS tool_arguments,
    DROP COLUMN IF EXISTS tool_name;
