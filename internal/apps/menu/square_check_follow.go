package menu

import "fmt"

// Square lists that follow the taps: the to-go items (crowlers, growlers,
// kegs) and the flight picker. Each holds one entry per drink, named as the
// drink's Square item, and the check treats them alike:
//
//   - every tap drink that can be sold that way gets an entry, added when
//     missing (toGo, for the to-go items; flight, whose price is the
//     taster's upcharge);
//   - the entries of drinks no longer on tap (or 86'd in Kit) are deleted,
//     or shown sold out for kegs, and put back when the drink returns.
//
// A flight option stays only while its drink's own item still sells a 4oz
// taster, so taking the taster off a drink takes it out of the picker too.

// sqEntry is one drink's entry in a list that follows the taps: a variation
// of an online item, or an option in the flight picker.
type sqEntry struct {
	ID   string
	Name string
}

func varEntries(vs []sqVar) []sqEntry {
	out := make([]sqEntry, len(vs))
	for i, v := range vs {
		out[i] = sqEntry{ID: v.ID, Name: v.Name}
	}
	return out
}

func modEntries(ms []sqMod) []sqEntry {
	out := make([]sqEntry, len(ms))
	for i, m := range ms {
		out[i] = sqEntry{ID: m.ID, Name: m.Name}
	}
	return out
}

// onTapEntries marks, in keep, the entries whose drink is on tap and whose
// item rings size, or every such drink when size is empty. tap says which
// menu rows the list is for. An entry answers to the row's own name, the row's name without its vintage, or the
// name of the item it rings as.
func (p *planner) onTapEntries(entries []sqEntry, tap func(Beer) bool, size string, keep map[string]bool) {
	names := map[string]bool{}
	for _, b := range p.rows {
		if !tap(b) {
			continue
		}
		it, ok := p.cat.match(b.Name)
		if !ok {
			continue
		}
		if v, ok := it.variation(size); size != "" && (!ok || v.Cents < 0) {
			continue
		}
		names[nameKey(it.Name)] = true
		for _, n := range menuNames(b.Name) {
			names[nameKey(n)] = true
		}
	}
	for _, e := range entries {
		if names[nameKey(e.Name)] {
			keep[e.ID] = true
		}
	}
}

// nothingOnTap reports a list none of whose entries matches a drink on tap,
// and notes it. That is a sync gone wrong far more often than an empty bar,
// so the caller leaves the list alone.
func (p *planner) nothingOnTap(keep map[string]bool, what string) bool {
	if len(keep) > 0 {
		return false
	}
	p.plan.Notes = append(p.plan.Notes, "no "+what+" matches a drink on tap, so none were taken off")
	return true
}

// delist deletes every entry of the list whose drink is off tap. Square
// keeps no item without a variation, so when every entry is off tap the
// last one stays and is handed to last, which says what happens to it
// instead.
func (p *planner) delist(listName string, entries []sqEntry, keep map[string]bool, last func(sqEntry)) {
	var gone []sqEntry
	for _, e := range entries {
		if !keep[e.ID] {
			gone = append(gone, e)
		}
	}
	for i, e := range gone {
		if i == len(gone)-1 && len(gone) == len(entries) {
			last(e)
			continue
		}
		p.count(sqCount{ObjectID: e.ID, Remove: true,
			Summary: fmt.Sprintf("%s: off the menu, so taken out of %s", e.Name, listName)})
	}
}

// keepLast is the last entry's fate for a list with no stock to count it
// out with: it stays, noted.
func (p *planner) keepLast(listName string) func(sqEntry) {
	return func(e sqEntry) {
		p.plan.Notes = append(p.plan.Notes, fmt.Sprintf("%s: off the menu but the last entry in %s, so kept", e.Name, listName))
	}
}
