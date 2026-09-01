-- +goose Up

-- Pre-aggregated Square sales, pulled from the Reporting API's Sales and
-- ProductMixReport views. These rows are a CACHE, not the record -- Square
-- owns the truth and can amend it (a void, a comp, a refund three days
-- later), so every write is an idempotent replace keyed on the business
-- day, and the sync deliberately re-pulls days it already has.
--
-- WHY CACHE AT ALL. The daily sales card answers "was that unexpectedly
-- high?", which needs a same-weekday baseline over the trailing eight
-- weeks. That is the same eight numbers every morning; asking Square for
-- eight weeks of sales on every run is slow and rude.
--
-- WHY CENTS. The Reporting API returns money as a JSON number in major
-- units, with float artifacts (2725.0899999999997). Rounding to an integer
-- minor unit at the edge means no float ever reaches a stored amount, and
-- it matches price_cents in app_events.
--
-- business_date is a CALENDAR LOCAL DATE in the LOCATION's timezone, taken
-- straight from Sales.local_date (which Square derives from
-- local_reporting_timestamp -- the revenue-recognition time, so a tab
-- closed at 00:40 lands on the night before). It is deliberately the same
-- basis the merchant's own Dashboard Sales Summary uses: a card whose
-- Saturday total disagrees with the number the owner already looked at is
-- worse than no card. timezone is stored per row so a location that
-- changes zones is detectable rather than silently re-labelled.

CREATE TABLE app_squaresales_daily (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    location_id       TEXT NOT NULL,
    location_name     TEXT NOT NULL DEFAULT '',
    business_date     DATE NOT NULL,
    timezone          TEXT NOT NULL,
    currency          TEXT NOT NULL DEFAULT 'USD',

    -- net_sales is the revenue metric: gross less discounts, comps and
    -- returns, ALREADY excluding tax and tips (Square computes it that
    -- way). Tips run ~20% of collected here, so reporting collected as
    -- "sales" would overstate every number by a quarter.
    net_sales_cents   BIGINT NOT NULL DEFAULT 0,
    gross_sales_cents BIGINT NOT NULL DEFAULT 0,
    collected_cents   BIGINT NOT NULL DEFAULT 0,
    -- Stored as magnitudes. Square returns discounts_amount and
    -- comps_amount signed negative; the sign is normalised on ingest so
    -- nothing downstream subtracts them twice. itemized_returns is already
    -- positive. Verified against live data on seven days:
    --   net = gross - discounts - comps - returns   (exact)
    --   collected = net + tax + tips + gift_cards + service_charges
    -- Both identities are re-checked per row on ingest; see reconcile().
    -- Comps are kept because they are material here (9% of gross on one
    -- August Saturday) and comped beer is worth seeing on its own.
    discounts_cents   BIGINT NOT NULL DEFAULT 0,
    comps_cents       BIGINT NOT NULL DEFAULT 0,
    returns_cents     BIGINT NOT NULL DEFAULT 0,
    tips_cents        BIGINT NOT NULL DEFAULT 0,
    tax_cents         BIGINT NOT NULL DEFAULT 0,
    -- Gift card sales are a LIABILITY, not revenue -- money collected on
    -- someone else's behalf until the card is redeemed. Square rightly
    -- keeps them out of net_sales; they are stored only so the collected
    -- identity can be re-checked from the row, and because "$200 of gift
    -- cards moved" is worth seeing in December.
    gift_card_cents     BIGINT NOT NULL DEFAULT 0,
    service_charge_cents BIGINT NOT NULL DEFAULT 0,
    order_count       INTEGER NOT NULL DEFAULT 0,

    -- Idempotency key for the daily card: the posting task only considers
    -- business dates whose row has this NULL, and stamps it once a card is
    -- created (or once the date is judged unpostable). No second table, and
    -- a task that runs twice cannot post twice.
    card_posted_at    TIMESTAMPTZ,

    -- 'reporting' today. Recorded so a later backfill from a different
    -- Square surface is distinguishable rather than blended into one
    -- baseline.
    source            TEXT NOT NULL DEFAULT 'reporting',
    observed_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (tenant_id, location_id, business_date)
);

-- Two access patterns: a contiguous recent range, and an explicit list of
-- same-weekday dates (D-7, D-14 ... D-56) generated in Go. Both are served
-- by this one index -- there is no day_of_week column because the baseline
-- never asks "which of these are Saturdays", it asks for eight dates it
-- already computed.
CREATE INDEX app_squaresales_daily_date
    ON app_squaresales_daily (tenant_id, business_date DESC);

-- Hourly buckets, for "the 3-4pm stretch was dead".
--
-- A row exists for every hour of every covered day INCLUDING zero-sales
-- hours: the Reporting API simply omits an hour that sold nothing, so the
-- sync materialises all 24. That makes "no row" mean "not synced" and never
-- "nobody came in" -- a dead hour is a fact that must be stored, not
-- inferred from an absence -- and it makes the row set fixed, so a plain
-- upsert can never leave a stale hour behind.
CREATE TABLE app_squaresales_hourly (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    location_id     TEXT NOT NULL,
    business_date   DATE NOT NULL,
    hour_of_day     SMALLINT NOT NULL CHECK (hour_of_day BETWEEN 0 AND 23),
    net_sales_cents BIGINT NOT NULL DEFAULT 0,
    order_count     INTEGER NOT NULL DEFAULT 0,
    observed_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (tenant_id, location_id, business_date, hour_of_day)
);

CREATE INDEX app_squaresales_hourly_date
    ON app_squaresales_hourly (tenant_id, business_date DESC);

-- What sold, per item, per business day -- the "movers" table. For a
-- taproom "sales were flat but Golden Mosaic doubled" is the whole story,
-- and the top-line number hides it.
--
-- category_name is Square's seller-facing name ('Uncategorized' when an
-- item has none) and item_name is the item, both kept as text rather than
-- ids: they are what the card says out loud, and a renamed beer should read
-- as renamed. Day grain only -- hour x item is a cross product nobody asks
-- about.
CREATE TABLE app_squaresales_items (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    location_id       TEXT NOT NULL,
    business_date     DATE NOT NULL,
    category_name     TEXT NOT NULL DEFAULT '',
    item_name         TEXT NOT NULL DEFAULT '',
    net_sales_cents   BIGINT NOT NULL DEFAULT 0,
    gross_sales_cents BIGINT NOT NULL DEFAULT 0,
    -- NUMERIC because Square reports quantity in the item's unit of
    -- measure: whole pours here, but a food partner selling by weight would
    -- silently truncate against an integer.
    units_sold        NUMERIC(14,3) NOT NULL DEFAULT 0,
    observed_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (tenant_id, location_id, business_date, category_name, item_name)
);

CREATE INDEX app_squaresales_items_date
    ON app_squaresales_items (tenant_id, business_date DESC);

-- Serves "how has this beer been moving" without scanning every day.
CREATE INDEX app_squaresales_items_item
    ON app_squaresales_items (tenant_id, item_name, business_date DESC);

-- +goose Down

DROP TABLE IF EXISTS app_squaresales_items;
DROP TABLE IF EXISTS app_squaresales_hourly;
DROP TABLE IF EXISTS app_squaresales_daily;
