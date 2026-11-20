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

func changes(p sqPlan) map[string]string {
	out := map[string]string{}
	for _, c := range p.Counts {
		if c.Remove {
			out[c.VariationID] = "delete"
		} else {
			out[c.VariationID] = c.Quantity
		}
	}
	return out
}

func dipperCatalog(counts map[string]string) sqCatalog {
	return sqCatalog{
		Items:  []sqItem{littleDipper(), gravityCrowlers()},
		Flight: flightList(sqMod{ID: "M1", Name: "Little Dipper", Cents: 0}),
		Counts: counts,
	}
}

// A crowler whose drink left the menu is taken off the shop; one still on
// the menu is left alone.
func TestPlanSquareOffMenuCrowlerDeleted(t *testing.T) {
	rows := []Beer{beer("Little Dipper", "7", "2.50")}
	p := planSquare(rows, dipperCatalog(map[string]string{"CTG-L": "97", "CTG-S": "0"}), map[string]bool{})
	got := changes(p)
	if len(got) != 1 || got["CTG-S"] != "delete" {
		t.Fatalf("want only Snickers Tsar deleted, got %v", got)
	}
	if !strings.Contains(p.Counts[0].Summary, "taken out of") {
		t.Errorf("summary = %q", p.Counts[0].Summary)
	}
}

// A drink on the menu whose listing was counted out is counted back in.
func TestPlanSquareCrowlerBackOnSale(t *testing.T) {
	rows := []Beer{beer("Little Dipper", "7", "2.50"), beer("Snickers Tsar", "8", "3")}
	snickers := rawItem("ST", "Snickers Tsar", rawVar("ST-16", "16oz", 800), rawVar("ST-4", "4oz", 300),
		rawVar("ST-C", "32oz Crowler", 1900))
	cat := dipperCatalog(map[string]string{"CTG-S": "0"}) // Little Dipper never counted
	cat.Items = append(cat.Items, snickers)
	cat.Flight.Mods = append(cat.Flight.Mods, sqMod{ID: "M2", Name: "Snickers Tsar", Cents: 0})
	got := changes(planSquare(rows, cat, map[string]bool{}))
	if len(got) != 2 || got["CTG-L"] != inStock || got["CTG-S"] != inStock {
		t.Fatalf("want both counted in, got %v", got)
	}
}

// Square keeps no item without a variation, so the last listing is only
// counted out.
func TestPlanSquareLastCrowlerKept(t *testing.T) {
	rows := []Beer{beer("Little Dipper", "7", "2.50")}
	cat := dipperCatalog(map[string]string{"CTG-L": "99", "CTG-S": "99", "CSC-G": "99"})
	cat.Items = append(cat.Items, rawItem("CSC", sodaCrowlerItem, trackedVar("CSC-G", "Graviton Grapefruit Soda", 800)))
	got := changes(planSquare(rows, cat, map[string]bool{}))
	if got["CSC-G"] != soldOut || got["CTG-S"] != "delete" {
		t.Fatalf("want Graviton counted out and Snickers deleted, got %v", got)
	}
}

// A menu that matches no crowler at all changes nothing.
func TestPlanSquareCrowlerStockNeedsAMenu(t *testing.T) {
	cat := dipperCatalog(map[string]string{"CTG-L": "99", "CTG-S": "99"})
	p := planSquare([]Beer{beer("Somebody Else's IPA", "7", "2.50")}, cat, map[string]bool{})
	if len(p.Counts) != 0 {
		t.Errorf("no crowler on the menu, want no change, got %v", p.Counts)
	}
}
