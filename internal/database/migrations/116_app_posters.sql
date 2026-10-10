-- +goose Up

-- Posters: on-brand event graphics rendered from the tenant's own photos.
--
-- Kit stores an INDEX of the photo library, not the photos: Drive is where
-- the files live, and the renderer fetches them by id when it draws. A
-- poster is TSX source, stored as versions; the only image Kit keeps is the
-- one set on an event, through the events app's hero_attachment_id.

CREATE TABLE app_poster_settings (
    tenant_id          UUID PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    photo_folder_id    TEXT NOT NULL DEFAULT '',
    logo_folder_id     TEXT NOT NULL DEFAULT '',
    -- variant -> {"file_id","name","modified"}; only the mapping is kept, the
    -- renderer fetches and trims the files.
    logo_map           JSONB NOT NULL DEFAULT '{}',
    allow_stock_photos BOOLEAN NOT NULL DEFAULT false,
    -- The brand derived from the branding-guide skill, cached by the skill
    -- content's hash so editing the skill re-derives it.
    brand_json         JSONB,
    brand_hash         TEXT NOT NULL DEFAULT '',
    brand_problems     TEXT[] NOT NULL DEFAULT '{}',
    brand_derived_at   TIMESTAMPTZ,
    last_sync_at       TIMESTAMPTZ,
    last_sync_error    TEXT NOT NULL DEFAULT '',
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE app_poster_photos (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    drive_file_id     TEXT NOT NULL,
    drive_modified_at TEXT NOT NULL DEFAULT '',
    folder            TEXT NOT NULL DEFAULT '',
    filename          TEXT NOT NULL,
    width             INTEGER NOT NULL DEFAULT 0,
    height            INTEGER NOT NULL DEFAULT 0,
    orientation       TEXT NOT NULL DEFAULT '',
    status            TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'indexed', 'removed')),
    description       TEXT NOT NULL DEFAULT '',
    tags              TEXT[] NOT NULL DEFAULT '{}',
    focus_x           REAL NOT NULL DEFAULT 0.5,
    focus_y           REAL NOT NULL DEFAULT 0.5,
    notes             TEXT NOT NULL DEFAULT '',
    -- Provenance verdict from the renderer: none, camera, ai, unknown. A
    -- photo flagged ai is indexed but never offered.
    c2pa              TEXT NOT NULL DEFAULT 'none',
    indexed_by        TEXT NOT NULL DEFAULT '',
    indexed_at        TIMESTAMPTZ,
    -- description + tags + folder + notes, maintained on write because
    -- array_to_string is not immutable and so cannot sit in an index.
    search_text       TEXT NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, drive_file_id)
);

CREATE INDEX idx_app_poster_photos_tenant ON app_poster_photos (tenant_id, status, folder);
CREATE INDEX idx_app_poster_photos_fts ON app_poster_photos USING GIN (to_tsvector('english', search_text));

-- Templates. tenant_id NULL marks a built-in shipped with Kit, visible to
-- every tenant and editable by none (duplicate to get a tenant copy). This
-- is the one deliberate exception to the tenant_id NOT NULL rule in this
-- app: built-ins are shared rows, like oauth_clients. Every read filters
-- on (tenant_id = $1 OR tenant_id IS NULL).
CREATE TABLE app_poster_templates (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id               UUID REFERENCES tenants(id) ON DELETE CASCADE,
    builtin_key             TEXT,
    name                    TEXT NOT NULL,
    description             TEXT NOT NULL DEFAULT '',
    status                  TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'active', 'archived')),
    origin                  TEXT NOT NULL DEFAULT 'chat' CHECK (origin IN ('builtin', 'poster', 'image', 'chat')),
    meta                    JSONB NOT NULL DEFAULT '{}',
    current_version_id      UUID,
    parent_template_id      UUID REFERENCES app_poster_templates(id) ON DELETE SET NULL,
    source_poster_id        UUID,
    reference_attachment_id UUID REFERENCES attachments(id) ON DELETE SET NULL,
    created_by              UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((tenant_id IS NULL) = (builtin_key IS NOT NULL))
);

CREATE UNIQUE INDEX idx_app_poster_templates_builtin ON app_poster_templates (builtin_key) WHERE tenant_id IS NULL;
CREATE INDEX idx_app_poster_templates_tenant ON app_poster_templates (tenant_id, status);

CREATE TABLE app_poster_template_versions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID REFERENCES tenants(id) ON DELETE CASCADE,
    template_id UUID NOT NULL REFERENCES app_poster_templates(id) ON DELETE CASCADE,
    parent_id   UUID REFERENCES app_poster_template_versions(id) ON DELETE SET NULL,
    source      TEXT NOT NULL,
    summary     TEXT NOT NULL DEFAULT '',
    author      TEXT NOT NULL DEFAULT 'user' CHECK (author IN ('user', 'agent', 'builtin')),
    created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_app_poster_template_versions_template ON app_poster_template_versions (template_id, created_at);

-- A tenant hides a built-in it never wants offered.
CREATE TABLE app_poster_template_hidden (
    tenant_id   UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    template_id UUID NOT NULL REFERENCES app_poster_templates(id) ON DELETE CASCADE,
    PRIMARY KEY (tenant_id, template_id)
);

CREATE TABLE app_posters (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id          UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    event_id           UUID REFERENCES app_events(id) ON DELETE SET NULL,
    title              TEXT NOT NULL DEFAULT '',
    current_version_id UUID,
    -- The version whose portrait render is on the event, and the facts its
    -- copy used. When the event's facts change, stale flips on.
    set_version_id     UUID,
    facts              JSONB,
    facts_hash         TEXT NOT NULL DEFAULT '',
    stale              BOOLEAN NOT NULL DEFAULT false,
    created_by         UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_app_posters_tenant ON app_posters (tenant_id, event_id);

-- Options are versions sharing a batch_id with no parent; picked and the
-- edits that follow feed template statistics.
CREATE TABLE app_poster_versions (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    poster_id           UUID NOT NULL REFERENCES app_posters(id) ON DELETE CASCADE,
    parent_id           UUID REFERENCES app_poster_versions(id) ON DELETE SET NULL,
    batch_id            UUID,
    template_id         UUID REFERENCES app_poster_templates(id) ON DELETE SET NULL,
    template_version_id UUID REFERENCES app_poster_template_versions(id) ON DELETE SET NULL,
    source              TEXT NOT NULL,
    content             JSONB NOT NULL DEFAULT '{}',
    photos              JSONB NOT NULL DEFAULT '[]',
    ground              TEXT NOT NULL DEFAULT '',
    format              TEXT NOT NULL DEFAULT '',
    problems            JSONB NOT NULL DEFAULT '[]',
    instruction         TEXT NOT NULL DEFAULT '',
    author              TEXT NOT NULL DEFAULT 'system' CHECK (author IN ('user', 'agent', 'system')),
    picked              BOOLEAN NOT NULL DEFAULT false,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_app_poster_versions_poster ON app_poster_versions (tenant_id, poster_id, created_at);
CREATE INDEX idx_app_poster_versions_batch ON app_poster_versions (tenant_id, batch_id);
CREATE INDEX idx_app_poster_versions_template ON app_poster_versions (template_id) WHERE picked;

-- +goose Down
DROP TABLE IF EXISTS app_poster_versions;
DROP TABLE IF EXISTS app_posters;
DROP TABLE IF EXISTS app_poster_template_hidden;
DROP TABLE IF EXISTS app_poster_template_versions;
DROP TABLE IF EXISTS app_poster_templates;
DROP TABLE IF EXISTS app_poster_photos;
DROP TABLE IF EXISTS app_poster_settings;
