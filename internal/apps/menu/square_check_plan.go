package menu

import (
	"fmt"
	"sort"
	"strings"
)

// sqPlan is what one look at the menu and Square found.
type sqPlan struct {
	Adds      []sqAdd
	Questions []sqQuestion
	// Notes are gaps the check saw and left, said in the preview.
	Notes []string
}

// sqAdd is one change with a right answer, applied without asking.
type sqAdd struct {
	Summary string
	Objects []map[string]any
	// Stock lists the temp ids of new stock-tracked variations, counted in
	// at inStock once Square has given them real ids.
	Stock []string
	// Fresh is the menu row whose item this add creates.
	Fresh string
}

// sqQuestion is one gap without a right answer, asked on a decision card.
type sqQuestion struct {
	// Key is stable for the same gap with the same numbers, so a card is
	// filed once and a changed price asks again.
	Key         string
	Title       string
	Body        string
	Options     []sqOption
	Recommended string
}

type sqOption struct {
	ID    string
	Label string
	Fix   *sqFix // nil: nothing to do in Square
}

// sqFix is what an approved option does. It travels as the tool arguments
// of apply_menu_square_fix, so it holds ids and amounts, never objects: the
// object is read fresh when the fix runs.
type sqFix struct {
	// Kind is "price" (reprice a variation), "flight_price" (reprice a
	// flight option), "variation" (add a size to an item) or "item"
	// (create the menu row Name as a new item).
	Kind     string `json:"kind"`
	ObjectID string `json:"object_id,omitempty"`
	Name     string `json:"name,omitempty"`
	Cents    int    `json:"cents,omitempty"`
}

type planner struct {
	cat  sqCatalog
	rows []Beer
	// fresh names the rows whose item this run created. Only those get a
	// size filled in from the house price without asking: on an item that
	// already existed, a missing size may well be on purpose.
	fresh map[string]bool
	plan  sqPlan
	temps int
}

// planSquare compares the menu with Square. Pure: everything it reads is
// passed in, which is what lets the tests hold it to the rules above.
func planSquare(rows []Beer, cat sqCatalog, fresh map[string]bool) sqPlan {
	p := &planner{cat: cat, rows: rows, fresh: fresh}
	for _, b := range rows {
		p.row(b)
	}
	p.houseSizes()
	p.flight()
	p.crowlers()
	return p.plan
}

func (p *planner) temp(prefix string) string {
	p.temps++
	return fmt.Sprintf("#%s-%d", prefix, p.temps)
}

// pricedPours is every pour on the row with a price.
func pricedPours(b Beer) []Pour {
	var out []Pour
	for _, pr := range b.Pours {
		if _, ok := pourCents(pr.Price); ok && pr.Price != "" {
			out = append(out, pr)
		}
	}
	return out
}

// isTap is a row poured from a keg, which is what the flight, the crowlers
// and a new item's template are about. Cans and juice boxes are not.
func isTap(b Beer) bool {
	for _, pr := range b.Pours {
		if isDraft(pr.Label) {
			return true
		}
	}
	return false
}

// isSoda tells our craft sodas and hop water from beer: Untappd shows no ABV
// for them. Only meaningful for a tap row.
func isSoda(b Beer) bool { return strings.TrimSpace(b.ABV) == "" }

// variationName is what Square calls a pour: "16oz" for a draft, "64oz
// Growler" for anything else.
func variationName(pr Pour) string {
	if isDraft(pr.Label) {
		return pr.Size
	}
	return strings.TrimSpace(pr.Label)
}

// pourVariation finds the variation a pour rings as. A drink sold in one
// glass may ring it as "Regular", whatever size Untappd prints.
func pourVariation(b Beer, it sqItem, pr Pour) (sqVar, bool) {
	if v, ok := it.variation(variationName(pr)); ok {
		return v, true
	}
	if v, ok := it.variation(pr.Size); ok {
		return v, true
	}
	if pr.Size != tasterPour && glassCount(b) == 1 {
		return it.variation(regularVariation)
	}
	return sqVar{}, false
}

// glassCount is how many sizes other than the taster the row is sold in.
func glassCount(b Beer) int {
	n := 0
	for _, pr := range pricedPours(b) {
		if pr.Size != tasterPour {
			n++
		}
	}
	return n
}

