# Kit COGS app: spec (production state, COGS, TTB)

**Status:** Draft, 2026-11-02. Nothing is built yet. The design was worked out in a Claude Code session and checked against an independent review of the mockup (findings and what was done with them are in the appendix).
**Mockup:** an interactive iPad mockup at claude.ai/artifact/5q17jcdCkF8AukVDk2gVSn (shared with anyone who has the link). It uses sample data built on Gravity's real tanks and beers. Where the mockup and this spec differ, the spec wins.
**Audience:** the implementing agent, and Don.

## Problem

Gravity has almost no view into what its beer costs or where it is. Today:

- **Cost** is guessed from Ramp expenses. Nothing ties ingredients to batches or batches to kegs.
- **Schedule and inventory** live in the brewer's head, a paper calendar and paper logs, so finding anything out means asking the brewer.
- **The brew plan** is a Google Sheet that `~/dev/automation/scripts/sales/build_brew_plan_sheet.py` generates from Square and wholesale history. It's a good demand model, but it doesn't know what is in the tanks, it ignores beer that is still fermenting, it has no idea of tank capacity, and its keg inventory is counted by hand every few weeks at best.
- **TTB reporting** has no system behind it.

The brewer is happy with BeerSmith, paper and the calendar. Whatever we build has to give Don visibility **without making the brewer's job worse**. The brewer is non-technical but willing to enter data if it's clear, easy and quick.

## Goals

1. **COGS per keg, can and bottle** for everything sold or on tap: batch cost ÷ units packaged from that batch, plus packaging materials for cans and bottles.
2. **TTB automation**: the Brewer's Report of Operations and excise, built from data the system already has.
3. **A live state-of-the-system view**: what beer is in which vessel, how old it is, what happens next. This **replaces the brew-plan spreadsheet.**
4. **A feed** of schedule and cost signals: brew-now, run-out dates, stale beer, holds coming due, ingredient price changes.

**Nice to have, later:**
- Menu sync to Untappd, Square and the online store.
- Wholesale invoicing paperwork.
- Sales commission on gross profit instead of today's 10% of top-line sales. This one becomes straightforward once COGS per keg exists.

## Non-goals

- **Replacing BeerSmith.** Kit *reads* recipes and never authors or edits them.
- **A brewery ERP.** In particular: gravity and temperature logs stay on paper clipboards, there's no QC module, and there's no per-keg serial or barcode tracking.
- **Wholesale and invoicing**, for now. They come after production data is flowing.
- **Recipes and ingredients in the first cut.** They're designed below, but deferred.

## Principles

1. **The system guesses, the brewer confirms.** Forms are pre-filled from the plan and from what's in the tank. The brewer changes only what's different. **There are two deliberate exceptions, where a pre-fill would turn the entry into a rubber stamp:** the weekly count is blind, and unit counts at packaging and repacking (kegs, cans, bottles) start at zero. Those two numbers are the ground truth that COGS and TTB losses rest on.
2. **Nothing is written down twice.** Every fact lives on paper *or* in the app, never both. See "Paper vs app".
3. **Packaged beer enters inventory in exactly one way: packaging from a vessel.** Every keg, can and bottle that leaves a tank is recorded with its vessel, batch, format, count and date.
4. **The beer ledger is append-only.** Volumes are added and subtracted by events and never overwritten. Even reading a level records the difference as an event. Undo means a reversing event, not deleting a row. (These are lessons COG learned the hard way. See "What to take from COG".)
5. **Never block the brewer.** Unknown beer, a missing recipe, a dirty tank, logging a day late: in every case the entry gets saved and the loose end goes to Don's queue.
6. **No tenant-specific hard-coding.** Vessels, beers, stage durations, package formats, packaging materials and rates are all per-tenant configuration. Gravity is the seed data, not the schema.
7. **Keep Kit's own model calls to a minimum** (same rule as the posters app). The brewer's screens make no model calls. Any extraction work, such as reading Ramp invoice lines or recipes, is bounded, triggered by a person or a sync, and always reviewed by Don.

## Users

| Who | Where | What they do |
|---|---|---|
| **The brewer** | A wall-mounted iPad in the brewhouse (works on a phone too) | Records physical facts: brews, cellar moves, packaging, repacks, losses, cleaning, the weekly count. Never sees money. |
| **Don** | The kit console (desktop) | Owns the plan, beer settings, setup, corrections, Ramp matching and the review queues. Reads COGS, TTB and the feed. |

**Signing in on the iPad:** the iPad is paired once, by opening a console-generated link or QR code. After that it stays signed in permanently as "Brewhouse iPad", and Don can revoke it from the console. There's no PIN: nothing on the iPad is dangerous, every entry can be undone or corrected, and a PIN wouldn't stop the realistic risk, which is an unlocked iPad. Use iPad Guided Access to lock it to the app. Optional per-person 4-digit codes can be added later if more than one person logs and Don wants to know who did what. Reuse kit's `devices` and `kiosk` pairing if it fits.

## Equipment (Gravity seed data)

Confirmed by Don on 2026-11-02. The authoritative document is the Asset Purchase Agreement, Exhibit A(i) (Drive id `1MD3WhqKYg278hZ8eriXTKjgutMfMbfM7`).

