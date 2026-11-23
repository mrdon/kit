-- +goose Up

-- Holiday hours: the days the taproom keeps different hours from the usual
-- week, or is shut. One row per date. Regular weekly hours are not stored
-- here; the website still owns those.
--
-- They live beside events because the two have to agree. A closed day skips
-- every onsite repeating event that would land on it (see hours.go), and the
-- website reads both from the same feed, so it can never list trivia on a day
-- the doors are locked.
CREATE TABLE app_event_special_hours (
    tenant_id  UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    day        DATE NOT NULL,
    label      TEXT NOT NULL DEFAULT '',
    closed     BOOLEAN NOT NULL DEFAULT false,
    -- Wall-clock HH:MM in the tenant's zone. Empty when closed.
    opens      TEXT NOT NULL DEFAULT '',
    closes     TEXT NOT NULL DEFAULT '',
    note       TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, day),
    CONSTRAINT app_event_special_hours_shape CHECK (
        (closed AND opens = '' AND closes = '')
        OR (NOT closed AND opens <> '' AND closes <> '')
    )
);

-- +goose Down
DROP TABLE IF EXISTS app_event_special_hours;
