-- +goose Up

-- app_calendars: configured iCal sources
CREATE TABLE app_calendars (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    url TEXT NOT NULL,
    timezone TEXT NOT NULL DEFAULT 'UTC',
    last_sync_at TIMESTAMPTZ,
    last_sync_status TEXT NOT NULL DEFAULT 'pending',
    last_sync_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(tenant_id, name)
);

-- app_calendar_scopes: role-based access (reuses standard scope pattern)
CREATE TABLE app_calendar_scopes (
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    calendar_id UUID NOT NULL REFERENCES app_calendars(id) ON DELETE CASCADE,
    scope_type TEXT NOT NULL,
    scope_value TEXT NOT NULL,
    PRIMARY KEY(tenant_id, calendar_id, scope_type, scope_value)
);

-- app_calendar_events: parsed event occurrences
CREATE TABLE app_calendar_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    calendar_id UUID NOT NULL REFERENCES app_calendars(id) ON DELETE CASCADE,
    uid TEXT NOT NULL,
    summary TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    location TEXT NOT NULL DEFAULT '',
    start_time TIMESTAMPTZ NOT NULL,
    end_time TIMESTAMPTZ NOT NULL,
    all_day BOOLEAN NOT NULL DEFAULT false,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(tenant_id, calendar_id, uid)
);

CREATE INDEX idx_app_calendar_events_tenant_cal_start
    ON app_calendar_events(tenant_id, calendar_id, start_time);

CREATE INDEX idx_app_calendar_events_fts
    ON app_calendar_events
    USING gin(to_tsvector('english',
        coalesce(summary, '') || ' ' || coalesce(description, '') || ' ' || coalesce(location, '')));

-- +goose Down
DROP TABLE IF EXISTS app_calendar_events;
DROP TABLE IF EXISTS app_calendar_scopes;
DROP TABLE IF EXISTS app_calendars;
