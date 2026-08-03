-- +goose Up

-- Card expiry. A card with expires_at set is swept to 'archived' once that
-- deadline passes, so short-lived cards (a daily email-intake summary, a
-- transient status note) leave the stack on their own instead of piling up
-- until someone acks each one by hand.
--
-- Applies to every card kind. NULL means never expires, which stays the
-- default: only a creator that opts in gets the behaviour.
--
-- Expiry is deliberately a state transition, not a delete. The 90-day purge
-- below is what actually reclaims rows, and it runs off terminal_at, so an
-- expired card is still auditable via list_briefings / list_decisions for a
-- quarter after it leaves the stack.
ALTER TABLE app_cards ADD COLUMN expires_at TIMESTAMPTZ;

-- Drives the expiry sweep, which runs every scheduler tick. Partial so the
-- index stays small: only pending cards that actually carry a deadline are
-- ever candidates.
CREATE INDEX idx_app_cards_expiring
    ON app_cards (expires_at)
    WHERE expires_at IS NOT NULL AND state = 'pending';

-- Drives the 90-day purge of archived cards. terminal_at is stamped by every
-- transition out of pending, so it's the age we want here (not created_at).
CREATE INDEX idx_app_cards_archived_terminal
    ON app_cards (terminal_at)
    WHERE state = 'archived';

-- +goose Down

DROP INDEX IF EXISTS idx_app_cards_archived_terminal;
DROP INDEX IF EXISTS idx_app_cards_expiring;
ALTER TABLE app_cards DROP COLUMN IF EXISTS expires_at;
