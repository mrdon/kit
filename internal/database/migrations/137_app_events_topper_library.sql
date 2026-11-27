-- +goose Up

-- Ready-made table toppers: finished PDFs the bar prints whenever it likes.
--
-- The generated topper is the week's events, composed fresh on every print.
-- These are the other kind -- a happy hour card, a beer feature, a weekend
-- sale -- designed once elsewhere and kept here so staff can print another
-- copy from the bar iPad without anyone hunting for the file. Kit does not
-- draw them; it keeps them and hands them to the printer.
--
-- The bytes live in the attachment store (encrypted, tenant-scoped) and this
-- row gives them a title. Replacing a topper points the row at a new
-- attachment, so the iPad's button keeps its place and its name.
CREATE TABLE app_event_topper_library (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    title         TEXT NOT NULL,
    attachment_id UUID NOT NULL REFERENCES attachments(id) ON DELETE CASCADE,
    created_by    UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Titles are the buttons, so two with the same name would be two buttons
-- nobody can tell apart.
CREATE UNIQUE INDEX idx_app_event_topper_library_title
    ON app_event_topper_library (tenant_id, lower(title));

-- +goose Down
DROP TABLE IF EXISTS app_event_topper_library;
