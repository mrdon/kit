package menu

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
	for _, b := range p.rows {
		if !isKeggable(b) {
			continue
		}
		it, ok := p.cat.match(b.Name)
		if !ok {
			continue
		}
		if own, ok := it.variation(kegVariation); ok && own.Cents >= 0 {
			p.listing(b, it, own, list, "sixtel", false)
		}
	}
	entries := varEntries(list.Vars)
	keep := map[string]bool{}
	p.onTapEntries(entries, isKeggable, kegVariation, keep)
	if p.nothingOnTap(keep, "keg listing") {
		return
	}
	p.delist(list.Name+" online", entries, keep, p.keepLast(list.Name))
}

// isKeggable is a tap row sold by the keg: any beer, never a soda.
func isKeggable(b Beer) bool { return isTap(b) && !isSoda(b) }