| Vessel | Count | Size | Notes |
|---|---|---|---|
| FV-1 … FV-6 | 6 | 10 bbl | Fermenters |
| FV-7 | 1 | 20 bbl | A batch this size takes **two brews**, because the brewhouse is 10 bbl |
| BT-1 | 1 | 10 bbl, jacketed | Brite tank. Listed as a "serving vessel" in the purchase agreement. **What it's actually used for is unconfirmed.** Working guess: beer headed for kegs goes FV → BT-1 → kegs, and serving-tank beer goes FV → SV. Both routes must work. |
| SV-1 … SV-5 | 5 | 10 bbl, single wall | Serving tanks in the chiller. **The brewer's notice calls them "tax determined serving tanks"** (see TTB). |
| Barrels | ? | ~53 gal (1.7 bbl) | Used for the BA beers. **Names and count are unconfirmed.** The mockup uses B-1 … B-6. |
| Mash tun, kettle | 1 each | 10 bbl | Brew day only. Not tracked as vessels. |
| Package formats | — | Kegs: ½ bbl (0.5), ⅙ bbl (0.1667). **Cans and 750ml bottles too** (confirmed 2026-11-02) | Per-tenant config: name, bbl per unit, units per case, whether it's returnable. The distribution workbook sells 16oz and 12oz can cases and 750ml cases. Assumed 24 cans and 12 bottles per case (`build_wholesale.py`'s `CASE_UNITS`); **confirm.** A "partial" keg counts as half full. **Crowlers are not production packaging:** they're filled at the taproom and deplete like pours. |

**The TTB brewer's notices are out of date.** Every one of them, including the one filed 03/19/2026 for Gravity Local LLC, repeats the 2012 description: 2 fermenters and 4 serving tanks. Whoever handles filings should check whether it needs amending.

## What exists today, and what we keep

### The brew-plan sheet: `~/dev/automation`

The sheet is "Gravity brew plan", id `1YjRmin8Z6waC7wR-PdbHFFl9rSfMP4EfSZBuFwH1eSw`. Read it with `gws`. **Its model gets ported, not reinvented.** The module docstring and the sheet's "Read me" tab are the spec. In summary:

- **Sell rate, per beer and channel.**
  - Taproom: bbl per *day on tap* × open days per week, so partial months and kegs that kicked early don't skew it.
  - Wholesale: bbl per month on sale.
  - Seasonal: this month plus the next two, averaged over 3 years, then adjusted by a recent-vs-last-year trend factor (weight 0.5, clamped to 0.5–2).
  - A beer with too little history falls back to its 3-year rate and is flagged as low confidence.
- **Runs out** = date counted + bbl on hand ÷ rate.
- **Fresh until** = brewed on + brew time + keg shelf life.
- **Next ideal brew date** = runs out − brew time − buffer.
- **Statuses:**
  - **STOP WHOLESALE + BREW NOW:** the beer won't last until a new batch at the full rate, but the taproom alone would.
  - **WON'T LAST:** it won't last even without wholesale.
  - **BREW NOW, HOLD WHOLESALE**
  - **BREW NOW / Brew soon** (within 7 or 28 days)
  - **LASTS TOO LONG:** some of it will be poured past shelf life.
  - **ENTER INVENTORY**
- **Brew this much** = the ideal batch, capped at what sells within one keg shelf life.
- **Per-beer settings (the Beers tab):**
  - active, staple, style
  - brew time (weeks), ideal batch (bbl), packaging yield, keg shelf life (weeks), buffer (weeks)
  - include wholesale, demand adjust, rate override
  - plan order
- **Global settings:** keg sizes, partial-keg fullness, weeks per month, the brew-now and brew-soon windows, batch rounding, rate windows and their thresholds.
- **Name aliases.** `scripts/sales/beers.py` (`canonical()`) and `PLAN_NAME` in the generator unify how the beers are spelled across the two POS eras, the wholesale workbook and Untappd. Every Quasar flavour counts as "Quasar" for planning. **Port these aliases.** They're hard-won and easy to lose.
- **Data quirks to carry over:**
  - **2026-06 is the handover month:** blank, not zero.
  - **Seller-era pours** were all logged at 16 oz and get rescaled.
  - **`keg_wholesale` POS lines are excluded**, because those kegs were also billed on the distribution sheet.
  - **Wholesale figures are a floor**, because invoice lines that name no beer are left out.

A data point worth acting on: as of the 2026-10-31 build, the sheet flags about **42 of the 88 bbl on hand** as past, or going past, shelf life before it sells. Cerveza Espacial accounts for 10.5 bbl (27 bbl across roughly three serving tanks), Mars Water 6.2 and Black Array 4.5. Either the style-default shelf lives are too tight, or half the stock is going stale. The brewer should correct the shelf lives during setup.

### COG: `~/dev/cog`

COG is a Next.js and Supabase app vibe-coded by Don's partner. It was **never used** in production for brewing. Don't port its code or UI. **Do read its domain notes:** the "Domain tables" section of `~/dev/cog/CLAUDE.md` and `docs/decisions/` there. Rules worth keeping:

- **Ledgers are append-only.** A correction is a new row.
- **Cost is frozen when an ingredient is used.** A later price change never rewrites what a past batch cost.
- **When purchases and usage are recorded together, write the purchases first.** Usage is costed at the running average, so the order matters.
- **No unit conversion.** The unit lives on the ingredient's catalog row.
- **Vessel volumes add and subtract, never get set.** Over-drawing a tank fails loudly rather than clamping to zero.
- **A batch locks in a snapshot of its recipe** at brew time. Editing the recipe later never changes past batches.
- **Brewed vs acquired:** beer bought as finished goods must be excluded from TTB production figures.
- **Excise is applied per barrel removed, on the sales side.** It is not a COGS component.
- **Exclude cleaning chemicals from COGS.** They're overhead.
- **Yeast cost is shared across repitches.** One pack's cost is split over every batch it's repitched into, so a batch's yeast share falls as the yeast is reused.

COG's bugs are also worth learning from. They're in its CLAUDE.md under `tank_fills` and `tank_kegouts`:
- a fill trigger that **set** the volume instead of adding to it
- a keg-out that clamped to zero
- a free-text package type that split one format into two rows

### Already in kit

