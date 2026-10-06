package menu

import (
	"context"
	"errors"
	"fmt"
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
// So the beers are grouped by how much comes off each one -- $1.50 off a
// $6.50 pint, $3 off an $8 one -- and each group gets a fixed-amount
// discount, a product set naming those beers' pour variations, and a pricing
// rule tying the two together. Rules created through the API apply
// automatically: the bartender rings a pint as usual and the register takes
// the discount off.
//
// The rules carry no time period. Square's own schedule could only ever say
// what the timetable says, and happy hour here can be started and ended by
// hand, so Square mirrors the on/off state instead: while happy hour is on
// the rules exist, and while it is off they do not. Kit checks every minute
// (see happy_hour_schedule.go) and right after Start now / End now, and it
// only calls Square when the state Square should be in has changed.
//
// The objects are rebuilt whole rather than patched. Replacements are created
// first and the previous set deleted after, so a failure halfway leaves the
// last good set in place, and a price change in Square flows through on the
// next change without anyone editing this.

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

// hhObjects is the catalog batch for a plan, with "#temp" ids that refer to
// each other inside the one request.
func hhObjects(plan hhPlan) []map[string]any {
	var objs []map[string]any
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
			map[string]any{"type": "PRICING_RULE", "id": rule, "pricing_rule_data": map[string]any{
				"name":              "Happy Hour",
				"discount_id":       disc,
				"match_products_id": set,
			}},
		)
	}
	return objs
}

// describePlan writes what Square rings while happy hour is on, beer by beer.
func describePlan(h HappyHour, plan hhPlan, b *strings.Builder) {
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

// hhOutcome is what a push leaves to record. ids is what Square holds for
// happy hour afterwards: the new objects on success, and on failure whatever
// is still standing, so the next attempt cleans it up.
type hhOutcome struct {
	ok     bool
	record bool
	ids    []string
}

// SyncHappyHour makes Square match the happy hour as it is right now, or with
// apply false only reports what Square would ring. The log is the whole
// story, Square's own error text included, because the settings page shows it
// verbatim.
func (a *App) SyncHappyHour(ctx context.Context, tenantID uuid.UUID, apply bool) (string, bool) {
	state, err := LoadHappyHour(ctx, a.pool, tenantID)
	if err != nil {
		return "FAILED: " + err.Error(), false
	}
	return a.pushHappyHour(ctx, tenantID, state, !apply)
}

// reconcileHappyHour is the every-minute check, and what Start now / End now
// call: push only when the state Square should be in has changed. A push that
// failed is retried after retryFailedPush rather than every minute, so a token
// without permission produces one log entry every few minutes, not sixty an
// hour.
func (a *App) reconcileHappyHour(ctx context.Context, tenantID uuid.UUID) error {
	state, err := LoadHappyHour(ctx, a.pool, tenantID)
	if err != nil || !state.Configured {
		return err
	}
	key := state.squareKey(timeNow(), a.tenantLocation(ctx, tenantID))
	if state.SyncedHash == key {
		if state.SyncOK {
			return nil
		}
		if state.SyncedAt != nil && timeNow().Sub(*state.SyncedAt) < retryFailedPush {
			return nil
		}
	}
	if state.SyncedAt == nil && key == "off" && len(state.SquareIDs) == 0 {
		return nil // never pushed and nothing to remove: Square is already right
	}
	log, ok := a.pushHappyHour(ctx, tenantID, state, false)
	if !ok {
		return errors.New(log)
	}
	return nil
}

// retryFailedPush is how long a failed push waits before the minute check
// tries it again. Start now, End now and the Sync button do not wait.
const retryFailedPush = 10 * time.Minute

// pushHappyHour brings Square to the state wanted now, and records the result.
func (a *App) pushHappyHour(ctx context.Context, tenantID uuid.UUID, state *HappyHourState, preview bool) (string, bool) {
	var log strings.Builder
	loc := a.tenantLocation(ctx, tenantID)
	key := state.squareKey(timeNow(), loc)
	out, err := a.push(ctx, tenantID, state, key == "off", preview, &log)
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
	if !preview && out.record {
		if rerr := RecordHappyHourSync(ctx, a.pool, tenantID, out.ids, key, text, out.ok); rerr != nil {
			text += "\n\nCould not record this sync: " + rerr.Error()
			out.ok = false
		}
	}
	return text, out.ok
}

func (a *App) push(ctx context.Context, tenantID uuid.UUID, state *HappyHourState, off, preview bool, log *strings.Builder) (hhOutcome, error) {
	if !state.Configured {
		log.WriteString("No happy hour is set up yet, so there is nothing to send to Square.\n")
		return hhOutcome{}, nil
	}
	h := state.Config
	fail := hhOutcome{record: true, ids: state.SquareIDs}
	if off {
		log.WriteString("Happy hour is off right now, so Square should have no happy hour discount.\n")
	} else {
		log.WriteString("Happy hour is on right now.\n")
	}

	client, err := square.Instance().LoadClient(ctx, tenantID)
	if err != nil {
		return fail, fmt.Errorf("connecting to Square: %w", err)
	}
	if off && !preview {
		left := removeOld(ctx, client, state.SquareIDs, log)
		return hhOutcome{ok: len(left) == 0, record: true, ids: left}, nil
	}

	log.WriteString("Reading the Square catalog… ")
	items, err := client.ListCatalogItems(ctx)
	if err != nil {
		log.WriteString("failed.\n")
		return fail, fmt.Errorf("reading the catalog: %w", err)
	}
	fmt.Fprintf(log, "%d items.\n\nWhile happy hour is on, Square rings:\n", len(items))
	plan := planHappyHour(h, items)
	describePlan(h, plan, log)

	switch {
	case len(plan.Problems) > 0:
		log.WriteString("\nNothing was sent to Square. Fix the beer names above (in the happy hour " +
			"setting, or in Square) and sync again.\n")
		return fail, nil
	case len(plan.Groups) == 0:
		log.WriteString("\nEvery beer is already at or under the happy hour price; nothing to discount.\n")
		return fail, nil
	case preview:
		log.WriteString("\nPreview only; nothing was sent to Square.\n")
		return hhOutcome{ok: true}, nil
	}

	log.WriteString("\nCreating the discount in Square… ")
	mapping, err := client.BatchUpsertCatalog(ctx, uuid.NewString(), hhObjects(plan))
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
	return hhOutcome{ok: true, record: true, ids: append(created, leftover...)}, nil
}

// removeOld deletes the previous push's objects and returns any it could not,
// so they are kept on record and retried next time rather than forgotten.
func removeOld(ctx context.Context, client *square.Client, ids []string, log *strings.Builder) []string {
	if len(ids) == 0 {
		log.WriteString("Square has no happy hour discount from Kit to remove.\n")
		return nil
	}
	log.WriteString("Removing the previous happy hour discount from Square… ")
	if _, err := client.BatchDeleteCatalog(ctx, ids); err != nil {
		fmt.Fprintf(log, "failed: %v\nThey will be retried.\n", err)
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
