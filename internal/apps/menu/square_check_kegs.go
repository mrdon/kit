package menu

import "fmt"

// Online kegs follow the crowler rule: any beer on tap can be kegged, so a
// tap beer whose own item rings a sixtel is listed in the online keg item at
// that price, and comes off it when the beer leaves the menu. Kegs are filled
// to order with notice, so unlike crowlers they carry no stock count: a
// listing is either there or deleted.

const (
	kegItem      = "Keg To Go"
	kegVariation = "1/6 BBL Keg"
)

// kegs lists every tap beer's sixtel online and takes off the rest. A
// workspace without the keg item skips it.
func (p *planner) kegs() {
	list, ok := p.cat.item(kegItem)
	if !ok {
		return
	}
	onMenu := map[string]bool{}
	for _, b := range p.rows {
		if !isTap(b) || isSoda(b) {
			continue
		}
		it, ok := p.cat.match(b.Name)
		if !ok {
			continue
		}
		own, ok := it.variation(kegVariation)
		if !ok || own.Cents < 0 {
			continue
		}
		if listed, ok := list.variation(it.Name); ok {
			onMenu[listed.ID] = true
		}
		p.listing(b, it, own, list, "sixtel", false)
	}
	if len(onMenu) == 0 {
		// As with crowlers: no keg on the menu is a sync gone wrong far
		// more often than nothing on tap.
		p.plan.Notes = append(p.plan.Notes, "no keg on the menu matches Square, so none were taken off the online shop")
		return
	}
	p.kegStock(list, onMenu)
}

// kegStock deletes the keg listings of beers off the menu. Square keeps no
// item without a variation, and an untracked listing cannot be counted out,
// so the last one stays and is noted.
func (p *planner) kegStock(list sqItem, onMenu map[string]bool) {
	var gone []sqVar
	for _, v := range list.Vars {
		if !onMenu[v.ID] {
			gone = append(gone, v)
		}
	}
	for i, v := range gone {
		if i == len(gone)-1 && len(gone) == len(list.Vars) {
			p.plan.Notes = append(p.plan.Notes, fmt.Sprintf("%s: off the menu but the last listing in %s, so kept", v.Name, list.Name))
			continue
		}
		p.count(sqCount{VariationID: v.ID, Remove: true,
			Summary: fmt.Sprintf("%s: off the menu, so taken out of %s online", v.Name, list.Name)})
	}
}
