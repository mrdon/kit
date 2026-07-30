-- +goose Up

-- Events Kit owns: public taproom events (trivia, live music, releases),
-- private bookings, and offsite appearances. Unlike app_squareshifts_map --
-- which mirrors an external system -- these rows ARE the record. The Google
-- Calendar and the website feed are both derived from them.
--
-- THREE INDEPENDENT AXES, deliberately not one `type` enum. The real cases
-- vary along all three, so an enum would need the cross product:
--   visibility   public | private   -- does it reach the public feed at all
--   venue        onsite | offsite   -- offsite = a festival we attend
--   space_impact none   | partial   -- what a bartender needs to know
-- A private party is private/onsite/partial; a beer festival we pour at is
-- public/offsite/none -- public because we do want it on the website.
--
-- visibility DEFAULTS TO 'private' and is the ONLY gate on public exposure.
-- Leaking a customer's birthday party onto the brewery's website is the worst
-- failure this app can produce, so the safe value is the default and
-- publishing is the explicit act. Note visibility does NOT gate the calendar:
-- every event lands on the single shared team calendar (which is also shared
-- with the food partner, who caters the private ones). Only the feed filters
-- on it, via one tested predicate.
--
-- `status` is the LIFECYCLE axis and is orthogonal to visibility. 'published'
-- means CONFIRMED, not public -- a private party is published the moment it's
-- on the books. 'draft' is nowhere at all: not on the calendar, not in the
-- feed, so an event can be revised repeatedly before anyone sees it.
--
-- price_cents / capacity / registration_url are nullable ATTRIBUTES, not a
-- fourth axis: free-but-limited (six seats at a D&D table) and
-- paid-but-unlimited (a $5 cover) are both real, so a `paid` boolean would rot
-- immediately. NULL means "doesn't apply".
--
-- rrule holds one RFC 5545 recurrence for the single genuinely recurring event
-- (weekly trivia, identical every week). Only FREQ=WEEKLY is accepted, at the
-- service layer -- anything the in-process expander can't read is rejected on
-- write rather than stored and silently mis-expanded later. Live music is NOT
-- recurring: each night is a distinct event with its own band.
--
-- timezone is a NAMED IANA zone, never a fixed offset. It is passed straight
-- to Google as start.timeZone/end.timeZone and is the zone our own expander
-- expands in, which is what keeps 7pm trivia at 7pm across a DST boundary.
--
-- gcal_* are sync state, deliberately columns here rather than a mapping
-- table: the event id IS the key (calendar ids are derived from it), so a join
-- table would carry only the hash -- and would cascade away on delete,
-- destroying the only record that a Google event still needs removing.
CREATE TABLE app_events (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,

    title               TEXT NOT NULL,
    -- Stable public identity. The canonical URL is derived from this plus
    -- app_event_settings.public_url_template, and that URL is what gets posted
    -- to Instagram and newsletters -- so the service layer freezes the slug
    -- once status leaves 'draft'. Cancelled rows keep their slug reserved so a
    -- new event can never inherit a URL pointing at different content.
    slug                TEXT NOT NULL,
    summary             TEXT NOT NULL DEFAULT '',
    description         TEXT NOT NULL DEFAULT '',
    -- INTERNAL bartender brief. Rendered into the calendar event's description
    -- (staff and the food partner read that calendar) but NEVER serialised to
    -- the public feed. Calendar is not feed.
    prep_notes          TEXT NOT NULL DEFAULT '',

    starts_at           TIMESTAMPTZ NOT NULL,
    ends_at             TIMESTAMPTZ,
    all_day             BOOLEAN NOT NULL DEFAULT false,
    timezone            TEXT NOT NULL,
    rrule               TEXT,

    location            TEXT NOT NULL DEFAULT '',
    hero_attachment_id  UUID REFERENCES attachments(id) ON DELETE SET NULL,

    status              TEXT NOT NULL DEFAULT 'draft',
    visibility          TEXT NOT NULL DEFAULT 'private',
    venue               TEXT NOT NULL DEFAULT 'onsite',
    -- 'buyout' is deliberately absent: a full taproom takeover has never
    -- happened at this venue, and the conflict-checking it would justify is
    -- descoped. Adding it later is an additive CHECK change.
    space_impact        TEXT NOT NULL DEFAULT 'none',

    -- Defaulted in the service layer to (visibility='public' AND
    -- venue='onsite') at create time, then freely overridable -- a private
    -- party may well want the food truck and a festival never does. A column
    -- default cannot reference sibling columns, so it is false here.
    notify_food_partner BOOLEAN NOT NULL DEFAULT false,

    price_cents         BIGINT,
    currency            TEXT NOT NULL DEFAULT 'USD',
    capacity            INTEGER,      -- informational; the ticketing side enforces caps
    expected_attendance INTEGER,      -- the number the food partner actually plans around
    registration_url    TEXT,
    square_variation_id TEXT,         -- forward hook for inventory-backed ticketing

    gcal_event_id       TEXT NOT NULL DEFAULT '',
    -- The calendar actually written to, not the currently configured one.
    -- Repointing app_event_settings.calendar_id would otherwise strand every
    -- existing event on the old calendar: reconcile queries the new one, never
    -- sees them, and leaves them behind forever.
    gcal_calendar_id    TEXT NOT NULL DEFAULT '',
    gcal_content_hash   TEXT NOT NULL DEFAULT '',

    created_by          UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (tenant_id, slug),
    CONSTRAINT app_events_status_valid
        CHECK (status IN ('draft', 'published', 'cancelled')),
    CONSTRAINT app_events_visibility_valid
        CHECK (visibility IN ('public', 'private')),
    CONSTRAINT app_events_venue_valid
        CHECK (venue IN ('onsite', 'offsite')),
    CONSTRAINT app_events_space_impact_valid
        CHECK (space_impact IN ('none', 'partial')),
    CONSTRAINT app_events_end_after_start
        CHECK (ends_at IS NULL OR ends_at >= starts_at),
    -- space_impact is only meaningful onsite; nobody is holding the taproom
    -- while we pour at someone else's festival.
    CONSTRAINT app_events_offsite_no_space_impact
        CHECK (venue = 'onsite' OR space_impact = 'none'),
    -- Recurring all-day events are not supported in v1. RFC 5545 requires
    -- UNTIL to be a DATE for an all-day DTSTART and a UTC DATE-TIME for a
    -- timed one; supporting both shapes doubles the validation surface for a
    -- case that does not exist here. Relaxing this later is additive.
    CONSTRAINT app_events_no_recurring_all_day
        CHECK (rrule IS NULL OR all_day = false),
    CONSTRAINT app_events_price_non_negative
        CHECK (price_cents IS NULL OR price_cents >= 0),
    CONSTRAINT app_events_capacity_positive
        CHECK (capacity IS NULL OR capacity > 0)
);

