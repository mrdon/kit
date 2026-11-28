package menu

import (
	"fmt"
	"math"
	"slices"
	"strings"
)

// Everything that leaves the building is rung from a to-go item: one Square
// item per format, one variation per drink, named as the drink's own item.
// The drink's own item rings only what is poured at the bar, so its sales
// report as draft and the to-go items as what they are.
//
// Each to-go item is where its format's price lives, at the bar and online:
// once a drink is listed, nothing else is compared with it but the menu. A
// tap drink missing from an item it is sold from is listed in this order:
//
//   - at the menu's price, when Untappd prices the format (a growler);
//   - at the price of the old size on the drink's own item, from before the
//     to-go items rang at the bar (a "32oz Crowler" or "1/6 BBL Keg");
//   - otherwise priced from the drinks already listed: a crowler or growler
//     at the drink's own draft price per ounce times the share of it the
//     listed ones charge (the median, so one odd price does not move it); a
//     keg at the price of the listed beer nearest it per ounce, because kegs
//     are priced in a few tiers that barely follow the pint. A drink this
//     run created takes that price without asking, and the briefing says
//     how it was set; a drink that already existed is asked about on a
//     yes/no card, since leaving it out may be on purpose.
//
// When a drink leaves the menu its crowler and growler listings are deleted,
// because they are filled from the tap and Square cannot hide one variation
// from a channel through the API. Kegs are made unsellable instead, which
// hides them from the register and the website's shop alike: they sell for
// a hundred dollars and more, and a price someone set by hand should still
// be there when the beer comes back. A keg's stock is left to staff: one
// marked sold out because there is not enough beer to fill it stays sold out
// while its beer is on tap. It is counted back in only when its beer
// returns to the menu.

// toGo is one item a format is sold from.
type toGo struct {
	Item string // the Square item's name
	What string // the format, in what admins read
	// OldSize is the size on a drink's own item that used to ring this
	// format; when one is left it prices a missing listing.
	OldSize string
	// Menu is the word in an Untappd pour's label that prices this format
	// ("Growler"), or empty when Untappd never prices it.
	Menu string
	// Oz is how much the format holds, for pricing it by the ounce; 0
	// prices it like its nearest neighbour instead.
	Oz float64
	// For says which tap rows are sold this way.
	For func(Beer) bool
	// Keep hides a listing when its drink is off the menu, rather than
	// deleting it, and leaves its stock count to staff.
	Keep bool
}

const (
	beerCrowlerItem  = "Crowler To Go"
	sodaCrowlerItem  = "Craft Soda Crowler"
	growlerItem      = "Growler Fill"
	sixtelItem       = "Keg To Go"
	halfBarrelItem   = "Half Barrel To Go"
	crowlerVariation = "32oz Crowler"
)

var toGos = []toGo{
	{Item: beerCrowlerItem, What: "crowler", OldSize: crowlerVariation, Menu: "crowler", Oz: 32, For: isKeggable},
	{Item: sodaCrowlerItem, What: "crowler", OldSize: crowlerVariation, Menu: "crowler", Oz: 32, For: isTapSoda},
	{Item: growlerItem, What: "growler", OldSize: "64oz Growler", Menu: "growler", Oz: 64, For: isKeggable},
	{Item: sixtelItem, What: "sixtel", OldSize: "1/6 BBL Keg", For: isKeggable, Keep: true},
	{Item: halfBarrelItem, What: "half barrel", OldSize: "1/2 BBL Keg", For: isKeggable, Keep: true},
}

// isKeggable is a tap row sold by the keg: any beer, never a soda.
func isKeggable(b Beer) bool { return isTap(b) && !isSoda(b) }

func isTapSoda(b Beer) bool { return isTap(b) && isSoda(b) }

// isToGoPour is a menu pour rung from a to-go item, not the drink's own.
func isToGoPour(pr Pour) bool {
	for _, g := range toGos {
		if g.Menu != "" && strings.Contains(strings.ToLower(pr.Label), g.Menu) {
			return true
		}
	}
	return false
}

