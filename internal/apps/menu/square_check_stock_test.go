package menu

import (
	"strings"
	"testing"
)

func trackedVar(id, name string, cents int) map[string]any {
	v := rawVar(id, name, cents)
	obj(v, "item_variation_data")["track_inventory"] = true
	return v
}

func gravityCrowlers() sqItem {
	return rawItem("CTG", beerCrowlerItem,
		trackedVar("CTG-L", "Little Dipper", 1100), trackedVar("CTG-S", "Snickers Tsar", 1900))
}

func littleDipper() sqItem {
	return rawItem("LD", "Little Dipper", rawVar("LD-16", "16oz", 700), rawVar("LD-4", "4oz", 250),
		rawVar("LD-C", "32oz Crowler", 1100))
}

func counts(p sqPlan) map[string]string {
	out := map[string]string{}
	for _, c := range p.Counts {
		out[c.VariationID] = c.Quantity
	}
	return out
}

// A crowler whose drink left the menu goes sold out online; one still on
// the menu is left alone.
func TestPlanSquareOffMenuCrowlerSoldOut(t *testing.T) {
	rows := []Beer{beer("Little Dipper", "7", "2.50")}
	cat := sqCatalog{
		Items:  []sqItem{littleDipper(), gravityCrowlers()},
		Flight: flightList(sqMod{ID: "M1", Name: "Little Dipper", Cents: 0}),
		Counts: map[string]string{"CTG-L": "97", "CTG-S": "99"},
	}
	p := planSquare(rows, cat, map[string]bool{})
	got := counts(p)
	if len(got) != 1 || got["CTG-S"] != soldOut {
		t.Fatalf("want only Snickers Tsar counted to 0, got %v", p.Counts)
	}
	if !strings.Contains(p.Counts[0].Summary, "sold out") {
		t.Errorf("summary = %q", p.Counts[0].Summary)
	}
}

// A drink back on the menu is counted in again; one already sold out stays
// out without another write.
func TestPlanSquareCrowlerBackOnSale(t *testing.T) {
	rows := []Beer{beer("Little Dipper", "7", "2.50")}
	cat := sqCatalog{
		Items:  []sqItem{littleDipper(), gravityCrowlers()},
		Flight: flightList(sqMod{ID: "M1", Name: "Little Dipper", Cents: 0}),
		Counts: map[string]string{"CTG-S": "0"}, // Little Dipper never counted
	}
	got := counts(planSquare(rows, cat, map[string]bool{}))
	if len(got) != 1 || got["CTG-L"] != inStock {
		t.Fatalf("want only Little Dipper counted in, got %v", got)
	}
}

// Stock unread, or a menu that matches no crowler at all, changes nothing.
func TestPlanSquareCrowlerStockNeedsBothSides(t *testing.T) {
	cat := sqCatalog{Items: []sqItem{littleDipper(), gravityCrowlers()}}
	if p := planSquare([]Beer{beer("Little Dipper", "7", "2.50")}, cat, map[string]bool{}); len(p.Counts) != 0 {
		t.Errorf("counts unread, want no change, got %v", p.Counts)
	}
	cat.Counts = map[string]string{"CTG-L": "99", "CTG-S": "99"}
	p := planSquare([]Beer{beer("Somebody Else's IPA", "7", "2.50")}, cat, map[string]bool{})
	if len(p.Counts) != 0 {
		t.Errorf("no crowler on the menu, want no change, got %v", p.Counts)
	}
}
