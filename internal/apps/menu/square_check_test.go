package menu

import (
	"strings"
	"testing"
)

// fixture builders: Square objects shaped as the catalog API sends them.

func rawVar(id, name string, cents int) map[string]any {
	return map[string]any{
		"id": id, "type": "ITEM_VARIATION", "present_at_all_locations": true,
		"item_variation_data": map[string]any{
			"name": name, "ordinal": float64(0), "track_inventory": false,
			"price_money": map[string]any{"amount": float64(cents), "currency": "USD"},
			"channels":    []any{"CH_POS"},
		},
	}
}

func rawItem(id, name string, vars ...map[string]any) sqItem {
	list := make([]any, len(vars))
	for i, v := range vars {
		list[i] = v
	}
	return parseSqItem(map[string]any{
		"id": id, "type": "ITEM", "present_at_all_locations": true,
		"item_data": map[string]any{
			"name": name, "tax_ids": []any{"TAX"}, "categories": []any{map[string]any{"id": "CAT"}},
			"variations": list,
		},
	})
}

func pours(ps ...string) []Pour {
	var out []Pour
	for i := 0; i+1 < len(ps); i += 2 {
		label := ps[i]
		out = append(out, Pour{Size: strings.Fields(label)[0], Label: label, Price: ps[i+1]})
	}
	return out
}

func soda(name string) Beer {
	return Beer{Section: "Non Alcoholic", Name: name, Pours: pours("12oz Draft", "4.50", "4oz Draft", "3")}
}

func beer(name, pint, taster string) Beer {
	return Beer{Section: "Ales", Name: name, ABV: "5%", Pours: pours("16oz Draft", pint, "4oz Draft", taster)}
}

func graviton() sqItem {
	return rawItem("GRAV", "Graviton Grapefruit Soda",
		rawVar("GRAV-R", "Regular", 450), rawVar("GRAV-4", "4oz", 300), rawVar("GRAV-C", "32oz Crowler", 800))
}

func flightList(mods ...sqMod) *sqModList { return &sqModList{ID: "FLIGHT", Mods: mods} }

func summaries(p sqPlan) string {
	var s []string
	for _, a := range p.Adds {
		s = append(s, a.Summary)
	}
	return strings.Join(s, "\n")
}

// A new soda is created like the soda beside it, then gets its crowler size,
// its flight option and its online crowler listing as the passes see it.
func TestPlanSquareNewSodaFollowsItsNeighbour(t *testing.T) {
	rows := []Beer{soda("Graviton Grapefruit Soda"), soda("Gaia Green Soda")}
	crowlers := rawItem("CSC", sodaCrowlerItem, rawVar("CSC-G", "Graviton Grapefruit Soda", 800))
	cat := sqCatalog{
		Items:  []sqItem{graviton(), crowlers},
		Flight: flightList(sqMod{ID: "M1", Name: "Graviton Grapefruit Soda", Cents: 0}),
	}

	first := planSquare(rows, cat, map[string]bool{})
	if len(first.Adds) != 1 || first.Adds[0].Fresh != "Gaia Green Soda" {
		t.Fatalf("want one create for Gaia, got:\n%s", summaries(first))
	}
	item := first.Adds[0].Objects[0]
	data := obj(item, "item_data")
	if data["abbreviation"] != "Ga" || data["tax_ids"] == nil || data["categories"] == nil {
		t.Errorf("new item did not copy its set up: %v", data)
	}
	var names []string
	for _, v := range data["variations"].([]any) {
		names = append(names, str(obj(v.(map[string]any), "item_variation_data"), "name"))
	}
	if strings.Join(names, ",") != "Regular,4oz" {
		t.Errorf("sizes = %v, want the menu's pours as Graviton names them", names)
	}

	// Second pass: Gaia exists with its two menu sizes.
	gaia := rawItem("GAIA", "Gaia Green Soda", rawVar("GAIA-R", "Regular", 450), rawVar("GAIA-4", "4oz", 300))
	cat.Items = append(cat.Items, gaia)
	second := planSquare(rows, cat, map[string]bool{"gaia green soda": true})
	got := summaries(second)
	for _, want := range []string{"added 32oz Crowler at $8.00", "added to Flight pours at +$0.00"} {
		if !strings.Contains(got, want) {
			t.Errorf("second pass missing %q:\n%s", want, got)
		}
	}

	// Third pass: the crowler size exists, so it is listed online and counted in.
	gaia = rawItem("GAIA", "Gaia Green Soda", rawVar("GAIA-R", "Regular", 450), rawVar("GAIA-4", "4oz", 300),
		rawVar("GAIA-C", "32oz Crowler", 800))
	cat.Items[len(cat.Items)-1] = gaia
	cat.Flight.Mods = append(cat.Flight.Mods, sqMod{ID: "M2", Name: "Gaia Green Soda", Cents: 0})
	third := planSquare(rows, cat, map[string]bool{"gaia green soda": true})
	if len(third.Adds) != 1 || len(third.Adds[0].Stock) != 1 || !strings.Contains(third.Adds[0].Summary, sodaCrowlerItem) {
		t.Fatalf("want the online crowler listing, stocked, got:\n%s", summaries(third))
	}
	if len(third.Questions) != 0 {
		t.Errorf("nothing to ask about, got %v", third.Questions)
	}
}

