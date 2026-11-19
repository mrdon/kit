package menu

import (
	"fmt"
	"strings"
	"unicode"
)

// The objects the check adds. Each is built by copying a neighbour -- a
// variation of the same item, the item of a drink of the same kind, an
// existing flight option -- so its tax, categories, channels and stock
// tracking are whatever the house already chose, and only the name and the
// price are new. Existing objects are never sent back: adding a variation
// or a flight option is its own object pointing at its parent, so nothing
// Kit did not mean to touch can be lost in the write.

// addVariation adds one size to an existing item, set up like its others.
func (p *planner) addVariation(it sqItem, name string, cents int, summary string) {
	p.plan.Adds = append(p.plan.Adds, sqAdd{
		Summary: summary,
		Objects: []map[string]any{newVariation(it, p.temp("var"), it.ID, name, cents)},
	})
}

// newVariation copies the first variation of the item it is going into.
func newVariation(it sqItem, id, itemID, name string, cents int) map[string]any {
	src := map[string]any{}
	if len(it.Vars) > 0 {
		src = it.Vars[0].Raw
	}
	srcData := obj(src, "item_variation_data")
	data := map[string]any{
		"item_id":         itemID,
		"name":            name,
		"ordinal":         nextOrdinal(it),
		"pricing_type":    "FIXED_PRICING",
		"price_money":     map[string]any{"amount": cents, "currency": currencyOf(srcData)},
		"sellable":        true,
		"stockable":       true,
		"track_inventory": srcData["track_inventory"] == true,
	}
	if ch, ok := srcData["channels"]; ok {
		data["channels"] = ch
	}
	out := map[string]any{"type": "ITEM_VARIATION", "id": id, "item_variation_data": data}
	copyPresence(src, out)
	return out
}

func nextOrdinal(it sqItem) int {
	n := len(it.Vars)
	for _, v := range it.Vars {
		if f, ok := obj(v.Raw, "item_variation_data")["ordinal"].(float64); ok && int(f) >= n {
			n = int(f) + 1
		}
	}
	return n
}

func currencyOf(data map[string]any) string {
	if c := str(obj(data, "price_money"), "currency"); c != "" {
		return c
	}
	return "USD"
}

// copyPresence copies which locations an object is at.
func copyPresence(src, dst map[string]any) {
	for _, k := range []string{"present_at_all_locations", "present_at_location_ids", "absent_at_location_ids"} {
		if v, ok := src[k]; ok {
			dst[k] = v
		}
	}
}

// itemFields are what a new item copies from its template: everything about
// how it rings and where it shows, nothing about which drink it is.
var itemFields = []string{
	"is_taxable", "tax_ids", "categories", "reporting_category", "product_type",
	"channels", "label_color", "skip_modifier_screen", "modifier_list_info", "ecom_visibility",
}

// createItem sets up a menu row as a new item, like the drink of its kind
// that rings every pour it has. Its sizes are the menu's pours at the menu's
// prices; the sizes the menu does not price (a pitcher, a crowler) follow on
// the next pass through houseSizes, once the item exists.
func (p *planner) createItem(b Beer) (sqAdd, bool) {
	tpl, ok := p.template(b)
	if !ok {
		return sqAdd{}, false
	}
	id := p.temp("item")
	name := squareName(b.Name)
	data := map[string]any{"name": name, "abbreviation": abbreviation(name)}
	if notes := strings.TrimSpace(b.Notes); notes != "" {
		data["description"] = notes
	}
	tplData := obj(tpl.Raw, "item_data")
	for _, k := range itemFields {
		if v, ok := tplData[k]; ok {
			data[k] = v
		}
	}
	var vars []any
	var sizes []string
	for i, pr := range pricedPours(b) {
		v, _ := pourVariation(b, tpl, pr)
		cents, _ := pourCents(pr.Price)
		nv := newVariation(sqItem{Vars: []sqVar{v}}, p.temp("var"), id, v.Name, cents)
		obj(nv, "item_variation_data")["ordinal"] = i
		vars = append(vars, nv)
		sizes = append(sizes, v.Name+" "+dollars(cents))
	}
	data["variations"] = vars
	item := map[string]any{"type": "ITEM", "id": id, "item_data": data}
	copyPresence(tpl.Raw, item)
	created := "created in Square"
	if name != b.Name {
		created += " as " + name
	}
	return sqAdd{
		Summary: fmt.Sprintf("%s: %s, set up like %s (%s)", b.Name, created, tpl.Name, strings.Join(sizes, ", ")),
		Objects: []map[string]any{item},
		Fresh:   b.Name,
	}, true
}

// abbreviation is the two letters on a register button, as the house writes
// them: "Gaia Green Soda" is "Ga".
func abbreviation(name string) string {
	var out []rune
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out = append(out, r)
			if len(out) == 2 {
				break
			}
		}
	}
	if len(out) == 2 {
		out[1] = unicode.ToLower(out[1])
	}
	return string(out)
}

