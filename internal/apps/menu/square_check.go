package menu

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/mrdon/kit/internal/apps/square"
)

// The Square check: every drink on the menu should be something the register
// can ring, the flight picker can offer, and the online shop can sell as a
// crowler. A new soda went up on Untappd, onto the wall and onto paper, and
// nobody had put it in Square; this is the job that notices.
//
// It reads the menu the printed page reads (menuRows) and nothing else, so it
// can never disagree with what customers are shown. Square is matched by name
// the way happy hour matches it: the item's name or kitchen name, case and
// spacing aside, with no alias table. A leading vintage year is ignored:
// Untappd lists "2026 Tsar Bomba", and Square keeps years off the register.
//
// What it does about a gap depends on whether there is a right answer
// already written down somewhere. A missing pour, flight option or crowler
// listing takes its price from the menu or from Square itself, so it is
// added. A price that disagrees, a name that is nearly but not quite an
// existing item, or a size whose price differs from beer to beer has no
// right answer to copy, so it becomes a decision card for an admin. Nothing
// is ever removed or repriced without one: a beer off the wall for a day is
// still a beer we pour. Its online crowler is the one exception, taken off
// the shop until it is back (see square_check_stock.go), because a crowler
// is filled from the tap.

// The Square objects the check knows by name. A workspace without one of
// them simply skips that part of the check.
const (
	flightListName   = "Flight pours"
	beerCrowlerItem  = "Crowler To Go"
	sodaCrowlerItem  = "Craft Soda Crowler"
	crowlerVariation = "32oz Crowler"
	// regularVariation is how Square names the one glass of a drink sold in
	// a single size, such as a craft soda.
	regularVariation = "Regular"
	// inStock is the count that makes an online crowler orderable; 0 is how
	// staff mark one sold out.
	inStock = "99"
)

type sqVar struct {
	ID    string
	Name  string
	Cents int // -1 when unpriced
	Raw   map[string]any
}

type sqItem struct {
	ID          string
	Name        string
	KitchenName string
	// Archived items stay in the catalog but cannot be rung. They never
	// match a menu row, and a menu row named like one is asked about rather
	// than created again.
	Archived bool
	Vars     []sqVar
	Raw      map[string]any
}

type sqMod struct {
	ID    string
	Name  string
	Cents int
	Raw   map[string]any
}

type sqModList struct {
	ID   string
	Mods []sqMod
}

// sqCatalog is the part of Square the check reads.
type sqCatalog struct {
	Items  []sqItem
	Flight *sqModList // nil when the workspace has no flight picker
	// Counts is the stock of each online crowler listing by variation id;
	// nil when it was not read, which leaves stock alone.
	Counts map[string]string
}

// loadSquareCatalog reads every item and the flight picker.
func loadSquareCatalog(ctx context.Context, client *square.Client) (sqCatalog, error) {
	var cat sqCatalog
	items, err := client.ListCatalogObjects(ctx, "ITEM")
	if err != nil {
		return cat, fmt.Errorf("listing Square items: %w", err)
	}
	for _, o := range items {
		cat.Items = append(cat.Items, parseSqItem(o))
	}
	lists, err := client.ListCatalogObjects(ctx, "MODIFIER_LIST")
	if err != nil {
		return cat, fmt.Errorf("listing Square modifier lists: %w", err)
	}
	for _, o := range lists {
		data := obj(o, "modifier_list_data")
		if nameKey(str(data, "name")) != nameKey(flightListName) {
			continue
		}
		ml := &sqModList{ID: str(o, "id")}
		for _, m := range objs(data, "modifiers") {
			md := obj(m, "modifier_data")
			ml.Mods = append(ml.Mods, sqMod{ID: str(m, "id"), Name: str(md, "name"), Cents: money(md), Raw: m})
		}
		cat.Flight = ml
	}
	return cat, nil
}

func parseSqItem(o map[string]any) sqItem {
	data := obj(o, "item_data")
	archived, _ := data["is_archived"].(bool)
	it := sqItem{ID: str(o, "id"), Name: str(data, "name"), KitchenName: str(data, "kitchen_name"), Archived: archived, Raw: o}
	for _, v := range objs(data, "variations") {
		vd := obj(v, "item_variation_data")
		it.Vars = append(it.Vars, sqVar{ID: str(v, "id"), Name: str(vd, "name"), Cents: money(vd), Raw: v})
	}
	return it
}

