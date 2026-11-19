-- +goose Up

-- The menu's nightly Square check files a decision card for each gap it
-- cannot close by itself (a price that disagrees, a near-miss name). One row
-- per question asked, keyed by what was found, so the same gap is asked once
-- however many nights it stands, and a changed price asks again.
CREATE TABLE app_menu_square_asked (
    tenant_id  UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    key        TEXT NOT NULL,
    card_id    UUID,
    asked_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, key)
);

-- +goose Down
DROP TABLE IF EXISTS app_menu_square_asked;