- **Square:** the `square` app (OAuth and client) and `squaresales` (daily, hourly and item rollups). **Gap:** `app_squaresales_items` is keyed by `item_name` with no variation or pour size. Taproom depletion and the demand model both need ounces sold, so the sync has to keep the variation. `automation` gets this today from Square exports (`beer_month.csv`, `gl_item_mix_day_*.csv`, `gl_tap_days_*.csv`).
- **Menu and Untappd:** the `menu` app already reads the Untappd board and tracks 86'd items. Days on tap and "on tap?" can come from there or from Square.
- **Kiosk and devices:** pairing an iPad.
- **Google:** the calendar app's service-account token only (`googlecalendar/token.go`, calendar scope). **There is no Drive access today.**
- **Email:** a per-user IMAP/SMTP agent tool. There's no inbound address.

## Domain model

### Concepts

- **Vessel:** a fermenter, brite, serving tank or barrel. Per-tenant config: name, kind, size in bbl, area (brewhouse, chiller or barrels). Cached current state: batch, volume, stage, status (clean, dirty or in use), filled at. **The cached state is derived from events and never edited by hand.**
- **Beer:** a catalog entry, plus planning settings (the Beers tab above) and production settings:
  - **stage durations:** ferment days, whether the beer is dry hopped (and how many days), conditioning or lagering days, brite days
  - **route:** uses BT-1 or not
  - **shelf life per format**
  - **planning family**, which rolls variants up for demand. For example, Peach Orbit might roll into Galactic Orbit or Quasar. Don decides per variant.
  - Optional link to a BeerSmith recipe, used later.
- **Batch:** one brew, or a double brew into FV-7, which still counts as one batch. It has a beer, a brew date, `source` (brewed or acquired), its volumes, and later a recipe snapshot and ingredient costs. A batch's beer can end up split across several vessels and package runs. **Batch is how every keg, can, bottle, loss and cost is traced.**
- **Vessel event:** the append-only ledger. Each event carries:
  - kind, date (backdatable up to 7 days on the iPad; more from the console)
  - batch, source vessel, destination vessel, volume
  - the device or user that recorded it, and an optional attachment (the brew-sheet photo)
  - `reverses_event_id` when it's an undo
- **Package run:** a packaging event. Batch, vessel, date, and a count per package format: ½ and ⅙ kegs, cans and bottles, entered as cases plus loose units. **The only way packaged beer enters inventory.** A canning or bottling run can span hours, but it's logged once, at the end, like brew day.
- **Count:** a blind physical count per beer and format, compared with what was expected. Kegs have full and partial (half full); cans and bottles are counted in cases plus loose units. Serving tanks and barrels are read from their volumes, not counted.
- **Out, not sold:** a beer, a format, a count (kegs, or cases plus units), and a reason: dumped, event or gift, came back full, or other.
- **Repack:** packaged beer turned into other packaged beer. Examples: a ½ broken into sixtels, a keg canned or bottled off, a sixtel turned into a new beer. Inputs are packaged units (beer, format, count), taken from the oldest lot first unless a batch is picked. Outputs are packaged units (beer, format, count). The input bbl minus the output bbl is written off as loss. **Repack is the only way packaged beer changes format or beer.**
- **Variant (derived batch):** when a repack's output beer differs from its input, the output is a **new batch of the variant beer** (Cashew Newtonian, Grapefruit Orbit, a Quasar flavour), linked to the parent batch it came from. The same applies in bulk, when part of a tank becomes a variant (fruiting half a batch, say): a "Became a variant" event on the vessel creates the derived batch in place. A derived batch's planning family decides whether its sales count toward the parent beer's demand.
- **Plan entry:** a planned brew (beer, fermenter, date, size), produced by the planner and owned by Don. The brewer can flag a date with "this date won't work", but **can't move a plan entry**.
- **Hold:** a batch deliberately waiting before its next step. The case that motivated this: an IPA sits in its fermenter, not yet dry hopped, until the old batch sells down. A hold carries a release rule ("release when the old batch has less than N weeks left") and a computed release date. It also keeps the fermenter occupied, and the planner must account for that.

### Event kinds

| Kind | Brewer button | Effect on volume | TTB meaning |
|---|---|---|---|
| `brew` | Brew day / Brewed into it | +vol to the fermenter (a second brew into the same vessel adds to the same batch) | Produced |
| `dry_hop` | Dry hopped | none (stage change; releases a hold) | none |
| `transfer` | Moved it / Filled it / Pulled it | −out from the source, +in to the destination; `out − in` is a loss. The destination must be empty or hold the same beer (blending). | Into an SV may count as a taxable removal (see TTB) |
| `barrel_fill` | Moved it → Barrels | −N×1.7 from the source, +1.7 into each barrel | none (bulk stays bulk) |
| `package` | Packaged it | −packaged bbl from the vessel, +units per format to inventory | Packaged (bulk → package) |
| `empty` | the "now empty" tick on transfer or packaging | writes off what's left in the vessel | Loss |
| `loss` | Spilled or dumped (gallons) | −vol | Loss |
| `dump` | Dumped the whole thing | −all | Destroyed or loss (CPA to decide) |
| `level` | Read the level | records the difference between reading and expected as a loss or gain | Loss or gain |
| `kick` | It kicked | serving tank → 0; the remainder against the sales-based estimate is a loss or gain. If another SV holds the same beer, it goes on tap automatically. | Loss or gain |
| `on_tap` | On tap now | none (state only) | none |
| `clean` | Cleaned it (implied automatically when a dirty vessel is used) | none (status) | none |
| `out_not_sold` | Out, not sold | −units from inventory (or + for "came back full") | Per reason (CPA) |
| `repack` | Repack or make a variant (Packaged tab) | −input units, +output units; the bbl difference is a loss. A different output beer creates a derived batch | Repackaging (CPA: confirm how beer repacked from packages is reported) |
| `variant` | Became a variant (on a vessel) | none; the vessel's batch becomes a derived batch of the variant beer, linked to the parent | none (it's still bulk) |
| `count` | Count (Packaged tab) | records variance against expected; doesn't move beer | Feeds losses |