// A price that disagrees is a question, never a write.
func TestPlanSquarePriceMismatchAsks(t *testing.T) {
	rows := []Beer{beer("Little Dipper", "6.50", "4")}
	cat := sqCatalog{Items: []sqItem{rawItem("LD", "Little Dipper", rawVar("LD-16", "16oz", 650), rawVar("LD-4", "4oz", 300))}}
	plan := planSquare(rows, cat, map[string]bool{})
	if len(plan.Adds) != 0 {
		t.Fatalf("repriced without asking:\n%s", summaries(plan))
	}
	if len(plan.Questions) != 1 || plan.Questions[0].Options[0].Fix.Cents != 400 {
		t.Fatalf("want one price question offering $4.00, got %+v", plan.Questions)
	}
}

// A name a letter or a bracket away from an item is a rename, not a new item.
func TestPlanSquareNearNameAsks(t *testing.T) {
	rows := []Beer{beer("Newtonian", "6.50", "3"), beer("Mr. Radar (Nitro)", "7", "4")}
	cat := sqCatalog{Items: []sqItem{
		rawItem("NEW", "Newtonian", rawVar("N16", "16oz", 650), rawVar("N4", "4oz", 300)),
		rawItem("MR", "Mr Radar", rawVar("R16", "16oz", 700), rawVar("R4", "4oz", 400)),
	}}
	plan := planSquare(rows, cat, map[string]bool{})
	for _, a := range plan.Adds {
		if a.Fresh != "" {
			t.Fatalf("created a near-miss as a new item: %s", a.Summary)
		}
	}
	if len(plan.Questions) != 1 || !strings.HasPrefix(plan.Questions[0].Key, "rename|") {
		t.Fatalf("want a rename question, got %+v", plan.Questions)
	}
}

// An archived item is asked about, never created a second time.
func TestPlanSquareArchivedAsks(t *testing.T) {
	old := rawItem("OLD", "Pumpkin Little Dipper", rawVar("P16", "16oz", 750), rawVar("P4", "4oz", 300))
	old.Archived = true
	rows := []Beer{beer("Newtonian", "6.50", "3"), beer("Pumpkin Little Dipper", "7.50", "3")}
	cat := sqCatalog{Items: []sqItem{rawItem("NEW", "Newtonian", rawVar("N16", "16oz", 650), rawVar("N4", "4oz", 300)), old}}
	plan := planSquare(rows, cat, map[string]bool{})
	if len(plan.Adds) != 0 {
		t.Fatalf("acted on an archived drink:\n%s", summaries(plan))
	}
	if len(plan.Questions) != 1 || !strings.HasPrefix(plan.Questions[0].Key, "archived|") {
		t.Fatalf("want an archived question, got %+v", plan.Questions)
	}
}

// On an item that already existed, a missing size might be deliberate, so
// even a price every other drink shares is asked, not copied.
func TestPlanSquareHouseSizeOnOldItemAsks(t *testing.T) {
	rows := []Beer{soda("Graviton Grapefruit Soda"), soda("Regulus Root Beer"), soda("Gaia Green Soda")}
	cat := sqCatalog{Items: []sqItem{
		graviton(),
		rawItem("REG", "Regulus Root Beer", rawVar("REG-R", "Regular", 450), rawVar("REG-4", "4oz", 300), rawVar("REG-C", "32oz Crowler", 800)),
		rawItem("GAIA", "Gaia Green Soda", rawVar("GAIA-R", "Regular", 450), rawVar("GAIA-4", "4oz", 300)),
	}}
	plan := planSquare(rows, cat, map[string]bool{})
	if strings.Contains(summaries(plan), "Crowler") {
		t.Fatalf("filled in a size on an existing item:\n%s", summaries(plan))
	}
	if len(plan.Questions) != 1 || plan.Questions[0].Recommended != "c800" {
		t.Fatalf("want one size question recommending $8.00, got %+v", plan.Questions)
	}
}