// flight offers every tap drink with a taster in the flight picker. Each
// option's upcharge is its own taster price over the cheapest taster on the
// menu, which is how the picker has always been priced.
func (p *planner) flight() {
	if p.cat.Flight == nil {
		p.plan.Notes = append(p.plan.Notes, "no "+flightListName+" list in Square, so the flight was not checked")
		return
	}
	type tap struct {
		row   Beer
		item  sqItem
		cents int
	}
	var taps []tap
	base := -1
	for _, b := range p.rows {
		if !b.OnTap() {
			continue
		}
		it, ok := p.cat.match(b.Name)
		if !ok {
			continue
		}
		pr, _ := b.PourBySize(tasterPour)
		v, ok := pourVariation(b, it, pr)
		if !ok || v.Cents < 0 {
			continue
		}
		taps = append(taps, tap{b, it, v.Cents})
		if base < 0 || v.Cents < base {
			base = v.Cents
		}
	}
	next := len(p.cat.Flight.Mods)
	for _, t := range taps {
		want := t.cents - base
		mod, ok := p.cat.Flight.find(t.item.Name)
		switch {
		case !ok:
			p.plan.Adds = append(p.plan.Adds, sqAdd{
				Summary: fmt.Sprintf("%s: added to %s at +%s", t.row.Name, flightListName, dollars(want)),
				Objects: []map[string]any{p.newFlightOption(t.item.Name, want, next)},
			})
			next++
		case mod.Cents != want:
			p.askFlightPrice(t.row.Name, mod, want, base)
		}
	}
}

func (p *planner) newFlightOption(name string, cents, ordinal int) map[string]any {
	return map[string]any{
		"type":                     "MODIFIER",
		"id":                       p.temp("mod"),
		"present_at_all_locations": true,
		"modifier_data": map[string]any{
			"name":             name,
			"price_money":      map[string]any{"amount": cents, "currency": "USD"},
			"ordinal":          ordinal,
			"modifier_list_id": p.cat.Flight.ID,
		},
	}
}

func (p *planner) askFlightPrice(beer string, mod sqMod, want, base int) {
	p.plan.Questions = append(p.plan.Questions, sqQuestion{
		Key:   fmt.Sprintf("flight|%s|%d|%d", mod.ID, mod.Cents, want),
		Title: fmt.Sprintf("%s in a flight: +%s or +%s?", beer, dollars(want), dollars(max(mod.Cents, 0))),
		Body: fmt.Sprintf("A flight pour costs its own taster price over the cheapest taster (%s). For **%s** that is "+
			"**+%s**, but %s charges **+%s**.", dollars(base), beer, dollars(want), flightListName, dollars(max(mod.Cents, 0))),
		Options: []sqOption{
			{ID: "use_taster", Label: "Charge +" + dollars(want), Fix: &sqFix{Kind: "flight_price", ObjectID: mod.ID, Cents: want}},
			{ID: "keep", Label: "Keep +" + dollars(max(mod.Cents, 0))},
		},
		Recommended: "use_taster",
	})
}

// crowlers lists every tap drink that sells a crowler in the online shop's
// crowler item, at the crowler price its own item rings, and counts it in.
func (p *planner) crowlers() {
	missingList := map[string]bool{}
	for _, b := range p.rows {
		if !isTap(b) {
			continue
		}
		it, ok := p.cat.match(b.Name)
		if !ok {
			continue
		}
		own, ok := it.variation(crowlerVariation)
		if !ok || own.Cents < 0 {
			continue
		}
		listName := beerCrowlerItem
		if isSoda(b) {
			listName = sodaCrowlerItem
		}
		list, ok := p.cat.item(listName)
		if !ok {
			missingList[listName] = true
			continue
		}
		p.crowler(b, it, own, list)
	}
	for name := range missingList {
		p.plan.Notes = append(p.plan.Notes, "no "+name+" item in Square, so its crowlers were not checked")
	}
}

func (p *planner) crowler(b Beer, it sqItem, own sqVar, list sqItem) {
	listed, ok := list.variation(it.Name)
	if !ok {
		id := p.temp("crowler")
		p.plan.Adds = append(p.plan.Adds, sqAdd{
			Summary: fmt.Sprintf("%s: added to %s at %s, in stock", b.Name, list.Name, dollars(own.Cents)),
			Objects: []map[string]any{newVariation(list, id, list.ID, it.Name, own.Cents)},
			Stock:   []string{id},
		})
		return
	}
	if listed.Cents == own.Cents {
		return
	}
	p.plan.Questions = append(p.plan.Questions, sqQuestion{
		Key:   fmt.Sprintf("crowler|%s|%d|%d", listed.ID, listed.Cents, own.Cents),
		Title: fmt.Sprintf("%s crowler: %s or %s?", b.Name, dollars(own.Cents), dollars(max(listed.Cents, 0))),
		Body: fmt.Sprintf("At the bar a **%s** crowler rings **%s**, but online (%s) it is **%s**.",
			b.Name, dollars(own.Cents), list.Name, dollars(max(listed.Cents, 0))),
		Options: []sqOption{
			{ID: "match_bar", Label: "Set online to " + dollars(own.Cents), Fix: &sqFix{Kind: "price", ObjectID: listed.ID, Cents: own.Cents}},
			{ID: "keep", Label: "Leave them different"},
		},
		Recommended: "match_bar",
	})
}