// menuPour is the menu's price for the format, when Untappd lists one.
func (g toGo) menuPour(b Beer) (int, bool) {
	if g.Menu == "" {
		return 0, false
	}
	for _, pr := range b.Pours {
		if strings.Contains(strings.ToLower(pr.Label), g.Menu) {
			if c, ok := pourCents(pr.Price); ok && pr.Price != "" {
				return c, true
			}
		}
	}
	return 0, false
}

// toGo lists every tap drink in each item it is sold from, then brings the
// listings of drinks off the menu in line. An item the workspace has not
// set up is skipped and said so.
func (p *planner) toGo() {
	for _, g := range toGos {
		list, ok := p.cat.item(g.Item)
		if !ok {
			if p.sellsAny(g) {
				p.plan.Notes = append(p.plan.Notes, "no "+g.Item+" item in Square, so its "+g.What+"s were not checked")
			}
			continue
		}
		for _, b := range p.rows {
			if !g.For(b) {
				continue
			}
			if it, ok := p.cat.match(b.Name); ok {
				p.list(g, b, it, list)
			}
		}
	}
	p.toGoStock()
}

func (p *planner) sellsAny(g toGo) bool { return slices.ContainsFunc(p.rows, g.For) }

// list makes sure one drink is in one to-go item, at the menu's price where
// the menu has one.
func (p *planner) list(g toGo, b Beer, it, list sqItem) {
	menu, priced := g.menuPour(b)
	if listed, ok := list.variation(it.Name); ok {
		if priced && listed.Cents != menu {
			p.askPrice(b.Name, list.Name, listed, menu, "the menu")
		}
		return
	}
	if priced {
		p.addListing(g, b, it, list, menu, "")
		return
	}
	if own, ok := it.variation(g.OldSize); ok && own.Cents >= 0 {
		p.addListing(g, b, it, list, own.Cents, "")
		return
	}
	p.priceListing(g, b, it, list)
}

// priceListing lists a drink priced from the drinks already listed.
func (p *planner) priceListing(g toGo, b Beer, it, list sqItem) {
	cents, how, ok := p.listingPrice(g, b, list)
	if !ok {
		p.plan.Notes = append(p.plan.Notes, fmt.Sprintf("%s: not in %s, and no drink like it is to price it from", b.Name, list.Name))
		return
	}
	if p.fresh[nameKey(b.Name)] {
		p.addListing(g, b, it, list, cents, how)
		return
	}
	p.plan.Questions = append(p.plan.Questions, sqQuestion{
		Key:   fmt.Sprintf("%s|%s|%s|%d", nameKey(g.What), list.ID, nameKey(it.Name), cents),
		Title: fmt.Sprintf("Sell a %s of %s at %s?", g.What, b.Name, dollars(cents)),
		Body: fmt.Sprintf("We sell a %s of every %s on tap, and **%s** is not in %s. %s is %s.",
			g.What, kindOf(b), b.Name, list.Name, dollars(cents), how),
		Options: []sqOption{
			{ID: "yes", Label: "Yes, add it at " + dollars(cents), Fix: &sqFix{Kind: "variation", ObjectID: list.ID, Name: it.Name, Cents: cents}},
			{ID: "no", Label: "No, I'll set it up in Square"},
		},
		Recommended: "yes",
	})
}

// listingPrice is what a missing listing should cost, and how that was
// worked out, said the way admins read it.
func (p *planner) listingPrice(g toGo, b Beer, list sqItem) (cents int, how string, ok bool) {
	listed := func(other sqItem) (int, bool) {
		v, has := list.variation(other.Name)
		return v.Cents, has && v.Cents > 0
	}
	if g.Oz > 0 {
		share, ok := p.ounceShare(g, b, listed)
		po, has := perOunce(b)
		if ok && has {
			cents = int(math.Round(po*g.Oz*share/100)) * 100
			return cents, fmt.Sprintf("%.0f%% of its draft price per ounce, as the other %ss in %s are", share*100, g.What, list.Name), true
		}
	}
	near, cents, _, ok := p.nearestPrice(b, listed)
	return cents, fmt.Sprintf("priced like **%s**, which pours at a similar price per ounce", near), ok
}

