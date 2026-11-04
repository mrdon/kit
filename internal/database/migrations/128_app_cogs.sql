-- +goose Up

-- The brewing app: what beer is in which vessel, what was packaged from it,
-- and what happened to it since. See docs/cogs-spec.md.
--
-- The core of it is an append-only ledger (app_cogs_events). Every
-- physical fact the brewer records is an event; nothing in the ledger is
-- updated or deleted by app code. Undo is a 'reversal' event naming the
-- action it cancels, and a reversed action has no effect anywhere: vessel
-- state is replayed from the actions that stand, and lot balances sum only
-- their moves. That one rule is what lets an entry be logged a day late, or
-- reversed from the console a month later, without anything going stale.
--
-- Setup tables (vessels, beers, formats, settings) are ordinary config and
-- are edited in place. Nothing here is Gravity-specific: the brewery's tanks
-- and formats are rows, not constants.

CREATE TABLE app_cogs_settings (
    tenant_id           UUID PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    -- A "partial" keg in a count is taken to be this full.
    partial_fullness    NUMERIC(4,3) NOT NULL DEFAULT 0.5,
    barrel_bbl          NUMERIC(8,3) NOT NULL DEFAULT 1.7,
    brewhouse_bbl       NUMERIC(8,3) NOT NULL DEFAULT 10,
    count_interval_days INT NOT NULL DEFAULT 7,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE app_cogs_vessels (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    kind       TEXT NOT NULL CHECK (kind IN ('fermenter', 'brite', 'serving', 'barrel')),
    area       TEXT NOT NULL CHECK (area IN ('brewhouse', 'chiller', 'barrels')),
    size_bbl   NUMERIC(8,3) NOT NULL CHECK (size_bbl > 0),
    sort       INT NOT NULL DEFAULT 0,
    retired_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- Cached state, replayed from the ledger after every action that touches
    -- the vessel. Never written any other way.
    batch_id   UUID,
    volume_bbl NUMERIC(10,3) NOT NULL DEFAULT 0,
    stage      TEXT NOT NULL DEFAULT '',
    stage_at   TIMESTAMPTZ,
    filled_at  TIMESTAMPTZ,
    dirty      BOOLEAN NOT NULL DEFAULT FALSE,
    on_tap     BOOLEAN NOT NULL DEFAULT FALSE
);
CREATE UNIQUE INDEX idx_app_cogs_vessels_name ON app_cogs_vessels (tenant_id, lower(name));

CREATE TABLE app_cogs_beers (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id          UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name               TEXT NOT NULL,
    -- Short prefix for batch codes ("LL" makes LL-07).
    code               TEXT NOT NULL DEFAULT '',
    aliases            TEXT[] NOT NULL DEFAULT '{}',
    -- Planning family: variants roll their demand up into this beer's name.
    family             TEXT NOT NULL DEFAULT '',
    style              TEXT NOT NULL DEFAULT '',
    active             BOOLEAN NOT NULL DEFAULT TRUE,
    staple             BOOLEAN NOT NULL DEFAULT FALSE,
    -- Set when the brewer typed a new name on the iPad; Don finishes it.
    needs_setup        BOOLEAN NOT NULL DEFAULT FALSE,
    ferment_days       INT NOT NULL DEFAULT 14,
    dry_hop            BOOLEAN NOT NULL DEFAULT FALSE,
    dry_hop_days       INT NOT NULL DEFAULT 4,
    condition_days     INT NOT NULL DEFAULT 0,
    brite_days         INT NOT NULL DEFAULT 3,
    uses_brite         BOOLEAN NOT NULL DEFAULT TRUE,
    keg_shelf_weeks    NUMERIC(6,2) NOT NULL DEFAULT 12,
    pkg_shelf_weeks    NUMERIC(6,2) NOT NULL DEFAULT 16,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX idx_app_cogs_beers_name ON app_cogs_beers (tenant_id, lower(name));

CREATE TABLE app_cogs_package_formats (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name           TEXT NOT NULL,
    kind           TEXT NOT NULL CHECK (kind IN ('keg', 'can', 'bottle')),
    bbl_per_unit   NUMERIC(10,6) NOT NULL CHECK (bbl_per_unit > 0),
    -- Cans and bottles are counted in cases plus loose units; 0 for kegs.
    units_per_case INT NOT NULL DEFAULT 0 CHECK (units_per_case >= 0),
    returnable     BOOLEAN NOT NULL DEFAULT FALSE,
    sort           INT NOT NULL DEFAULT 0,
    retired_at     TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX idx_app_cogs_formats_name ON app_cogs_package_formats (tenant_id, lower(name));

CREATE TABLE app_cogs_batches (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    beer_id         UUID NOT NULL REFERENCES app_cogs_beers(id),
    -- Per beer: the 7th batch of Light Lift is LL-07.
    number          INT NOT NULL,
    brewed_on       DATE NOT NULL,
    source          TEXT NOT NULL DEFAULT 'brewed' CHECK (source IN ('brewed', 'acquired')),
    -- A variant (Cashew Newtonian) is a derived batch of its parent.
    parent_batch_id UUID REFERENCES app_cogs_batches(id),
    note            TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX idx_app_cogs_batches_number ON app_cogs_batches (tenant_id, beer_id, number);

-- Lineage: a derived batch's sources. A variant has one parent; a blend of
-- two batches of the same beer, or a repack that drew from several, has
-- each, with how much of it went in, so any keg traces back to every brew
-- in it.
CREATE TABLE app_cogs_batch_parents (
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    batch_id        UUID NOT NULL REFERENCES app_cogs_batches(id) ON DELETE CASCADE,
    parent_batch_id UUID NOT NULL REFERENCES app_cogs_batches(id) ON DELETE CASCADE,
    bbl             NUMERIC(10,3) NOT NULL DEFAULT 0,
    PRIMARY KEY (tenant_id, batch_id, parent_batch_id)
);

-- The ledger. An action (one press of Save) writes one or more events with
-- the same action_id; undo reverses the whole action.
CREATE TABLE app_cogs_events (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id          UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    seq                BIGSERIAL NOT NULL,
    action_id          UUID NOT NULL,
    kind               TEXT NOT NULL,
    occurred_at        TIMESTAMPTZ NOT NULL,
    batch_id           UUID REFERENCES app_cogs_batches(id),
    vessel_id          UUID REFERENCES app_cogs_vessels(id),
    to_vessel_id       UUID REFERENCES app_cogs_vessels(id),
    -- What left the vessel (or went in, for a brew or a load).
    volume_bbl         NUMERIC(10,3) NOT NULL DEFAULT 0,
    -- What arrived in to_vessel_id, for a transfer.
    volume_in_bbl      NUMERIC(10,3) NOT NULL DEFAULT 0,
    -- Written off. Negative is a gain (a level read above what was expected).
    loss_bbl           NUMERIC(10,3) NOT NULL DEFAULT 0,
    details            JSONB NOT NULL DEFAULT '{}'::jsonb,
    recorded_by_kind   TEXT NOT NULL,
    recorded_by_id     UUID,
    recorded_label     TEXT NOT NULL DEFAULT '',
    reverses_action_id UUID,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_app_cogs_events_action ON app_cogs_events (tenant_id, action_id);
CREATE INDEX idx_app_cogs_events_vessel ON app_cogs_events (tenant_id, vessel_id, occurred_at);
CREATE INDEX idx_app_cogs_events_to_vessel ON app_cogs_events (tenant_id, to_vessel_id, occurred_at);
CREATE UNIQUE INDEX idx_app_cogs_events_reverses ON app_cogs_events (tenant_id, reverses_action_id)
    WHERE reverses_action_id IS NOT NULL;

-- Packaged inventory: one lot per batch x format per packaging (or repack,
-- or opening load). A lot's balance is the sum of its moves from actions
-- that stand; draws come from the oldest lot of a beer and format first.
CREATE TABLE app_cogs_lots (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    beer_id     UUID NOT NULL REFERENCES app_cogs_beers(id),
    batch_id    UUID REFERENCES app_cogs_batches(id),
    format_id   UUID NOT NULL REFERENCES app_cogs_package_formats(id),
    packaged_on DATE NOT NULL,
    event_id    UUID NOT NULL REFERENCES app_cogs_events(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_app_cogs_lots_beer ON app_cogs_lots (tenant_id, beer_id, format_id, packaged_on);

CREATE TABLE app_cogs_lot_moves (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    lot_id     UUID NOT NULL REFERENCES app_cogs_lots(id),
    event_id   UUID NOT NULL REFERENCES app_cogs_events(id),
    action_id  UUID NOT NULL,
    units      NUMERIC(10,2) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_app_cogs_lot_moves_lot ON app_cogs_lot_moves (tenant_id, lot_id);
CREATE INDEX idx_app_cogs_lot_moves_action ON app_cogs_lot_moves (tenant_id, action_id);

-- What a blind count found against what was expected, per beer and format.
CREATE TABLE app_cogs_count_lines (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    event_id       UUID NOT NULL REFERENCES app_cogs_events(id),
    beer_id        UUID NOT NULL REFERENCES app_cogs_beers(id),
    format_id      UUID NOT NULL REFERENCES app_cogs_package_formats(id),
    counted_units  NUMERIC(10,2) NOT NULL,
    expected_units NUMERIC(10,2) NOT NULL,
    entry          JSONB NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX idx_app_cogs_count_lines_event ON app_cogs_count_lines (tenant_id, event_id);

-- The brew-sheet photo: the paper record, attached to the batch. Bytes in
-- Postgres for the same reason as app_menu_assets: a few small images a
-- week, and one fewer service to run.
CREATE TABLE app_cogs_photos (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    batch_id   UUID REFERENCES app_cogs_batches(id),
    mime       TEXT NOT NULL,
    bytes      BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_app_cogs_photos_batch ON app_cogs_photos (tenant_id, batch_id);

-- Planned brews. Owned by the console; the brewer can only flag one.
CREATE TABLE app_cogs_plan_entries (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    beer_id      UUID NOT NULL REFERENCES app_cogs_beers(id),
    vessel_id    UUID NOT NULL REFERENCES app_cogs_vessels(id),
    brew_on      DATE NOT NULL,
    volume_bbl   NUMERIC(8,3) NOT NULL,
    why          TEXT NOT NULL DEFAULT '',
    batch_id     UUID REFERENCES app_cogs_batches(id),
    cancelled_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_app_cogs_plan_entries_date ON app_cogs_plan_entries (tenant_id, brew_on);

-- Loose ends for the console: a beer typed on the iPad to set up, a plan
-- date the brewer flagged, a count that didn't match. Never a blocker.
CREATE TABLE app_cogs_queue (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    kind          TEXT NOT NULL,
    title         TEXT NOT NULL,
    body          TEXT NOT NULL DEFAULT '',
    beer_id       UUID REFERENCES app_cogs_beers(id),
    plan_entry_id UUID REFERENCES app_cogs_plan_entries(id),
    action_id     UUID,
    created_label TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at   TIMESTAMPTZ
);
CREATE INDEX idx_app_cogs_queue_open ON app_cogs_queue (tenant_id, created_at) WHERE resolved_at IS NULL;

-- +goose Down
DROP TABLE IF EXISTS app_cogs_queue;
DROP TABLE IF EXISTS app_cogs_plan_entries;
DROP TABLE IF EXISTS app_cogs_photos;
DROP TABLE IF EXISTS app_cogs_count_lines;
DROP TABLE IF EXISTS app_cogs_lot_moves;
DROP TABLE IF EXISTS app_cogs_lots;
DROP TABLE IF EXISTS app_cogs_events;
DROP TABLE IF EXISTS app_cogs_batch_parents;
DROP TABLE IF EXISTS app_cogs_batches;
DROP TABLE IF EXISTS app_cogs_package_formats;
DROP TABLE IF EXISTS app_cogs_beers;
DROP TABLE IF EXISTS app_cogs_vessels;
DROP TABLE IF EXISTS app_cogs_settings;
