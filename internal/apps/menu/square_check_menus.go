package menu

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// A drink on the menu needs a button, not just an item. Square puts an item
// on a register screen only through a menu category; one without any rings
// only by search, which on a busy night means it does not ring. Staff can
// take an item off a screen by hand (Pumpkin Little Dipper lost its button
// on 2026-11-07, in the same edit that marked it sold out), so every pass
// puts a menu drink back on the screens the drink it is set up like is on.
//
// Sold out is the other way a menu drink stops ringing. 86'd beers never
// reach the check (the 86 list drops them from the rows), so a menu drink
// marked sold out in Square is a mistake. Square's API ignores writes to an
// untracked size's sold-out flag, so Kit cannot clear it; it says so on a
// notice instead, naming the sizes.

// menuCategories are the item's categories that place it on a register
// screen.
func (c sqCatalog) menuCategories(it sqItem) []string {
	var out []string
	for _, ref := range objs(obj(it.Raw, "item_data"), "categories") {
		if id := str(ref, "id"); c.MenuCats[id] != "" {
			out = append(out, id)
		}
	}
	return out
}

// button puts a menu drink with no register button on the screens of the
// drink it is set up like. It must run before any other add to the item in
// the same pass: the upsert sends the item whole, with the sizes it has now.
func (p *planner) button(b Beer, it sqItem) {
	if p.cat.MenuCats == nil || len(p.cat.menuCategories(it)) > 0 {
		return
	}
	tpl, ok := p.template(b)
	if !ok {
		p.plan.Notes = append(p.plan.Notes, b.Name+": no button on the register, and no drink like it to copy the screen from")
		return
	}
	want := p.cat.menuCategories(tpl)
	if len(want) == 0 {
		return
	}
	o := writable(it.Raw)
	data := maps.Clone(obj(o, "item_data"))
	// Square fills description and description_plaintext from
	// description_html; sending description alone back wipes it.
	if _, ok := data["description_html"]; ok {
		delete(data, "description")
	}
	delete(data, "description_plaintext")
	cats, _ := data["categories"].([]any)
	cats = slices.Clone(cats)
	var screens []string
	for _, id := range want {
		cats = append(cats, map[string]any{"id": id})
		screens = append(screens, p.cat.MenuCats[id])
	}
	data["categories"] = cats
	o["item_data"] = data
	p.plan.Adds = append(p.plan.Adds, sqAdd{
		Summary: fmt.Sprintf("%s: had no register button, put back on %s like %s (at the end of the screen)",
			b.Name, strings.Join(screens, ", "), tpl.Name),
		Objects: []map[string]any{o},
	})
}

// soldOut tells the admins about a menu drink's sizes marked sold out at a
// location, which Kit cannot undo.
func (p *planner) soldOut(b Beer, it sqItem) {
	var sizes, versions []string
	for _, pr := range pricedPours(b) {
		v, ok := pourVariation(b, it, pr)
		if !ok || !markedSoldOut(v) {
			continue
		}
		sizes = append(sizes, v.Name)
		ver, _ := v.Raw["version"].(float64)
		versions = append(versions, v.ID+"@"+strconv.FormatFloat(ver, 'f', 0, 64))
	}
	if len(sizes) == 0 {
		return
	}
	p.plan.Questions = append(p.plan.Questions, sqQuestion{
		// The versions change each time a size is marked sold out again, so
		// a repeat is told again.
		Key:   "soldout|" + it.ID + "|" + strings.Join(versions, ","),
		Title: b.Name + " is on the menu but sold out in Square",
		Body: fmt.Sprintf("The menu lists **%s**, but Square has **%s** marked sold out, so the register greys it out. "+
			"Square does not let Kit change that: open the item in the Square dashboard, set those sizes to Available, and save. "+
			"If it is really out, 86 it in Kit instead and it comes off the menu.",
			b.Name, strings.Join(sizes, ", ")),
		Notice: true,
	})
}

func markedSoldOut(v sqVar) bool {
	for _, lo := range objs(obj(v.Raw, "item_variation_data"), "location_overrides") {
		if out, _ := lo["sold_out"].(bool); out {
			return true
		}
	}
	return false
}