// A can or juice box Kit lists is checked but never created.
func TestPlanSquareMissingCanAsks(t *testing.T) {
	rows := []Beer{{Section: "Sodas & Juices", Name: "Lemonade", Pours: pours("12oz Glass", "3.50")}}
	plan := planSquare(rows, sqCatalog{}, map[string]bool{})
	if len(plan.Adds) != 0 || len(plan.Questions) != 1 {
		t.Fatalf("want one question and no adds, got adds:\n%s\nquestions %+v", summaries(plan), plan.Questions)
	}
}

func TestAbbreviation(t *testing.T) {
	for in, want := range map[string]string{"Gaia Green Soda": "Ga", "Zero-G Hop Water": "Ze", "1/2 Pint": "12"} {
		if got := abbreviation(in); got != want {
			t.Errorf("abbreviation(%q) = %q, want %q", in, got, want)
		}
	}
}

// Untappd puts the vintage year in front of a name Square keeps without it,
// and Square's kitchen name may hold the long form of a short item name. Both
// are the same item, so nothing is created and nothing is asked.
func TestPlanSquareVintageYearAndKitchenNameMatch(t *testing.T) {
	ba := rawItem("BA", "BA Tsar Bomba", rawVar("BA-16", "16oz", 900), rawVar("BA-4", "4oz", 400))
	ba.KitchenName = "Barrel Aged Tsar Bomba"
	cat := sqCatalog{Items: []sqItem{
		rawItem("TB", "Tsar Bomba", rawVar("TB-16", "16oz", 800), rawVar("TB-4", "4oz", 400)),
		ba,
	}}
	for name, want := range map[string]string{"2026 Tsar Bomba": "TB", "2026 Barrel Aged Tsar Bomba": "BA"} {
		if it, ok := cat.match(name); !ok || it.ID != want {
			t.Errorf("match(%q) = %q, %v; want %q", name, it.ID, ok, want)
		}
	}
	rows := []Beer{beer("2026 Tsar Bomba", "8", "4"), beer("2026 Barrel Aged Tsar Bomba", "9", "4")}
	plan := planSquare(rows, cat, map[string]bool{})
	if len(plan.Adds) != 0 || len(plan.Questions) != 0 {
		t.Fatalf("want no adds or questions, got adds:\n%s\nquestions %+v", summaries(plan), plan.Questions)
	}
}

func TestMenuNamesStripsOnlyALeadingYear(t *testing.T) {
	for in, want := range map[string]string{
		"2026 Tsar Bomba":  "2026 Tsar Bomba|Tsar Bomba",
		"1999 Ale":         "1999 Ale|Ale",
		"Tsar Bomba 2026":  "Tsar Bomba 2026",
		"2026":             "2026",
		"3026 Odyssey":     "3026 Odyssey",
		"20260 Tsar Bomba": "20260 Tsar Bomba",
	} {
		if got := strings.Join(menuNames(in), "|"); got != want {
			t.Errorf("menuNames(%q) = %q, want %q", in, got, want)
		}
	}
}

// A vintage year stays on the menu but never goes into Square: a new item is
// created without it, and a rename card suggests the kitchen name without it.
func TestPlanSquareVintageYearStaysOutOfSquare(t *testing.T) {
	rows := []Beer{soda("Graviton Grapefruit Soda"), soda("2026 Gaia Green Soda")}
	plan := planSquare(rows, sqCatalog{Items: []sqItem{graviton()}}, map[string]bool{})
	if len(plan.Adds) != 1 || plan.Adds[0].Fresh != "2026 Gaia Green Soda" {
		t.Fatalf("want one create for Gaia, got:\n%s", summaries(plan))
	}
	data := obj(plan.Adds[0].Objects[0], "item_data")
	if data["name"] != "Gaia Green Soda" || !strings.Contains(plan.Adds[0].Summary, "as Gaia Green Soda") {
		t.Fatalf("created %v (%s), want it named without the year", data["name"], plan.Adds[0].Summary)
	}

	rows = []Beer{beer("2026 Mr. Radar (Nitro)", "7", "4")}
	cat := sqCatalog{Items: []sqItem{rawItem("MR", "Mr Radar", rawVar("R16", "16oz", 700), rawVar("R4", "4oz", 400))}}
	plan = planSquare(rows, cat, map[string]bool{})
	if len(plan.Questions) != 1 || !strings.HasPrefix(plan.Questions[0].Key, "rename|") {
		t.Fatalf("want a rename question, got %+v", plan.Questions)
	}
	if body := plan.Questions[0].Body; !strings.Contains(body, `kitchen name to "Mr. Radar (Nitro)"`) {
		t.Fatalf("want the kitchen name suggested without the year, got %q", body)
	}
}