func (p *planner) row(b Beer) {
	pours := pricedPours(b)
	if len(pours) == 0 {
		return
	}
	it, ok := p.cat.match(b.Name)
	if !ok {
		p.missingItem(b)
		return
	}
	for _, pr := range pours {
		p.pour(b, it, pr)
	}
}

func (p *planner) pour(b Beer, it sqItem, pr Pour) {
	want, _ := pourCents(pr.Price)
	v, ok := pourVariation(b, it, pr)
	if !ok {
		p.addVariation(it, variationName(pr), want, b.Name+": added "+variationName(pr)+" at "+dollars(want))
		return
	}
	if v.Cents == want {
		return
	}
	p.askPrice(b.Name, it.Name, v, want, "the menu")
}

// askPrice files a menu-versus-Square price question.
func (p *planner) askPrice(beer, item string, v sqVar, want int, source string) {
	have := "no price"
	if v.Cents >= 0 {
		have = dollars(v.Cents)
	}
	p.plan.Questions = append(p.plan.Questions, sqQuestion{
		Key:   fmt.Sprintf("price|%s|%s|%d|%d", v.ID, nameKey(source), v.Cents, want),
		Title: fmt.Sprintf("%s %s: %s or %s?", beer, v.Name, dollars(want), have),
		Body: fmt.Sprintf("%s shows **%s %s at %s**, but Square rings **%s** (item %s). "+
			"Pick the right price. If it is Square's, fix %s instead.",
			titleCase(source), beer, v.Name, dollars(want), have, item, source),
		Options: []sqOption{
			{ID: "use_menu", Label: "Set Square to " + dollars(want), Fix: &sqFix{Kind: "price", ObjectID: v.ID, Cents: want}},
			{ID: "keep", Label: "Keep " + have + ", I'll fix " + source},
		},
		Recommended: "use_menu",
	})
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// missingItem handles a menu row with no item to ring it as.
func (p *planner) missingItem(b Beer) {
	if old, ok := p.cat.archived(b.Name); ok {
		p.plan.Questions = append(p.plan.Questions, sqQuestion{
			Key:   "archived|" + old.ID,
			Title: b.Name + " is on the menu but archived in Square",
			Body: fmt.Sprintf("The menu lists **%s**, and its Square item is archived, so it cannot be rung up. "+
				"Unarchive it in Square, or take it off the menu.", b.Name),
			Options: []sqOption{{ID: "ok", Label: "Got it"}},
		})
		return
	}
	if near, ok := p.cat.near(b.Name); ok {
		p.plan.Questions = append(p.plan.Questions, sqQuestion{
			Key:   "rename|" + nameKey(b.Name) + "|" + near.ID,
			Title: fmt.Sprintf("Is %s the same as %s in Square?", b.Name, near.Name),
			Body: fmt.Sprintf("The menu lists **%s**. Square has nothing by that name, but has **%s**. "+
				"If it is the same drink, set the Square item's kitchen name to %q and the check will match them.",
				b.Name, near.Name, b.Name),
			Options: []sqOption{
				{ID: "same", Label: "Same drink, I'll fix the name"},
				{ID: "create", Label: "Different drink, add it to Square", Fix: &sqFix{Kind: "item", Name: b.Name}},
			},
			Recommended: "same",
		})
		return
	}
	if !isTap(b) {
		p.plan.Questions = append(p.plan.Questions, sqQuestion{
			Key:   "missing|" + nameKey(b.Name),
			Title: b.Name + " is on the menu but not in Square",
			Body: fmt.Sprintf("The printed menu lists **%s**, and Square has no item by that name, so it cannot be rung up. "+
				"Kit only creates draft drinks by itself; add this one in Square, or give the Square item it rings as the kitchen name %q.",
				b.Name, b.Name),
			Options: []sqOption{{ID: "ok", Label: "Got it"}},
		})
		return
	}
	add, ok := p.createItem(b)
	if !ok {
		p.plan.Notes = append(p.plan.Notes, b.Name+": not in Square, and no drink like it to copy the set up from")
		return
	}
	p.plan.Adds = append(p.plan.Adds, add)
}

// template is the item a new tap row is set up like: the first one of the
// same kind (beer or soda) on the menu that rings every pour the row has.
func (p *planner) template(b Beer) (sqItem, bool) {
	for _, other := range p.rows {
		if !isTap(other) || isSoda(other) != isSoda(b) || nameKey(other.Name) == nameKey(b.Name) {
			continue
		}
		it, ok := p.cat.match(other.Name)
		if !ok {
			continue
		}
		all := true
		for _, pr := range pricedPours(b) {
			if _, ok := pourVariation(b, it, pr); !ok {
				all = false
				break
			}
		}
		if all {
			return it, true
		}
	}
	return sqItem{}, false
}

// siblings are the Square items of the other tap rows of the same kind.
func (p *planner) siblings(b Beer) []sqItem {
	var out []sqItem
	for _, other := range p.rows {
		if !isTap(other) || isSoda(other) != isSoda(b) || nameKey(other.Name) == nameKey(b.Name) {
			continue
		}
		if it, ok := p.cat.match(other.Name); ok {
			out = append(out, it)
		}
	}
	return out
}

// houseSizes adds the sizes the menu does not price -- a pitcher, a crowler,
// a taster -- that most drinks of the same kind carry. It copies a price only
// when every one of them charges the same; otherwise it asks.
func (p *planner) houseSizes() {
	for _, b := range p.rows {
		if !isTap(b) {
			continue
		}
		it, ok := p.cat.match(b.Name)
		if !ok {
			continue
		}
		sibs := p.siblings(b)
		for _, name := range commonSizes(sibs) {
			if _, has := it.variation(name); has || p.menuSize(b, it, name) {
				continue
			}
			p.houseSize(b, it, name, sibs)
		}
	}
}

// menuSize reports whether a size is one the menu prices for this row, which
// pour() has already dealt with.
func (p *planner) menuSize(b Beer, it sqItem, name string) bool {
	for _, pr := range pricedPours(b) {
		if nameKey(variationName(pr)) == nameKey(name) || nameKey(pr.Size) == nameKey(name) {
			return true
		}
		if v, ok := pourVariation(b, it, pr); ok && nameKey(v.Name) == nameKey(name) {
			return true
		}
	}
	return false
}

// commonSizes is every variation name more than half the items carry, kegs
// aside: a keg is a wholesale price, set when a beer goes out to accounts.
func commonSizes(items []sqItem) []string {
	if len(items) == 0 {
		return nil
	}
	count := map[string]int{}
	first := map[string]string{}
	for _, it := range items {
		for _, v := range it.Vars {
			k := nameKey(v.Name)
			if strings.Contains(k, "keg") {
				continue
			}
			if _, ok := first[k]; !ok {
				first[k] = v.Name
			}
			count[k]++
		}
	}
	var out []string
	for k, n := range count {
		if n*2 > len(items) {
			out = append(out, first[k])
		}
	}
	sort.Strings(out)
	return out
}

func (p *planner) houseSize(b Beer, it sqItem, name string, sibs []sqItem) {
	prices := map[int]bool{}
	for _, s := range sibs {
		if v, ok := s.variation(name); ok && v.Cents >= 0 {
			prices[v.Cents] = true
		}
	}
	if len(prices) == 1 && p.fresh[nameKey(b.Name)] {
		for c := range prices {
			p.addVariation(it, name, c, fmt.Sprintf("%s: added %s at %s, the price every other one has", b.Name, name, dollars(c)))
		}
		return
	}
	var list []int
	for c := range prices {
		list = append(list, c)
	}
	sort.Ints(list)
	q := sqQuestion{
		Key:   "size|" + it.ID + "|" + nameKey(name),
		Title: fmt.Sprintf("What does a %s of %s cost?", name, b.Name),
		Body: fmt.Sprintf("Most of our drinks sell a **%s**, and **%s** has none in Square. "+
			"Pick its price, or say it is not sold that way.", name, b.Name),
	}
	if len(list) == 1 {
		q.Recommended = fmt.Sprintf("c%d", list[0])
	}
	for _, c := range list {
		q.Options = append(q.Options, sqOption{
			ID: fmt.Sprintf("c%d", c), Label: dollars(c),
			Fix: &sqFix{Kind: "variation", ObjectID: it.ID, Name: name, Cents: c},
		})
	}
	q.Options = append(q.Options, sqOption{ID: "none", Label: "We don't sell it in that size"})
	p.plan.Questions = append(p.plan.Questions, q)
}