**The system records which of these happened; mapping each to a report line is a reporting decision.** That way the CPA's answers change the report, never what the brewer enters.

### Tables (sketch)

Everything is prefixed `app_cogs_` and carries `tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE`, per kit's rules, including child tables.

- `app_cogs_vessels`: config plus cached state.
- `app_cogs_beers`: catalog plus planning and production settings, aliases and planning family.
- `app_cogs_settings`: per-tenant globals (partial fullness, planning windows, rate parameters, barrel size, excise rates).
- `app_cogs_package_formats`: name, kind (keg, can, bottle), bbl per unit, units per case, returnable, and the per-unit packaging bill of materials (later, linked to ingredients).
- `app_cogs_lots`: packaged inventory lots (batch × format), created by package runs and repacks and drawn down first in, first out. Repack lines reference their input and output lots.
- `app_cogs_batches`: includes `parent_batch_id` for variants (derived batches) and `source` (brewed or acquired).
- `app_cogs_events`: the ledger. It's append-only in practice. App code never updates or deletes it; reversals are new rows.
- `app_cogs_package_lines` (one row per format per package run), or `package` events carrying the counts. Choose whichever keeps FIFO queries simple.
- `app_cogs_counts` + `app_cogs_count_lines`
- `app_cogs_plan_entries`
- `app_cogs_wholesale_lines` plus a coverage marker ("through" date): the imported wholesale input.
- `app_cogs_holds`: or fields on the batch.
- `app_cogs_devices`: if kit's devices don't cover it.
- **Later:** `app_cogs_recipes`, `recipe_versions`, `recipe_lines`, `ingredients`, `ingredient_purchases`, `ingredient_usage`, `ramp_line_mappings`.

Vessel state is maintained by the service layer from events, inside one transaction per event. The checks:
- **Over-drawing** a vessel fails.
- **A transfer into a vessel holding a different beer** fails, with an error explaining that blending different beers isn't modelled.

## The brewer's iPad UI

The mockup holds the reference behaviour. Each screen below matches one in the mockup.

### Top bar

The tabs are **Tanks · Packaged · Schedule**, plus a prominent **Brew day** button. **Packaged** is the chiller's kegs, cans and bottles. It holds the count, Repack and Out, not sold, because all three act on packaged beer rather than on a vessel. A brewer thinks "I'm brewing today", not "I'm doing something to FV-2", which is why brewing is a top-level button rather than an action buried under a tank.

### Tanks (home)

- **Tiles, grouped by area:**
  - Brewhouse: FV-1 to FV-7 and BT-1
  - Chiller: SV-1 to SV-5
  - Barrels
- **Each tile shows:** the vessel id and size, the beer, "stage · day N" ("Aging · N months" for a barrel), a fill bar, what's next, and a badge: Hold (amber), Due (cognac), Dirty, Empty, or On tap (hop green, serving tanks only).
- **A "This week" panel** lists:
  - tanks to clean
  - planned brews, each with a Brew day button
  - beer due to be packaged, each with a Package it button
  - serving tanks nearly out, based on the sales count
  - the weekly count
  - holds and their release dates

  Each item that needs action opens the right form, pre-filled.

### Tank detail

The detail screen shows the beer and batch, the stage and day count, the volume, the hold explanation when there is one ("The old Galactic Orbit has 3.75 bbl left and runs out around Dec 20. Dry hopping around Dec 8 has this batch ready right as it does."), the actions, and the last five history entries. **The newest entry has Undo**, and so does the confirmation banner. Older mistakes go to Don ("tell Don and he fixes them from his screen").

The actions depend on the vessel and what's in it:

| Vessel state | Actions |
|---|---|
| Fermenter with beer | Dry hopped, Moved it, Packaged it, Spilled or dumped, Became a variant |
| Fermenter, empty | Brewed into it (plus Cleaned it if it's dirty) |
| BT-1 with beer | Packaged it, Moved to a serving tank, Read the level, Spilled or dumped |
| SV with beer | It kicked (if pouring) or On tap now (if waiting), Packaged it (kegs only from a serving tank; confirm), Read the level, Spilled or dumped |
| SV, BT-1 or barrel, empty | Filled it (plus Cleaned it if it's dirty) |
| Barrel with beer | Pulled it, Packaged it, Spilled or dumped |

**Form rules** (each one came out of the review):

- **Packaged it** ("Kegged it" on a serving tank, which only fills kegs):
  - First pick what was packaged: **Kegs**, **Cans** or **Bottles**. A run normally does one of these; mixing is allowed.
  - Kegs: ½ and ⅙ steppers. Cans and bottles: **cases** plus **loose units**, per format, where the tenant has more than one can size.
  - Every count **starts at 0**, with −, + and +5 buttons. A number pad suits the larger can counts.
  - A live summary of "x bbl packaged, y left".
  - A warning if the total is more than the tank holds.
  - Can and bottle runs get an optional "units lost on the line" stepper. Losses on a canning line are normal and matter for both TTB and cost; it's optional because the shortfall shows up anyway when the tank is marked empty.
- **"Is now empty" box, on packaging, moves and fills:** ticked automatically when the packaged units or the move use up roughly the whole tank (≥85% for packaging, within 0.3 bbl for moves), unticked otherwise. The brewer can override it. When ticked, whatever is left is written off and the vessel is marked dirty. An unticked box leaves a partial volume behind, so splitting a tank across two destinations is two moves.
- **Moved it:**
  - The destination chips show each vessel's state: empty, dirty ("logs a clean too"), the same beer ("Add to its 7.6 bbl"), a different beer (disabled), or full.
  - From a fermenter there's also **Barrels (N empty)**, with a "barrels filled" stepper.
  - Volumes use −1, −, +, +1 steppers (0.1 bbl steps), capped at what the destination has room for.
- **Pulled it (from a barrel):** the volume **starts at 0**, and the brewer enters what actually came out, read in the receiving tank. The shortfall from 1.7 bbl is evaporation, written off.
- **Spilled or dumped:** a gallon stepper (1 and +5) with "≈ x bbl" shown underneath, or a tick for "Dumped the whole thing". Small spills only get logged if they can be entered in gallons.
- **When:** a chip row for Today, Yesterday, then back through the last 7 days. Backdated events are stamped at noon local time so they can't drift across a day boundary (COG had this bug).
- **Wording:** plain words. "Written off", never "booked as loss". The iPad never mentions TTB or "Kit".

### Brew day

- **Pre-filled from the plan** ("Wicked Nebula (planned) → FV-2").
- **Beer:** the planned beer, plus **Something else**, which opens the full beer list and **New beer** (type a name). A new beer is saved straight away, and Don gets a queue item to set it up.
- **Fermenter:**
  - Empty ones are pickable, and dirty ones are too ("Dirty: logs a clean too").
  - A fermenter brewed into *today* that still has room offers **"Add to today's X"**, for the double brew into FV-7. The second brew adds to the same batch.
  - Fermenters holding beer are disabled.
- **Volume knocked out:** defaults to the brewhouse size (10 bbl), in 0.5 steps.
- **An optional brew-sheet photo**, stored with the batch as the paper record. **No OCR in v1.**
- **Logged at knockout:** one entry at the end of brew day, not a start and a finish.

### Schedule

- **Four weeks of lanes** for the fermenters and BT-1: in the tank, holding (amber), planned brew (dashed), cleaning, free. The free time is shown on purpose: "FV-7 is free all month" is a planning signal.
- **Read-only for the brewer.** Tapping a planned brew shows why it's planned ("old batch runs out Nov 22") and a **"This date won't work"** button that sends Don a note. Don owns the plan.

### Packaged

The Packaged tab is the packaged inventory as the system calculates it: per beer and format, with the oldest batch's age and a "past shelf life" flag. It has three actions:

- **Count**, described below.
- **Repack or make a variant:**
  1. **Took:** the beer, then the format, then the count (default 1). For example, "1 × ½ Newtonian".
  2. **Made:** the same beer (preselected) or **a variant**: an existing variant beer, or **New** (type a name). Then the output counts per format, starting at 0: kegs, or cans and bottles as cases plus units.
  3. A live summary: "0.50 bbl in → 0.47 bbl out · 0.03 bbl written off". It warns if more comes out than went in.
  4. For a variant, an optional note on "what did you add?" (Cashew, grapefruit). Don prices the additions later.

  This covers canning or bottling from an old keg, breaking a ½ into sixtels, and building a new beer in a sixtel.
- **Out, not sold**, described below.

### Count (weekly)

- **Blind.** Every row starts as "–". The brewer steps each cell, or taps **None** for a beer they don't have. **Save stays locked until every row has been touched** ("Save count (N to go)"). The variance shows only after saving, and Don reviews it.
- Rows are per beer **and format**: kegs (full and partial ½ and ⅙), cans and bottles (cases plus loose units). Only formats on hand or expected are shown.
- **"Out, not sold":** reason chips (Dumped / Event or gift / Came back full / Other), the beer, the format and the count.
- The rows are the beers expected to be in the chiller. A row can be added for an unexpected beer.
- Once the calculated inventory keeps matching the counts, the count can go from weekly to every 2–4 weeks (Don's setting).

### How often the brewer touches it

| When | What | Time |
|---|---|---|
| Brew day (1–2× a week) | Brew day → confirm → photo | ~1 min |
| As it happens | Dry hopped, moved, packaged, kicked, cleaned, spilled | ~30 s each |
| Now and then | Repack or make a variant (break a keg down, can or bottle off a keg, build a variant in a sixtel) | ~1 min |
| Weekly (Monday) | Blind count on the Packaged tab | ~5 min |
| Never | Costs, ingredients, invoices, sales, planning | — |

## Paper vs app

| Log | Decision |
|---|---|
| Brew sheet (BeerSmith printout plus handwritten actuals) | **Stays on paper.** A photo gets attached to the batch on brew day. |
| Tank clipboard (gravity and temperature) | **Stays on paper.** (Assumption; confirm with the brewer.) |
| Cellar and packaging log | **Replaced** by the iPad. |
| Chiller inventory, including the sheet's Inventory tab | **Replaced** by the count screen. |
| Paper calendar | **Replaced eventually** by the Schedule. Until then, a printed two-week view can hang where the calendar does. |

## Don's console

- **Setup and go-live:** vessels, barrels and package formats (with the per-format packaging materials). A **day-one load** of what's in each vessel (beer, batch, brew date, volume) and of the packaged counts per beer and format (kegs, cans, bottles), with batch where it's known.
- **Beers:** the planning and production settings, aliases, planning family, active and staple. Import the Beers tab from the sheet. **The style-default shelf lives need the brewer's correction.**
- **The plan:** the planner's output (statuses, run-out dates, suggested brews slotted onto real free fermenters), plan entries, holds and release rules. Accept, move or edit plan entries.
- **Corrections:** edit or reverse any event, at any age. Every edit is a reversing event plus a new event, never an in-place change.
- **Queues:** new beers to set up, flagged plan dates, count variances, "recipe missing" (later), unmatched Ramp lines (later).
- **Reports:** state of the system, COGS per batch, per beer and per package format, the TTB draft, and the feed.
- **MCP and agent tools:** read state, the plan, COGS and TTB. Writes go through the same service methods as the iPad. Follow kit's agent and MCP parity rule.

## Inventory

**Bulk** (beer in vessels) is the running sum of vessel events.

**Packaged** is calculated:

> on hand, per beer and format = packaged − sold (Square to-go and retail, wholesale) − used in the taproom − out, not sold

A repack moves units between lots: it takes the oldest lot of the input first (unless a batch is picked) and creates new lots for the outputs, which keep their batch link. For a variant, the link is to the derived batch, which links to its parent. Kegs are the only format "used in the taproom". Cans and bottles leave inventory only by being sold (Square or wholesale) or recorded as out, not sold. Crowlers never enter packaged inventory.

- **Kegs used in the taproom are inferred from Square:** ounces poured per beer ÷ keg size, assigned to the oldest kegs first. There is deliberately no "tapped a keg" button, because the person tapping kegs is usually bartending, not brewing. The periodic count corrects the drift.
- **Serving-tank volume** is the last reading or event, minus estimated Square depletion between events. A kick or level reading records the difference.
- **Batch tracking is first in, first out, behind the scenes.** Each package run line (batch × format) is a lot. Sales and taproom use draw from the oldest lot of that beer, and a count resets the lots. The count screen never shows batches. This is what makes "fresh until", stale-beer alerts and cost per *sold* unit possible. Cans and bottles go stale too: shelf life is per format.
- **Barrel-aged beer:** anything packaged from a barrel (kegs or bottles) is counted under the BA variant (for example, "BA Tsar Bomba"). Confirm the naming rule with Don.

## Planning

Port `build_brew_plan_sheet.py`'s model (above) into kit, running on kit's live Square data plus wholesale. Then add what the sheet can't do:

1. **The pipeline.** Batches in vessels count as supply arriving on their predicted ready date (brew date plus the stage durations, or the hold's release date). The Light Lift Lager "runs out Nov 15" false alarm, with 10 bbl sitting in a fermenter, goes away.
2. **Capacity.** Ideal brew dates get slotted onto real free fermenters, accounting for holds, cleaning, BT-1 as a shared one-tank bottleneck for beers that use it, serving-tank space, and FV-7 needing two brews. Clashes get flagged. The result is the actual schedule.
3. **Holds.** The release date is computed from the old batch's run-out date minus the time from dry hop to ready. The plan shows what a hold costs ("FV-3 held for Galactic Orbit blocks Wicked Nebula's brew-now slot").
4. **Live inputs.** Rates update from the Square sync with no CSV exports. Inventory comes from the calculation above, not a hand count.

The output feeds the Schedule tab, This week and the feed. The Google Sheet is retired once the console view replaces it.

## COGS

- **Per batch:** the recipe quantities (from the batch's locked recipe snapshot, scaled to the brewed volume), plus any deviations the brewer or Don entered, × each ingredient's running average cost at the moment of use. Add the batch's share of its yeast pack.
- **Per bbl** = batch cost ÷ bbl packaged from that batch.
- **Per keg** = per bbl × keg size. Loss raises the cost per keg automatically, because the cost is spread over fewer kegs.
- **Per can or bottle** = per bbl × the unit's volume, **plus packaging materials**: the can and lid or the bottle and cap, the label, and the carrier or case. These are real per-unit costs, unlike kegs. They come from Ramp purchases like any other ingredient and are applied per unit packaged, using a per-format bill of materials Don sets up once. If a mobile canner does the run, their per-unit or per-run charge is a packaging cost too. Units lost on the line raise the cost of the units that remain.
- **Repacks and variants:** output cost = the input's share of its batch cost (by bbl), plus new packaging materials for any cans or bottles. For a variant, add the additions (Don prices them, or later a Ramp-matched ingredient). Repack loss raises the cost of the output units. A derived batch carries its parent's cost per bbl into its own.
- **Kegs themselves aren't a cost** (they come back). Chemicals and CO₂ are overhead, and an allocation can come later. Excise is applied on removal, not in COGS.
- **Brewed vs acquired:** bought-in finished beer carries a lump-sum cost and is excluded from TTB production figures.
- **Gross-profit commission (later):** invoice revenue − COGS per unit × units sold, by rep.

### Ingredients and recipes (deferred, but designed)

The brewer's screens **don't change** when this arrives. One optional addition: a collapsed "Ingredients (from the recipe)" section on brew day for recording swaps, plus a "Fresh pack / Repitched from FV-x" choice for yeast.

- **Recipes come from BeerSmith, read only:**
  1. BeerSmith runs on an **old offline office PC that Don wants to retire anyway**. Migrate it to an online office machine with **Google Drive for desktop**.
  2. Move BeerSmith's data folder (the `.bsmx` files) into a Drive folder, if BeerSmith allows that. **Check it.** If it doesn't, the fallback is "export BeerXML to that folder" as part of finishing a recipe.
  3. **Share only that folder with a kit service account** that has `drive.readonly` scope. This is a new capability; reuse `googlecalendar/token.go`'s approach. A scheduled task (`scheduler.RegisterScheduledTask`) picks up new and changed files.
  4. **Every import is a recipe version.** A batch locks in the version current at brew time.
- **On day one, the migrated data folder is the whole recipe library.** Import it all at once.
- **New recipes are frequent, and they never block a brew.** A batch with no recipe goes to Don's "recipe missing" queue, and its cost fills in once the recipe arrives.
  - **Fallbacks:** upload on the iPad (the file picker reaches Drive and iCloud), or a photo of the printout.
  - **Check BeerSmith licence transfer** to the new machine.
- **Ingredient names are matched to the catalog once, by Don.** BeerSmith's "Vienna Malt (Weyermann)" maps to the catalog item. Kit suggests the match and remembers it.
- **Purchases come from Ramp.** Supplier invoice lines (Country Malt, hop suppliers, yeast) become purchases with quantity and cost, and Don confirms the mapping from supplier SKU to ingredient and pack size. That sets each ingredient's running average cost.
- **Usage is inferred from the recipe** at brew day and dry hop. A **monthly** quick count of the hop freezer and grain corrects drift, and the variance itself is a useful signal.
- **Shopping list:** planned brews × recipes vs on hand → "order before the next brews", sent to Don.
- **Unit:** stored on the catalog row. No conversion; buying in kg and mashing in lb means two catalog rows.

## TTB

- **Reports:** the Brewer's Report of Operations (TTB F 5130.9, or the quarterly 5130.26 if Gravity qualifies; **confirm with whoever files**) and excise.
- **Inputs:**
  - **Produced:** `brew` events (brewed only, not acquired).
  - **Packaged:** package runs (kegs, cans, bottles).
  - **Removed:** Square and wholesale for packaged beer. Serving-tank removals depend on the next point.
  - **Losses, by kind:** transfer shortfalls, write-offs, spills, dumps, kicks and level differences, barrel evaporation, count variance.
  - **Beginning and ending inventory:** bulk and packaged reported separately.
- **"Tax determined serving tanks."** If the notice means what it says, beer counts as removed for tax **when it goes into an SV**, and taproom pours don't matter for TTB. **Confirm with the CPA.**
- **Open for the CPA:**
  - When do kegs used in the taproom count as removed? (It depends on how the premises are laid out on the notice.)
  - Is a whole-tank dump "destroyed" or a loss?
  - Which line does each "out, not sold" reason go on?
  - How is repacking already-packaged beer reported (a keg canned off, a variant built in a sixtel)?
- **Excise rates are tenant config, not constants:** federal $3.50/bbl and Colorado $2.48/bbl per COG's 0061; verify both.

## Feed

All of these are derived; none needs new input:
- brew-now, stop-wholesale and won't-last statuses
- holds coming due
- serving tanks and packaged beer running low
- beer past or nearing shelf life
- an empty fermenter with nothing planned
- a batch yielding below the beer's average
- count variances
- (later) ingredient price changes ("Citra up 18% on the last order") and cost per bbl trends

Delivered as Don's cards and briefings in kit, plus This week on the iPad (the brewer-relevant subset only).

## Integrations

| Source | Use | Status |
|---|---|---|
| Square | Taproom sales, pours, days on tap, kegs, cans and bottles sold to go, depletion | In kit (`square`, `squaresales`). **Needs the variation and pour size kept** |
| Wholesale | Kegs (and any cases) sold per beer and account | **Source: the distribution spreadsheet, for now.** Kit may not have access to it; see "Wholesale input" below |
| Untappd / menu | Beer names, on tap | In kit (`menu`) |
| Ramp | Ingredient purchases and costs | New (deferred) |
| Google Drive | BeerSmith recipes | New, read-only, one shared folder (deferred) |

### Wholesale input

**The source is the distribution spreadsheet, for now.** It's recorded by hand, one workbook per year, on Drive. `automation`'s sales-tax pipeline (`scripts/sales-tax/fetch.py`) caches each year as `data/co-sales-tax/dist-<year>.xlsx`, and `scripts/sales/build_wholesale.py` turns them into `wholesale_line_items.csv` and the other tidy CSVs.

**Read that script's "WHAT THIS DATA CAN AND CANNOT TELL YOU" block before touching any of this.** In short:
- **Layout:** one sheet per rep, plus a "Total" sheet for house accounts. The three are additive. Each sheet is a stack of month blocks. Columns move between years, so they're found by header text.
- **The beer is free text** in the Style column ("[2] Nebula, [1] Acceleration"), full of abbreviations and typos. The script has an alias table and leaves anything ambiguous unresolved rather than guessing.
- **The container comes from the quantity columns:** 1/6 BBL, 1/2 BBL, 750ml Cases, Crowlers, 16oz Cases, 12oz Cases. Lines that mix containers can't be split by beer.
- **Money is per line, not per beer.** Per-beer dollars are a pro-rata estimate.
- **It records what was invoiced.** Returns and deposits are in their own columns and aren't netted off.

**Kit may or may not get access to the spreadsheet,** so the COGS app must not depend on reading it directly. The design:

1. **Wholesale is a pluggable, dated input.** The app stores wholesale lines (date, account, beer, container, units, bbl, resolved flag, source) and records how far they cover ("through Oct 30"). The planner and inventory treat the data as covering up to that date. They extrapolate the rate past it the way the sheet already does for the month in progress, and show a "wholesale data is N days old" warning when it's stale.
2. **v1: `automation` pushes the lines in.** After `build_wholesale.py` runs, a small step sends the tidy lines to kit through an MCP tool or API endpoint (`cogs_import_wholesale`, idempotent per invoice line). The parser stays in Python, where it's maintained. Kit needs no Drive access, and nothing new is exposed.
3. **Later, if wanted:** share the workbook folder read-only with kit's service account (the same mechanism as the BeerSmith folder) and port the parser to Go. Or move wholesale into kit or QuickBooks properly. That's out of scope for now.
4. **Manual fallback:** upload the CSV in Don's console.

The import is also how wholesale kegs, cans and bottles leave the calculated packaged inventory. Lines whose beer isn't resolved go to Don's queue rather than being dropped.

## Phases

1. **Ledger and iPad.** Delivers the state view on its own.
   - Vessels, beers, batches, package formats, events, package runs, lots, repacks and variants, counts and out-not-sold.
   - The iPad screens per the mockup.
   - Pairing.
   - Don's setup, day-one load, corrections and the queues.
2. **Planner.** Replaces the sheet.
   - Port the demand model and aliases, running on kit's Square data plus wholesale.
   - Pipeline, capacity, holds.
   - The Schedule and This week driven by it.
   - The feed.
3. **COGS.**
   - BeerSmith recipes over Drive.
   - Ramp purchases with running average cost.
   - Usage from recipes.
   - Yeast.
   - Packaging materials per format.
   - Cost per batch, bbl and unit (keg, can, bottle), including repacks and variants.
4. **TTB.** Report drafts once the CPA questions are answered.
5. **Later:** gross-profit commission, wholesale paperwork, menu sync.

## Open questions

**For the brewer:**
- What is BT-1 actually used for, and how often? Where does beer get carbonated? Does serving-tank beer skip BT-1?
- Barrel names, count and size. Are barrels filled and pulled as a set or one at a time? Topped up?
- Do gravity and temperature readings stay on paper?
- Cans and bottles (confirmed as packaged; crowlers are taproom only):
  - Which can sizes (16oz, 12oz, both)?
  - How are they sold: 4-packs, 6-packs, cases of 24? Are bottle cases 12 × 750ml?
  - Is canning done in-house, or by a mobile canner? Is bottling in-house?
  - Are bottles bottle-conditioned? (That adds a conditioning stage after packaging.)
  - How often does each happen, and from which vessels? Don thinks **mostly from BT-1**, but **sometimes from an old keg** (that's a repack).
- Repacks and variants: how often are kegs broken down, and how often are variants built in sixtels versus in a tank? Which current beers are variants (Cashew Newtonian, Grapefruit Orbit, the Quasar flavours…), and should each count toward its parent's demand?
- Does packaged beer leave without a Square sale or invoice (events, collabs, dumps, returns)?
- What does the brewer call each step ("knock out", "transfer", "crash")? Use their words on the buttons.
- Realistic shelf lives per beer.

Already answered by Don: levels on BT-1 and the SVs can be read; blending is rare but must work; logging will be as it happens.

**For the CPA or whoever files with TTB:**
- Which TTB form and filing frequency?
- Are the SVs tax determined?
- When do taproom kegs count as removed?
- Dump vs loss lines.
- Should the brewer's notice be amended for the current equipment?

**For Don:**
- Naming of BA variants.
- Does kit get access to the distribution spreadsheet? Until it does, `automation` pushes the lines in (see "Wholesale input").
- Whether to keep a printed calendar during the transition.

## Decisions log

| Decision | Why | Rejected alternative |
|---|---|---|
| Build in kit as an `app_cogs` app | Kit already has Square, the menu and Untappd, devices and kiosk, cards and briefings, MCP; brewery use is ~95% of kit's use | Keep COG (never used, form-heavy, a separate stack) |
| Port the sheet's model | It's the one piece actually in use, and it's well reasoned | Redesign from scratch |
| iPad only, permanently signed in, no PIN | Low stakes, everything undoable, and a PIN doesn't stop an unlocked iPad; use Guided Access | A PIN, or desktop entry |
| Brew day as a top-level button | Brewers think "I'm brewing", not "something happened to a tank" | An action under a tank tile only |
| Blind count; keg counts start at 0 | They're the ground truth for losses and COGS, and a pre-fill becomes a rubber stamp | Pre-filled "looks right" |
| One "Repack" action covers splitting kegs, packaging from a keg, and building a variant | They're all "packaged units in, packaged units out", and a variant is just a different output beer. One form, with the loss and cost rules falling out of it | Separate split, can-from-keg and variant flows; or treating a keg as a vessel |
| One "Packaged it" action for kegs, cans and bottles | It's the same event (bulk to package, from a batch); only the formats differ, and the formats are tenant config | Separate keg, canning and bottling forms |
| Counts per fill, not keg serials | Scanning every keg fill and return is real extra work, and lost shells aren't a known cost | Barcode or RFID per keg |
| Taproom keg use inferred from Square | The person tapping is bartending; the count corrects drift | A "tapped a keg" button |
| Schedule read-only on the iPad, with a "won't work" note | Moving dates on the iPad would make it a second planning tool | Moving dates on the iPad |
| No stage pills | Nothing would advance them, so they'd be wrong; show "stage · day N" until the next event | A stage timeline |
| Brew-sheet photo as a plain attachment, no OCR | No goal needs OCR yet, and it's an unreliable promise | Extracting gravity and volumes from the photo |
| Blending allowed only with the same beer | Real but rare; different-beer blends aren't modelled and fail with a clear error | No blending, or a full blend model |
| Losses in gallons | Real spills are a few gallons; a 0.5 bbl step means they'd never be logged | bbl steps |
| Dirty vessel usable, with an implied clean | A forgotten "Cleaned it" must not block brew day | Hard block |
| Kit reads BeerSmith, never edits recipes | BeerSmith is the authoring tool and the brewer is happy with it | Rebuilding recipe management |
| Wholesale pushed in from `automation`, as a dated input | The spreadsheet stays the source, kit may not get access to it, and the parser is already maintained in Python | Kit reads the workbook directly; QuickBooks as the source |
| Drive folder shared with a service account | Least privilege: only that one folder is visible | Broad Drive OAuth, or email ingest |

## Appendix: review findings and what was done

An independent review of the mockup (2026-11-02), against "the simplest system, but no simpler".

**Adopted:**
- **Counts:** a blind count, with the variance shown only after saving; keg counts start at 0 (with +5).
- **Volumes:** blending into a vessel with the same beer; barrel pulls ask the actual volume; kicks and level reads record the difference against expected.
- **Kegs:** batch tracking first in, first out behind the scenes; "kegs out, not sold".
- **Forms:** a 7-day date picker; dry hop available on every fermenter with beer in it.
- **Setup:** Don's day-one load and setup screen.
- **Cuts:** no stage pills; the photo as an attachment only; automatic on-tap for a sibling SV after a kick; a read-only schedule with "won't work".
- **Brewer UX:** a dirty vessel can be picked anyway; losses in gallons; plainer wording.

**Changed from the review's suggestion:**
- **The "now empty" box:** the review wanted it unticked by default. It's ticked automatically when the kegs or the move roughly empty the tank, and unticked otherwise. Leaving it unticked on a normal keg-out would leave phantom beer in the tank, which is worse than the occasional wrong tick that Undo fixes.
- **Undo beyond the newest entry:** the iPad undoes only the newest entry; older fixes go through Don's console as reversing events. That keeps the iPad simple and the ledger clean.
