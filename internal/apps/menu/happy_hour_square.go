package menu

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/square"
	"github.com/mrdon/kit/internal/models"
)

// Happy hour, as Square rings it.
//
// Square has no "set the price to $5" discount, only amount-off and
// percent-off, and the beers on a happy hour rarely share a regular price.
// So the sync groups the beers by how much comes off each one -- $1.50 off a
// $6.50 pint, $3 off an $8 one -- and writes, per group, a fixed-amount
// discount, a product set naming those beers' pour variations, and a pricing
// rule tying the two to one shared weekly time period. Rules created through
// the API apply automatically: the bartender rings a pint as usual and the
// register takes the discount off inside the window.
//
// The objects are rebuilt whole on every sync rather than patched. Replacements
// are created first and the previous set deleted after, so a sync that fails
// halfway leaves the last good happy hour ringing instead of none, and a price
// change in Square flows through on the next sync without anyone editing this.

// hhBeer is one beer resolved to the Square variation the price applies to.
type hhBeer struct {
	Name        string // as configured
	ItemName    string // as Square names it
	VariationID string
	PriceCents  int
	Currency    string
}

// hhGroup is the beers sharing one discount amount.
type hhGroup struct {
	OffCents int
	Beers    []hhBeer
}

// hhPlan is what a sync would write, and what stops it.
type hhPlan struct {
	Groups []hhGroup
	// Problems block the sync: a beer that does not match, or has no pour at
	// the happy hour size. Ringing happy hour on two of three beers while the
	// wall advertises three is worse than not ringing it.
	Problems []string
	// Notes do not block: a beer already at or under the happy hour price.
	Notes []string
}

// planHappyHour resolves the configured beers against the catalog.
func planHappyHour(h HappyHour, items []square.CatalogItem) hhPlan {
	var plan hhPlan
	byOff := map[int][]hhBeer{}
	for _, name := range h.Beers {
		item, ok := matchItem(name, items)
		if !ok {
			plan.Problems = append(plan.Problems, fmt.Sprintf(
				"%q matches no Square item by name or kitchen name%s", name, nearest(name, items)))
			continue
		}
		v, ok := findVariation(item, h.Size)
		if !ok {
			plan.Problems = append(plan.Problems, fmt.Sprintf(
				"%s has no %q variation in Square (it has %s)", item.Name, h.Size, variationNames(item)))
			continue
		}
		if v.PriceCents < 0 {
			plan.Problems = append(plan.Problems, fmt.Sprintf(
				"%s %s has no fixed price in Square, so there is nothing to discount from", item.Name, v.Name))
			continue
		}
		off := v.PriceCents - h.PriceCents
		if off <= 0 {
			plan.Notes = append(plan.Notes, fmt.Sprintf(
				"%s %s is already $%s, at or under the happy hour price; no discount needed",
				item.Name, v.Name, formatCents(v.PriceCents)))
			continue
		}
		byOff[off] = append(byOff[off], hhBeer{
			Name: name, ItemName: item.Name, VariationID: v.ID,
			PriceCents: v.PriceCents, Currency: v.Currency,
		})
	}
	offs := make([]int, 0, len(byOff))
	for off := range byOff {
		offs = append(offs, off)
	}
	sort.Ints(offs)
	for _, off := range offs {
		plan.Groups = append(plan.Groups, hhGroup{OffCents: off, Beers: byOff[off]})
	}
	return plan
}

func matchItem(name string, items []square.CatalogItem) (square.CatalogItem, bool) {
	key := nameKey(name)
	for _, it := range items {
		if nameKey(it.Name) == key || (it.KitchenName != "" && nameKey(it.KitchenName) == key) {
			return it, true
		}
	}
	return square.CatalogItem{}, false
}

func findVariation(item square.CatalogItem, size string) (square.CatalogVariation, bool) {
	for _, v := range item.Variations {
		if nameKey(v.Name) == nameKey(size) {
			return v, true
		}
	}
	return square.CatalogVariation{}, false
}

