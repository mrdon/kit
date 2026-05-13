-- +goose Up

-- One row per tenant that has installed the Kit GitHub App. Owned by
-- the github Kit app (internal/apps/github) and read by any other
-- feature that needs GitHub access — today the netlify website-
-- management app; tomorrow PR-decisions, issue-tasks, etc. Same
-- pattern as the shared Slack bot (one install per workspace, used by
-- every feature that needs Slack).
--
-- installation_id is not secret (it's a public GitHub App install
-- reference). Per-call installation tokens are 1-hour TTL and minted
-- on demand from the App private key — never stored.
CREATE TABLE app_github_installations (
    tenant_id           UUID PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    installation_id     BIGINT NOT NULL,
    account_login       TEXT,
                         -- GitHub user or org name; nullable in v1 because
                         -- we don't call /app/installations/<id> at install
                         -- time. Lazy-populated by the first feature that
                         -- needs it.
    account_type        TEXT,
                         -- 'User' or 'Organization' (verbatim from GitHub).
                         -- Nullable for the same reason.
    installed_by_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down

DROP TABLE IF EXISTS app_github_installations;