// match finds the live item a menu row rings as.
func (c sqCatalog) match(name string) (sqItem, bool) {
	return c.matchAny(name, false)
}

// archived finds an archived item answering to the row's name.
func (c sqCatalog) archived(name string) (sqItem, bool) {
	return c.matchAny(name, true)
}

func (c sqCatalog) matchAny(name string, archived bool) (sqItem, bool) {
	for _, n := range menuNames(name) {
		key := nameKey(n)
		for _, it := range c.Items {
			if it.Archived != archived {
				continue
			}
			if nameKey(it.Name) == key || (it.KitchenName != "" && nameKey(it.KitchenName) == key) {
				return it, true
			}
		}
	}
	return sqItem{}, false
}

// vintageRe is a leading vintage year, as Untappd writes "2026 Tsar Bomba".
var vintageRe = regexp.MustCompile(`^(19|20)\d{2}\s+`)

// menuNames lists the names a menu row may ring as: its own, then without a
// leading vintage year, which Square names leave off.
func menuNames(name string) []string {
	name = strings.TrimSpace(name)
	if bare := vintageRe.ReplaceAllString(name, ""); bare != name && bare != "" {
		return []string{name, bare}
	}
	return []string{name}
}

// squareName is the name a menu row goes into Square under: without its
// vintage year, which belongs to Untappd's reviewers, not the register.
func squareName(name string) string {
	names := menuNames(name)
	return names[len(names)-1]
}

// item finds a catalog item by its own name, for the crowler listings.
func (c sqCatalog) item(name string) (sqItem, bool) {
	for _, it := range c.Items {
		if !it.Archived && nameKey(it.Name) == nameKey(name) {
			return it, true
		}
	}
	return sqItem{}, false
}

// near finds an item whose name is very nearly the row's: the same once
// punctuation and anything in brackets are dropped, or a letter or two out.
// Such a row is a rename for a person, never a new item: creating one would
// put the same beer on the register twice.
func (c sqCatalog) near(name string) (sqItem, bool) {
	for _, mn := range menuNames(name) {
		key := looseKey(mn)
		for _, it := range c.Items {
			if it.Archived {
				continue
			}
			for _, n := range []string{it.Name, it.KitchenName} {
				if n == "" {
					continue
				}
				if k := looseKey(n); k == key || closeness(k, key) >= 0.85 {
					return it, true
				}
			}
		}
	}
	return sqItem{}, false
}

func (it sqItem) variation(name string) (sqVar, bool) {
	for _, v := range it.Vars {
		if nameKey(v.Name) == nameKey(name) {
			return v, true
		}
	}
	return sqVar{}, false
}

func (l *sqModList) find(name string) (sqMod, bool) {
	for _, m := range l.Mods {
		if nameKey(m.Name) == nameKey(name) {
			return m, true
		}
	}
	return sqMod{}, false
}

// looseKey drops bracketed asides and punctuation: "Mr. Radar (Nitro)" and
// "Mr Radar" are one key.
func looseKey(s string) string {
	return normalizeBeerName(s)
}

// closeness is 1 minus the edit distance over the longer length.
func closeness(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	longest := max(len(ra), len(rb))
	if longest == 0 {
		return 1
	}
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return 1 - float64(prev[len(rb)])/float64(longest)
}

// pourCents reads a menu price ("4.50", "$3") into cents.
func pourCents(price string) (int, bool) {
	f, err := strconv.ParseFloat(strings.TrimPrefix(strings.TrimSpace(price), "$"), 64)
	if err != nil || f < 0 {
		return 0, false
	}
	return int(f*100 + 0.5), true
}

func dollars(cents int) string {
	return fmt.Sprintf("$%d.%02d", cents/100, cents%100)
}

func obj(m map[string]any, key string) map[string]any {
	v, _ := m[key].(map[string]any)
	return v
}

func objs(m map[string]any, key string) []map[string]any {
	list, _ := m[key].([]any)
	out := make([]map[string]any, 0, len(list))
	for _, v := range list {
		if o, ok := v.(map[string]any); ok {
			out = append(out, o)
		}
	}
	return out
}

func str(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// money reads price_money.amount, or -1 when there is none.
func money(m map[string]any) int {
	pm := obj(m, "price_money")
	if pm == nil {
		return -1
	}
	if f, ok := pm["amount"].(float64); ok {
		return int(f)
	}
	return -1
}