-- The public feed filter, exactly.
CREATE INDEX app_events_feed
    ON app_events (tenant_id, status, visibility, starts_at);
-- "What's on" listings and the calendar sync's desired-set scan.
CREATE INDEX app_events_upcoming
    ON app_events (tenant_id, starts_at);
-- Recurring rows must be inspected by every date-range query regardless of
-- starts_at, because a weekly series' first occurrence may be years in the
-- past. There are very few, so a partial index keeps that lookup cheap.
CREATE INDEX app_events_recurring
    ON app_events (tenant_id) WHERE rrule IS NOT NULL;

-- Per-tenant configuration. One row per tenant, same shape as
-- app_expense_policy.
--
-- calendar_id is chosen from a dropdown on the app's own admin page rather
-- than pasted into the integration form: the integrations substrate renders
-- only text-ish inputs, and the events calendar is a different calendar from
-- the one the shift sync writes to. An empty value means the sync is a no-op.
CREATE TABLE app_event_settings (
    tenant_id           UUID PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    calendar_id         TEXT NOT NULL DEFAULT '',
    timezone            TEXT NOT NULL DEFAULT 'America/Denver',
    -- e.g. 'https://www.thegravitybrewing.com/events/{slug}'. The canonical
    -- URL is DERIVED from this at read time, never stored per row, so changing
    -- the domain doesn't require rewriting every event.
    public_url_template TEXT NOT NULL DEFAULT '',
    -- Bearer token for the build-time feed. Consumption is server-side (a
    -- static-site build), so a shared secret is free and keeps the feed off
    -- casual scrapers.
    feed_token          TEXT NOT NULL DEFAULT '',
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down

DROP TABLE IF EXISTS app_event_settings;
DROP TABLE IF EXISTS app_events;