func variationNames(item square.CatalogItem) string {
	names := make([]string, len(item.Variations))
	for i, v := range item.Variations {
		names[i] = v.Name
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

// nearest suggests catalog names sharing a word with an unmatched beer, so a
// rename is a glance rather than a search through the catalog.
func nearest(name string, items []square.CatalogItem) string {
	words := strings.Fields(nameKey(name))
	var hits []string
	for _, it := range items {
		for _, w := range words {
			if len(w) > 3 && strings.Contains(nameKey(it.Name), w) {
				hits = append(hits, it.Name)
				break
			}
		}
	}
	if len(hits) == 0 {
		return ""
	}
	if len(hits) > 3 {
		hits = hits[:3]
	}
	return " (closest: " + strings.Join(hits, ", ") + ")"
}

// hhEvent is the iCalendar event Square's time period takes. DTSTART is local,
// unzoned time, which Square reads in the location's own timezone.
func hhEvent(h HappyHour, first time.Time) string {
	start, _ := parseClock(h.Start)
	end, _ := parseClock(h.End)
	days := make([]string, len(h.Days))
	for i, d := range h.Days {
		days[i] = strings.ToUpper(d[:2])
	}
	return fmt.Sprintf("DTSTART:%sT%02d%02d00\nDURATION:%s\nRRULE:FREQ=WEEKLY;BYDAY=%s",
		first.Format("20060102"), start/60, start%60, isoDuration(end-start), strings.Join(days, ","))
}

// firstDay is the first scheduled day on or after from, so DTSTART is itself
// an occurrence -- RFC 5545 counts DTSTART even when the rule would not.
func firstDay(h HappyHour, from time.Time) time.Time {
	for i := range 7 {
		d := from.AddDate(0, 0, i)
		if slices.Contains(h.Days, dayCodes[(int(d.Weekday())+6)%7]) {
			return d
		}
	}
	return from
}

func isoDuration(minutes int) string {
	h, m := minutes/60, minutes%60
	switch {
	case m == 0:
		return fmt.Sprintf("PT%dH", h)
	case h == 0:
		return fmt.Sprintf("PT%dM", m)
	}
	return fmt.Sprintf("PT%dH%dM", h, m)
}

// hhObjects is the catalog batch for a plan, with "#temp" ids that refer to
// each other inside the one request.
func hhObjects(h HappyHour, plan hhPlan, first time.Time) []map[string]any {
	objs := []map[string]any{{
		"type":             "TIME_PERIOD",
		"id":               "#hh-time",
		"time_period_data": map[string]any{"event": hhEvent(h, first)},
	}}
	for i, g := range plan.Groups {
		ids := make([]string, len(g.Beers))
		names := make([]string, len(g.Beers))
		for j, b := range g.Beers {
			ids[j], names[j] = b.VariationID, b.ItemName
		}
		currency := g.Beers[0].Currency
		if currency == "" {
			currency = "USD"
		}
		disc, set, rule := fmt.Sprintf("#hh-discount-%d", i), fmt.Sprintf("#hh-set-%d", i), fmt.Sprintf("#hh-rule-%d", i)
		ruleData := map[string]any{
			"name":              "Happy Hour",
			"time_period_ids":   []string{"#hh-time"},
			"discount_id":       disc,
			"match_products_id": set,
		}
		if h.StartsOn != "" {
			ruleData["valid_from_date"] = h.StartsOn
		}
		objs = append(objs,
			map[string]any{"type": "DISCOUNT", "id": disc, "discount_data": map[string]any{
				"name":             "Happy Hour",
				"discount_type":    "FIXED_AMOUNT",
				"amount_money":     map[string]any{"amount": g.OffCents, "currency": currency},
				"modify_tax_basis": "MODIFY_TAX_BASIS",
			}},
			map[string]any{"type": "PRODUCT_SET", "id": set, "product_set_data": map[string]any{
				"name":            "Happy Hour: " + strings.Join(names, ", "),
				"product_ids_any": ids,
			}},
			map[string]any{"type": "PRICING_RULE", "id": rule, "pricing_rule_data": ruleData},
		)
	}
	return objs
}

// describePlan writes what a sync will do, beer by beer.
func describePlan(h HappyHour, plan hhPlan, first time.Time, b *strings.Builder) {
	fmt.Fprintf(b, "Schedule: %s %s–%s, first on %s\n", describeDays(h.Days),
		clockLabel(h.Start), clockLabel(h.End), first.Format("Mon 2 Jan 2006"))
	for _, g := range plan.Groups {
		for _, beer := range g.Beers {
			fmt.Fprintf(b, "  %s %s: $%s → $%s ($%s off)\n", beer.ItemName, h.Size,
				formatCents(beer.PriceCents), h.Price(), formatCents(g.OffCents))
		}
	}
	for _, n := range plan.Notes {
		fmt.Fprintf(b, "  note: %s\n", n)
	}
	for _, p := range plan.Problems {
		fmt.Fprintf(b, "  PROBLEM: %s\n", p)
	}
}

// hhOutcome is what a sync leaves to record. ids is what Square holds for
// happy hour afterwards: the new objects on success, and on failure whatever
// is still standing, so the next attempt cleans it up.
type hhOutcome struct {
	ok     bool
	record bool
	ids    []string
	hash   string
}

// SyncHappyHour brings Square into line with the setting. With apply false it
// only reports what it would do. The log is the whole story, Square's own
// error text included, because it is shown verbatim on the settings page --
// and it is recorded once, here, after the failure hint is on it.
func (a *App) SyncHappyHour(ctx context.Context, tenantID uuid.UUID, apply bool) (string, bool) {
	var log strings.Builder
	out, err := a.syncHappyHour(ctx, tenantID, apply, &log)
	if err != nil {
		fmt.Fprintf(&log, "\nFAILED: %v\n", err)
		if errors.Is(err, square.ErrMissingScope) {
			log.WriteString("\nKit's Square token cannot edit the catalog. Add the ITEMS_READ and " +
				"ITEMS_WRITE permissions to the token (or paste a production access token, which " +
				"carries every permission) under Integrations → Square, then sync again.\n")
		}
		out.ok = false
	}
	text := strings.TrimRight(log.String(), "\n")
	if apply && out.record {
		if rerr := RecordHappyHourSync(ctx, a.pool, tenantID, out.ids, out.hash, text, out.ok); rerr != nil {
			text += "\n\nCould not record this sync: " + rerr.Error()
			out.ok = false
		}
	}
	return text, out.ok
}

func (a *App) syncHappyHour(ctx context.Context, tenantID uuid.UUID, apply bool, log *strings.Builder) (hhOutcome, error) {
	state, err := LoadHappyHour(ctx, a.pool, tenantID)
	if err != nil {
		return hhOutcome{}, err
	}
	if !state.Configured {
		log.WriteString("No happy hour is set up yet, so there is nothing to send to Square.\n")
		return hhOutcome{}, nil
	}
	h := state.Config
	// From here on a failure is recorded, keeping the previous objects on file.
	fail := hhOutcome{record: true, ids: state.SquareIDs}

	client, err := square.Instance().LoadClient(ctx, tenantID)
	if err != nil {
		return fail, fmt.Errorf("connecting to Square: %w", err)
	}
	if !h.Enabled {
		return clearHappyHour(ctx, client, state, apply, log), nil
	}

	log.WriteString("Reading the Square catalog… ")
	items, err := client.ListCatalogItems(ctx)
	if err != nil {
		log.WriteString("failed.\n")
		return fail, fmt.Errorf("reading the catalog: %w", err)
	}
	fmt.Fprintf(log, "%d items.\n\n", len(items))

	plan := planHappyHour(h, items)
	first := firstDay(h, startFrom(h, time.Now().In(a.tenantLocation(ctx, tenantID))))
	describePlan(h, plan, first, log)

	switch {
	case len(plan.Problems) > 0:
		log.WriteString("\nNothing was sent to Square. Fix the beer names above (in the happy hour " +
			"setting, or in Square) and sync again.\n")
		return fail, nil
	case len(plan.Groups) == 0:
		log.WriteString("\nEvery beer is already at or under the happy hour price; nothing to discount.\n")
		return fail, nil
	case !apply:
		fmt.Fprintf(log, "\nPreview only. Syncing would create %d Square objects and remove the %d from the last sync.\n",
			1+3*len(plan.Groups), len(state.SquareIDs))
		return hhOutcome{ok: true}, nil
	}

	log.WriteString("\nCreating the discount in Square… ")
	mapping, err := client.BatchUpsertCatalog(ctx, uuid.NewString(), hhObjects(h, plan, first))
	if err != nil {
		log.WriteString("failed. The previous happy hour, if any, is untouched.\n")
		return fail, err
	}
	created := make([]string, 0, len(mapping))
	for _, id := range mapping {
		created = append(created, id)
	}
	sort.Strings(created)
	fmt.Fprintf(log, "done, %d objects.\n", len(created))

	leftover := removeOld(ctx, client, state.SquareIDs, log)
	log.WriteString("\nSquare is up to date. Ring a test pint during the window to check it.\n")
	return hhOutcome{ok: true, record: true, ids: append(created, leftover...), hash: h.Hash()}, nil
}

// startFrom is the day Square's schedule should begin: the launch date when
// that is still ahead, otherwise today.
func startFrom(h HappyHour, now time.Time) time.Time {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if h.StartsOn != "" {
		if d, err := time.ParseInLocation(time.DateOnly, h.StartsOn, now.Location()); err == nil && d.After(today) {
			return d
		}
	}
	return today
}

// clearHappyHour removes what the last sync created, for a happy hour that
// has been switched off.
func clearHappyHour(ctx context.Context, client *square.Client, state *HappyHourState,
	apply bool, log *strings.Builder,
) hhOutcome {
	hash := state.Config.Hash()
	if len(state.SquareIDs) == 0 {
		log.WriteString("Happy hour is off and Square has nothing from Kit to remove.\n")
		return hhOutcome{ok: true, record: true, hash: hash}
	}
	if !apply {
		fmt.Fprintf(log, "Happy hour is off. Syncing would remove the %d Square objects the last sync created.\n",
			len(state.SquareIDs))
		return hhOutcome{ok: true}
	}
	log.WriteString("Happy hour is off.\n")
	leftover := removeOld(ctx, client, state.SquareIDs, log)
	return hhOutcome{ok: len(leftover) == 0, record: true, ids: leftover, hash: hash}
}

// removeOld deletes the previous sync's objects and returns any it could not,
// so they are kept on record and retried next time rather than forgotten.
func removeOld(ctx context.Context, client *square.Client, ids []string, log *strings.Builder) []string {
	if len(ids) == 0 {
		return nil
	}
	log.WriteString("Removing the previous happy hour from Square… ")
	if _, err := client.BatchDeleteCatalog(ctx, ids); err != nil {
		fmt.Fprintf(log, "failed: %v\nThey will be retried on the next sync.\n", err)
		return ids
	}
	fmt.Fprintf(log, "done, %d objects.\n", len(ids))
	return nil
}

// tenantLocation is the workspace's timezone, which the wall's window and
// Square's start date are both read in. A workspace without one falls back to
// UTC and says so in the logs rather than guessing.
func (a *App) tenantLocation(ctx context.Context, tenantID uuid.UUID) *time.Location {
	tenant, err := models.GetTenantByID(ctx, a.pool, tenantID)
	if err != nil || tenant == nil {
		return time.UTC
	}
	return locationOf(tenant.Timezone)
}

func locationOf(tz string) *time.Location {
	if tz == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.UTC
	}
	return loc
}
