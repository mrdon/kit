-- +goose Up

-- General attachment store: file bytes attached to a chat turn or referenced
-- by a domain object (e.g. an expense line item). This is the first place Kit
-- persists original uploaded file bytes — previously uploads were extracted to
-- text and discarded. Bytes are encrypted at rest (AES-256-GCM via
-- internal/crypto, same process key as bot tokens). Tenant-scoped; every read
-- MUST filter tenant_id.
CREATE TABLE attachments (
    id          UUID PRIMARY KEY,
    tenant_id   UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    filename    TEXT NOT NULL,
    mime        TEXT NOT NULL,
    size        INTEGER NOT NULL,   -- original (decrypted) byte length
    data        BYTEA NOT NULL,     -- nonce||ciphertext
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_attachments_tenant ON attachments (tenant_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS attachments;
