package menu

import (
	"strings"
	"testing"
)

func kegList(vars ...map[string]any) sqItem { return rawItem("KTG", kegItem, vars...) }

func dipperWithKeg() sqItem {
	return rawItem("LD", "Little Dipper", rawVar("LD-16", "16oz", 700), rawVar("LD-4", "4oz", 250),
		rawVar("LD-C", "32oz Crowler", 1100), rawVar("LD-K", kegVariation, 13000))
}

func kegCatalog(items ...sqItem) sqCatalog {
	return sqCatalog{
		Items:  append([]sqItem{dipperWithKeg(), gravityCrowlers()}, items...),
		Flight: flightList(sqMod{ID: "M1", Name: "Little Dipper", Cents: 0}),
		Counts: map[string]string{"CTG-L": "99", "CTG-S": "99"},
	}
}

// A tap beer with a sixtel is listed online at its own price, with no stock
// count; a beer off the menu is taken off.
func TestPlanSquareKegListedAndDelisted(t *testing.T) {
	rows := []Beer{beer("Little Dipper", "7", "2.50")}
	cat := kegCatalog(kegList(rawVar("KTG-S", "Snickers Tsar", 14500), rawVar("KTG-M", "Mars Water", 13000)))
	p := planSquare(rows, cat, map[string]bool{})
	var add *sqAdd
	for i := range p.Adds {
		if strings.Contains(p.Adds[i].Summary, kegItem) {
			add = &p.Adds[i]
		}
	}
	if add == nil || !strings.Contains(add.Summary, "$130.00") || len(add.Stock) != 0 {
		t.Fatalf("want Little Dipper listed at $130 unstocked, got:\n%s", summaries(p))
	}
	// Nothing listed is on the menu yet, so nothing is taken off this pass.
	if got := changes(p); got["KTG-S"] != "" {
		t.Fatalf("no listing on the menu yet, want no keg deleted, got %v", got)
	}
}

func TestPlanSquareOffMenuKegDeleted(t *testing.T) {
	rows := []Beer{beer("Little Dipper", "7", "2.50")}
	cat := kegCatalog(kegList(rawVar("KTG-L", "Little Dipper", 13000), rawVar("KTG-S", "Snickers Tsar", 14500)))
	p := planSquare(rows, cat, map[string]bool{})
	got := changes(p)
	if got["KTG-S"] != "delete" || got["KTG-L"] != "" {
		t.Fatalf("want only Snickers Tsar keg deleted, got %v", got)
	}
	for _, a := range p.Adds {
		if strings.Contains(a.Summary, kegItem) {
			t.Errorf("already listed at the bar price, want no add, got %q", a.Summary)
		}
	}
}

// A keg priced apart from the bar is asked about, not changed.
func TestPlanSquareKegPriceMismatchAsks(t *testing.T) {
	rows := []Beer{beer("Little Dipper", "7", "2.50")}
	p := planSquare(rows, kegCatalog(kegList(rawVar("KTG-L", "Little Dipper", 11000))), map[string]bool{})
	var found bool
	for _, q := range p.Questions {
		if strings.HasPrefix(q.Key, "sixtel|KTG-L|11000|13000") && q.Options[0].Fix.Cents == 13000 {
			found = true
		}
	}
	if !found {
		t.Fatalf("want a sixtel price question, got %+v", p.Questions)
	}
}

// A beer without a sixtel at the bar, and a soda, are not listed.
func TestPlanSquareKegNeedsABarSixtel(t *testing.T) {
	rows := []Beer{beer("Little Dipper", "7", "2.50"), soda("Graviton Grapefruit Soda")}
	cat := kegCatalog(kegList(rawVar("KTG-L", "Little Dipper", 13000)), graviton())
	cat.Items[0] = littleDipper()
	p := planSquare(rows, cat, map[string]bool{})
	for _, a := range p.Adds {
		if strings.Contains(a.Summary, kegItem) {
			t.Errorf("want no keg listing, got %q", a.Summary)
		}
	}
	// The one listing has no sixtel behind it, so no keg is on the menu:
	// nothing is taken off.
	if got := changes(p); got["KTG-L"] != "" {
		t.Errorf("want the listing left, got %v", got)
	}
}
