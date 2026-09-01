-- +goose Up

-- Where events get promoted, and what has been done about it.
--
-- An event is entered once and then hand-carried to a growing list of places:
-- the chamber calendar, the city calendar, a brewery-events app, Facebook,
-- Instagram. That work scales with the number of DESTINATIONS, not the number
-- of events, which is why it becomes unmanageable at a handful of each.
--
-- Two tables, and the split matters:
--
--   app_event_channels  -- the destinations, one row per place. DATA, not
--                          code. Adding "City of Louisville" is a form
--                          submission, never a deploy. Only an `automated`
--                          channel needs a Go connector behind it.
--
--   app_event_promos    -- SPARSE state. Deliberately NOT a materialised
--                          checklist: the to-do list is computed on read from
--                          (event x channel x step) and left-joined against
--                          this table, so a row exists only once someone has
--                          acted on something or an automation ran.
--
-- Computing rather than materialising is what makes the whole thing cheap to
-- change. Retime a drip, add a step, flip a channel to `subscribed`, and every
-- event reflects it on the next page load -- no backfill, no reconciler, no
-- rows to clean up, and nothing to go stale. `expired` needs no row at all; it
-- is a date comparison.

CREATE TABLE app_event_channels (
    id        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,

    name TEXT NOT NULL,

    -- How work reaches this destination. The three modes are the whole point
    -- of the table:
    --
    --   manual     -- a human fills in their web form. Generates checklist
    --                 items. The default, and where every channel starts.
    --   subscribed -- THEY pull our ICS feed. Generates NOTHING, forever.
    --                 This is the win condition: converting one channel from
    --                 manual to subscribed retires a recurring chore rather
    --                 than doing it faster.
    --   automated  -- Kit posts through an API. Needs `connector` set.
    --
    -- Changing mode is a data change precisely because the list is computed.
    mode TEXT NOT NULL DEFAULT 'manual'
        CHECK (mode IN ('manual', 'subscribed', 'automated')),

    -- Which Go connector backs `automated`, empty for generic destinations.
    -- Channel is NOT credential: the four Meta surfaces (FB post, FB story,
    -- IG post, IG story) are four channels sharing one integration, because
    -- they have four different rhythms.
    connector TEXT NOT NULL DEFAULT '',

    -- The deep link a checklist row opens: their actual submit-an-event form.
    submit_url TEXT NOT NULL DEFAULT '',

    -- Which ICS tier a `subscribed` channel consumes, for display.
    feed_tier TEXT NOT NULL DEFAULT ''
        CHECK (feed_tier IN ('', 'all', 'highlights', 'featured')),

    -- When someone last confirmed a `subscribed` channel is really pulling.
    --
    -- This exists because `subscribed` is the one mode that can fail
    -- SILENTLY. It generates no work by definition, so if the chamber never
    -- finished wiring up the feed, events quietly stop reaching them and
    -- nothing ever surfaces it. Every other mode fails loudly -- a manual row
    -- sits there, an automated post errors.
    verified_at TIMESTAMPTZ,

    -- How far ahead this destination needs telling. The chamber wanting two
    -- weeks' notice is what makes priority meaningful: urgency is distance to
    -- (event date - lead_time_days), NOT distance to the event. Without it,
    -- ordering a checklist is arbitrary.
    lead_time_days INT NOT NULL DEFAULT 0 CHECK (lead_time_days >= 0),

    -- The campaign template: an array of
    --   {key, label, kind, offset_days, interval_days, expires_after_days,
    --    automatable}
    -- where kind is oneshot | drip | cadence.
    --
    -- JSONB rather than a child table because this is small, hand-edited
    -- configuration rather than relational data -- the builder app's
    -- app_items sets the precedent. It is also written by presets in the
    -- admin UI, never hand-authored.
    --
    -- `automatable` is per STEP, not per channel: Facebook can auto-post but
    -- can never auto-create the annual recurring event, because no API exists
    -- for it. So an `automated` channel still emits some manual rows.
    steps JSONB NOT NULL DEFAULT '[]'::jsonb,

    -- Whether this destination hears about an event at all, below which
    -- nothing runs. Steps carry their own floor on top, so a channel can take
    -- the announce for a normal event and the full drip only for a featured
    -- one. Prominence already draws these lines; no new axis is introduced.
    min_prominence TEXT NOT NULL DEFAULT 'normal'
        CHECK (min_prominence IN ('background', 'normal', 'featured')),

    -- Whether events we are ATTENDING rather than hosting belong here.
    --
    -- Defaults false, and the default is the interesting part. An offsite
    -- event is someone else's -- the chamber and the city already carry it
    -- from the actual organiser, so submitting it duplicates their listing
    -- and takes the work of doing so. But "come see us at GABF" is exactly
    -- what you want on your own Facebook, which is why this cannot be
    -- inferred from the channel's mode and has to be a per-channel choice.
    --
    -- Mirrors the ICS tiers, where offsite appears only in the everything
    -- feed. Independent of prominence: a FEATURED offsite event is still
    -- someone else's to list.
    include_offsite BOOLEAN NOT NULL DEFAULT FALSE,

    active     BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Two destinations in one workspace should not share a name; the checklist
-- rows are read by a human who has to tell them apart.
CREATE UNIQUE INDEX idx_app_event_channels_name
    ON app_event_channels (tenant_id, lower(name));

-- The list build asks "which channels are live for this tenant" every load.
CREATE INDEX idx_app_event_channels_active
    ON app_event_channels (tenant_id) WHERE active;

-- One row per thing actually DONE (or deliberately not done).
--
-- Note what is absent from the status list: there is no 'todo'. A to-do is the
-- absence of a row, which is what keeps the computed list authoritative -- a
-- stored 'todo' would be a second source of truth that could disagree with the
-- template about whether the work still applies.
--
-- 'expired' is absent for the same reason: a drip step whose window has closed
-- is computed, never written. It lapses quietly and drops off rather than
-- accumulating as a guilt ledger, which is the failure mode this whole design
-- exists to avoid.
CREATE TABLE app_event_promos (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    event_id   UUID NOT NULL REFERENCES app_events(id) ON DELETE CASCADE,
    channel_id UUID NOT NULL REFERENCES app_event_channels(id) ON DELETE CASCADE,

    -- Which step of the channel's campaign. Part of the key because one
    -- channel has several: "announce on Facebook" and "remind one week out"
    -- are both Facebook, which is why (event, channel) alone cannot identify
    -- a row.
    step_key TEXT NOT NULL,

    status TEXT NOT NULL
        CHECK (status IN ('done', 'ignored', 'auto_done', 'auto_failed')),

    -- Where it landed, for auto_done especially: a cadence row shows "last
    -- posted 3 Aug" with a link, which answers "is this due" and "what did I
    -- say last time" in one glance.
    url  TEXT NOT NULL DEFAULT '',
    note TEXT NOT NULL DEFAULT '',

    updated_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (tenant_id, event_id, channel_id, step_key)
);

-- The page joins state onto the computed list for one tenant's upcoming
-- events; the cadence anchor asks for the latest 'done' per (event, channel,
-- step), which this ordering serves directly.
CREATE INDEX idx_app_event_promos_lookup
    ON app_event_promos (tenant_id, event_id, channel_id, step_key, updated_at DESC);

-- +goose Down
DROP INDEX IF EXISTS idx_app_event_promos_lookup;
DROP TABLE IF EXISTS app_event_promos;
DROP INDEX IF EXISTS idx_app_event_channels_active;
DROP INDEX IF EXISTS idx_app_event_channels_name;
DROP TABLE IF EXISTS app_event_channels;
