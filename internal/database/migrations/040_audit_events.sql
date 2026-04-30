-- +goose Up

-- General-purpose audit log. Distinct from session_events (which is
-- session-scoped and used by chat/coordination flows) — this table records
-- security-relevant actions that don't naturally have a session, especially
-- HTTP-driven web actions. App-namespaced action strings (e.g. "vault.unlock",
-- "integrations.token_rotated") let multiple apps share the table without
-- collision.
CREATE TABLE audit_events (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    actor_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
                                            -- nullable: system actions / unauthenticated probes
    action        TEXT NOT NULL,            -- "<app>.<verb>", e.g. "vault.unlock_failed"
    target_kind   TEXT,                     -- e.g. "vault_entry", "vault_user"
    target_id     UUID,                     -- the entity acted on (when applicable)
    metadata      JSONB NOT NULL DEFAULT '{}'::jsonb,
                                            -- per-action shape; populated by typed constructors
    ip            INET,                     -- captured for security-relevant events
    user_agent    TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_audit_events_recent
    ON audit_events (tenant_id, created_at DESC);
CREATE INDEX idx_audit_events_target
    ON audit_events (tenant_id, target_kind, target_id)
    WHERE target_id IS NOT NULL;
CREATE INDEX idx_audit_events_action
    ON audit_events (tenant_id, action, created_at DESC);
CREATE INDEX idx_audit_events_actor
    ON audit_events (tenant_id, actor_user_id, created_at DESC)
    WHERE actor_user_id IS NOT NULL;

-- Append-only enforcement. Kit uses a single DB role for migrations and
-- runtime, so GRANT/REVOKE-based append-only doesn't apply. A trigger
-- catches accidental UPDATE/DELETE from app code (only the migration owner
-- would bypass it via DROP TRIGGER, which is itself an audit signal).
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION audit_events_append_only()
RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'audit_events is append-only (operation: %)', TG_OP;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER audit_events_no_update
    BEFORE UPDATE ON audit_events
    FOR EACH ROW EXECUTE FUNCTION audit_events_append_only();

CREATE TRIGGER audit_events_no_delete
    BEFORE DELETE ON audit_events
    FOR EACH ROW EXECUTE FUNCTION audit_events_append_only();

-- +goose Down
DROP TRIGGER IF EXISTS audit_events_no_delete ON audit_events;
DROP TRIGGER IF EXISTS audit_events_no_update ON audit_events;
DROP FUNCTION IF EXISTS audit_events_append_only();
DROP TABLE IF EXISTS audit_events;
