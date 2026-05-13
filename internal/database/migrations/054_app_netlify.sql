-- +goose Up

-- One row per tenant that has installed the Netlify website-management
-- app. Created lazily on first visit to the settings page; the row
-- exists in a "half-connected" state while the user OAuths in but
-- before they pick a site. "Fully connected" means non-NULL
-- netlify_access_token + netlify_site_id PLUS a row in
-- app_github_installations (the GitHub side is owned by the github
-- Kit app — see migration 055).
--
-- Token columns store ciphertext from internal/crypto (AES-256-GCM
-- hex-encoded), same shape as tenants.bot_token.
CREATE TABLE app_netlify_config (
    tenant_id                UUID PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    netlify_access_token     TEXT,
    netlify_refresh_token    TEXT,
    netlify_token_expires_at TIMESTAMPTZ,
    netlify_site_id          TEXT,
    netlify_site_name        TEXT,
                              -- denormalized for display in the settings page;
                              -- refreshed lazily when we read the site
    netlify_repo_owner       TEXT,
    netlify_repo_name        TEXT,
                              -- captured at site-pick time from Netlify's
                              -- build_settings.repo_url so we know which repo
                              -- the user should install the Kit GitHub App on
    production_branch        TEXT NOT NULL DEFAULT 'main',
                              -- auto-detected from Netlify site config at
                              -- connect time; overrideable for repos with
                              -- non-standard default branches
    default_agent            TEXT NOT NULL DEFAULT 'claude',
                              -- one of: claude, codex, gemini
    monthly_run_budget       INT,
                              -- cost cap (number of agent runs allowed per
                              -- calendar month); NULL = no cap
    blobs_store_name         TEXT,
                              -- per-tenant Netlify Blobs namespace for image
                              -- uploads. Created lazily on first image upload.
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down

DROP TABLE IF EXISTS app_netlify_config;
