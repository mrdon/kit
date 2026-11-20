package menu

import (
	"fmt"
	"math"
	"strconv"
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
	// Notice is a gap only a person can close in Square, with nothing to
	// choose: it goes out as a briefing, not a decision.
	Notice bool
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
	if len(pricedPours(b)) == 0 {
		return
	}
	limit, capped := maxGlassOz(b)
	for _, pr := range pricedPours(b) {
		if oz, ok := glassOz(pr); ok && capped && oz > limit {
			p.tooBig(b, pr.Size, limit, "the menu sells one, so Kit leaves it out of Square. Fix the pour on Untappd")
		}
	}
	it, ok := p.cat.match(b.Name)
	if !ok {
		p.missingItem(b)
		return
	}
	for _, pr := range servedPours(b) {
		p.pour(b, it, pr)
	}
	if !capped {
		return
	}
	for _, v := range it.Vars {
		if oz, ok := ozOf(v.Name); ok && oz > limit && !p.menuSize(b, it, v.Name) {
			p.tooBig(b, v.Name, limit, "Square still sells one. Take it off the item in Square")
		}
	}
}

// maxGlassOz is the biggest glass a beer is poured in: 9oz over 10% ABV,
// 12oz over 8%. Weaker beers and sodas have no limit.
func maxGlassOz(b Beer) (float64, bool) {
	abv, ok := abvOf(b)
	switch {
	case !ok:
		return 0, false
	case abv > 10:
		return 9, true
	case abv > 8:
		return 12, true
	}
	return 0, false
}

// glassOz is the size of a draft pour, in ounces.
func glassOz(pr Pour) (float64, bool) {
	if !isDraft(pr.Label) {
		return 0, false
	}
	return ozOf(pr.Size)
}

// ozOf reads a bare size such as "16oz", which is how Square names a glass.
// "32oz Crowler" is not a glass.
func ozOf(s string) (float64, bool) {
	n, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(s), "oz"), 64)
	return n, err == nil && strings.HasSuffix(strings.TrimSpace(s), "oz")
}

// servedPours is the priced pours, less any glass too big for the beer.
func servedPours(b Beer) []Pour {
	limit, capped := maxGlassOz(b)
	var out []Pour
	for _, pr := range pricedPours(b) {
		if oz, ok := glassOz(pr); ok && capped && oz > limit {
			continue
		}
		out = append(out, pr)
	}
	return out
}

