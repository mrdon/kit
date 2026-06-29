-- +goose Up

-- Per-tenant app enable/disable. An admin turns individual feature apps on or
-- off for their workspace (e.g. some tenants get the vault, some don't).
--
-- Default is enabled: the ABSENCE of a row means the app is on. Only explicit
-- admin choices are stored, so a freshly-installed app is available everywhere
-- until someone disables it. Core apps (console, admin) are never written here —
-- they host the web UI and the admin tooling, so disabling them is refused.
CREATE TABLE tenant_app_enablement (
    tenant_id  UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    app_name   TEXT NOT NULL,
    enabled    BOOLEAN NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, app_name)
);

-- +goose Down
DROP TABLE IF EXISTS tenant_app_enablement;