// ounceShare is the median, over the listed drinks of the row's kind, of
// what the format costs over what its ounces would cost at draft price.
func (p *planner) ounceShare(g toGo, b Beer, listed func(sqItem) (int, bool)) (float64, bool) {
	var shares []float64
	for _, other := range p.rows {
		if !g.For(other) || isSoda(other) != isSoda(b) || nameKey(other.Name) == nameKey(b.Name) {
			continue
		}
		it, found := p.cat.match(other.Name)
		if !found {
			continue
		}
		c, has := listed(it)
		po, priced := perOunce(other)
		if has && priced && po > 0 {
			shares = append(shares, float64(c)/(po*g.Oz))
		}
	}
	if len(shares) == 0 {
		return 0, false
	}
	slices.Sort(shares)
	mid := len(shares) / 2
	if len(shares)%2 == 0 {
		return (shares[mid-1] + shares[mid]) / 2, true
	}
	return shares[mid], true
}

func kindOf(b Beer) string {
	if isSoda(b) {
		return "soda"
	}
	return "beer"
}

// addListing adds the drink to the item, set up like its other listings,
// and counts it in. how, when set, says how the price was worked out.
func (p *planner) addListing(g toGo, b Beer, it, list sqItem, cents int, how string) {
	id := p.temp(strings.ReplaceAll(g.What, " ", "-"))
	summary := fmt.Sprintf("%s: added to %s at %s, in stock", b.Name, list.Name, dollars(cents))
	if how != "" {
		summary += " (" + strings.ReplaceAll(how, "**", "") + ")"
	}
	p.plan.Adds = append(p.plan.Adds, sqAdd{
		Summary: summary,
		Objects: []map[string]any{newVariation(list, id, list.ID, it.Name, cents)},
		Stock:   []string{id},
	})
}

// toGoStock brings each listing in line with the menu: on tap but counted
// out is counted back in, off tap is deleted; an item that keeps its
// listings hides and shows them instead. One guard covers every item, so a
// bar pouring no soda still loses its soda crowlers.
func (p *planner) toGoStock() {
	type held struct {
		g    toGo
		list sqItem
	}
	var lists []held
	keep := map[string]bool{}
	for _, g := range toGos {
		if list, ok := p.cat.item(g.Item); ok {
			lists = append(lists, held{g, list})
			p.onTapEntries(varEntries(list.Vars), g.For, "", keep)
		}
	}
	if p.nothingOnTap(keep, "to-go listing") {
		return
	}
	for _, h := range lists {
		if h.g.Keep {
			p.hideOffTap(h.list, keep)
			continue
		}
		p.restock(h.list, keep)
		p.delist(h.list.Name+" online", varEntries(h.list.Vars), keep, p.countOutLast(h.list.Name))
	}
}

// hideOffTap makes each listing off tap unsellable and each one back on tap
// sellable again, counted in. A listing on tap and already sellable is left
// alone, whatever its stock: sold out there is a staff decision.
func (p *planner) hideOffTap(list sqItem, keep map[string]bool) {
	for _, v := range list.Vars {
		switch on, shown := keep[v.ID], sellable(v); {
		case !on && shown:
			p.count(sqCount{ObjectID: v.ID, Sellable: boolPtr(false), Raw: v.Raw,
				Summary: fmt.Sprintf("%s: off the menu, so its %s is hidden", v.Name, list.Name)})
		case on && !shown:
			p.count(sqCount{ObjectID: v.ID, Sellable: boolPtr(true), Raw: v.Raw, Quantity: inStock,
				Summary: fmt.Sprintf("%s: back on the menu, so its %s is on sale again", v.Name, list.Name)})
		}
	}
}

// sellable reports a variation Square lets anyone ring or show; absent
// means yes.
func sellable(v sqVar) bool {
	s, ok := obj(v.Raw, "item_variation_data")["sellable"].(bool)
	return !ok || s
}

func boolPtr(b bool) *bool { return &b }