// tooBig tells the admins about a glass bigger than the beer is poured in.
func (p *planner) tooBig(b Beer, size string, limit float64, what string) {
	p.plan.Questions = append(p.plan.Questions, sqQuestion{
		Key:    "glass|" + nameKey(b.Name) + "|" + nameKey(size),
		Title:  fmt.Sprintf("%s is %s, too strong for a %s", b.Name, b.ABV, size),
		Body:   fmt.Sprintf("We pour **%s** in a %goz glass at most, and %s.", b.Name, limit, what),
		Notice: true,
	})
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
		Title: fmt.Sprintf("Change %s %s in Square to %s?", beer, v.Name, dollars(want)),
		Body: fmt.Sprintf("%s shows **%s %s at %s**, but Square rings **%s** (item %s). "+
			"If Square is right, say no and fix %s instead.",
			titleCase(source), beer, v.Name, dollars(want), have, item, source),
		Options: []sqOption{
			{ID: "yes", Label: "Yes, ring " + dollars(want), Fix: &sqFix{Kind: "price", ObjectID: v.ID, Cents: want}},
			{ID: "no", Label: "No, keep " + have + ", I'll fix " + source},
		},
		Recommended: "yes",
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
			Notice: true,
		})
		return
	}
	if near, ok := p.cat.near(b.Name); ok {
		p.plan.Questions = append(p.plan.Questions, sqQuestion{
			Key:   "rename|" + nameKey(b.Name) + "|" + near.ID,
			Title: fmt.Sprintf("Is %s the same as %s in Square?", b.Name, near.Name),
			Body: fmt.Sprintf("The menu lists **%s**. Square has nothing by that name, but has **%s**. "+
				"If it is the same drink, set the Square item's kitchen name to %q and the check will match them.",
				b.Name, near.Name, squareName(b.Name)),
			Options: []sqOption{
				{ID: "same", Label: "Yes, I'll fix the name"},
				{ID: "create", Label: "No, add it to Square", Fix: &sqFix{Kind: "item", Name: b.Name}},
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
				b.Name, squareName(b.Name)),
			Notice: true,
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
		for _, pr := range servedPours(b) {
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

// houseSizes adds the sizes the menu does not price but the house always
// sells: a crowler of every tap drink, and a pitcher of every beer up to
// maxPitcherABV. Nothing else: a size the menu leaves off is not sold.
func (p *planner) houseSizes() {
	for _, b := range p.rows {
		if !isTap(b) {
			continue
		}
		it, ok := p.cat.match(b.Name)
		if !ok {
			continue
		}
		for _, name := range houseSizeNames(b) {
			if _, has := it.variation(name); has || p.menuSize(b, it, name) {
				continue
			}
			p.houseSize(b, it, name)
		}
	}
}

// maxPitcherABV is the strongest beer we pour by the pitcher.
const maxPitcherABV = 8.0

const pitcherVariation = "Pitcher"

func houseSizeNames(b Beer) []string {
	out := []string{crowlerVariation}
	if abv, ok := abvOf(b); ok && abv <= maxPitcherABV {
		out = append(out, pitcherVariation)
	}
	return out
}

// abvOf reads "6.2%". Sodas have none.
func abvOf(b Beer) (float64, bool) {
	f, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(b.ABV), "%"), 64)
	return f, err == nil
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

// houseSize prices a missing house size like the drink of the same kind
// that pours closest to it per ounce. A drink this run created takes that
// price without asking when every such drink charges the same; otherwise a
// yes/no card proposes it.
func (p *planner) houseSize(b Beer, it sqItem, name string) {
	near, cents, same, ok := p.sizePrice(b, name)
	if !ok {
		p.plan.Notes = append(p.plan.Notes, fmt.Sprintf("%s: no %s in Square, and no drink like it sells one to price it from", b.Name, name))
		return
	}
	if same && p.fresh[nameKey(b.Name)] {
		p.addVariation(it, name, cents, fmt.Sprintf("%s: added %s at %s, the price every other one has", b.Name, name, dollars(cents)))
		return
	}
	why := "We sell a crowler of everything on tap"
	if name == pitcherVariation {
		why = fmt.Sprintf("We sell a pitcher of every beer up to %g%%", maxPitcherABV)
	}
	p.plan.Questions = append(p.plan.Questions, sqQuestion{
		Key:   fmt.Sprintf("house|%s|%s|%d", it.ID, nameKey(name), cents),
		Title: fmt.Sprintf("Add a %s of %s at %s?", name, b.Name, dollars(cents)),
		Body: fmt.Sprintf("%s, and **%s** has none in Square. %s is priced like **%s**, which pours at a similar price per ounce.",
			why, b.Name, dollars(cents), near),
		Options: []sqOption{
			{ID: "yes", Label: "Yes, add it at " + dollars(cents), Fix: &sqFix{Kind: "variation", ObjectID: it.ID, Name: name, Cents: cents}},
			{ID: "no", Label: "No, I'll set it up in Square"},
		},
		Recommended: "yes",
	})
}

// sizePrice is the price of a size on the drink of the same kind whose
// largest glass costs nearest per ounce, and whether every such drink
// charges the same.
func (p *planner) sizePrice(b Beer, name string) (near string, cents int, same, ok bool) {
	want, _ := perOunce(b)
	best := -1.0
	prices := map[int]bool{}
	for _, other := range p.rows {
		if !isTap(other) || isSoda(other) != isSoda(b) || nameKey(other.Name) == nameKey(b.Name) {
			continue
		}
		it, found := p.cat.match(other.Name)
		if !found {
			continue
		}
		v, has := it.variation(name)
		if !has || v.Cents < 0 {
			continue
		}
		prices[v.Cents] = true
		po, _ := perOunce(other)
		if d := math.Abs(po - want); best < 0 || d < best {
			best, near, cents = d, other.Name, v.Cents
		}
	}
	return near, cents, len(prices) == 1, best >= 0
}

// perOunce is the menu price per ounce of the row's largest glass.
func perOunce(b Beer) (float64, bool) {
	oz, price := 0.0, 0
	for _, pr := range servedPours(b) {
		if !isDraft(pr.Label) {
			continue
		}
		n, err := strconv.ParseFloat(strings.TrimSuffix(pr.Size, "oz"), 64)
		c, ok := pourCents(pr.Price)
		if err != nil || !ok || n <= oz {
			continue
		}
		oz, price = n, c
	}
	if oz == 0 {
		return 0, false
	}
	return float64(price) / oz, true
}
