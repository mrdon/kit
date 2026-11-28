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

// A new soda is created like the soda beside it, then gets its flight option
// and its crowler listing, priced like its neighbour's, with no crowler size
// on its own item.
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
	for _, want := range []string{"added to Craft Soda Crowler at $8.00, in stock", "added to Flight pours at +$0.00"} {
		if !strings.Contains(got, want) {
			t.Errorf("second pass missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, crowlerVariation) || len(second.Questions) != 0 {
		t.Errorf("want no crowler size and no questions, got:\n%s\n%+v", got, second.Questions)
	}

	// Third pass: listed and in the flight, so nothing is left to do.
	cat.Items[1] = rawItem("CSC", sodaCrowlerItem, rawVar("CSC-G", "Graviton Grapefruit Soda", 800),
		rawVar("CSC-GA", "Gaia Green Soda", 800))
	cat.Flight.Mods = append(cat.Flight.Mods, sqMod{ID: "M2", Name: "Gaia Green Soda", Cents: 0})
	third := planSquare(rows, cat, map[string]bool{"gaia green soda": true})
	if len(third.Adds) != 0 || len(third.Questions) != 0 {
		t.Errorf("want nothing to do, got:\n%s\n%+v", summaries(third), third.Questions)
	}
}

// A crowler listing is the crowler's price, at the bar and online: a 32oz
// Crowler size left on a drink's own item prices a missing listing, but is
// never compared with one that exists.
func TestPlanSquareCrowlerListingIsThePrice(t *testing.T) {
	rows := []Beer{beer("Newtonian", "6.50", "3"), beer("Cherry Bomba", "8", "4")}
	cat := sqCatalog{Items: []sqItem{
		rawItem("NEW", "Newtonian", rawVar("N16", "16oz", 650), rawVar("N4", "4oz", 300), rawVar("NP", "Pitcher", 2200),
			rawVar("NC", "32oz Crowler", 1100)),
		rawItem("CB", "Cherry Bomba", rawVar("C16", "16oz", 800), rawVar("C4", "4oz", 400), rawVar("CP", "Pitcher", 2600),
			rawVar("CC", "32oz Crowler", 1900)),
		rawItem("CTG", beerCrowlerItem, rawVar("CTG-N", "Newtonian", 1200)),
	}}
	plan := planSquare(rows, cat, map[string]bool{})
	if got := summaries(plan); got != "Cherry Bomba: added to Crowler To Go at $19.00, in stock" {
		t.Errorf("adds:\n%s", got)
	}
	if len(plan.Questions) != 0 {
		t.Errorf("compared a listing with an old crowler size: %+v", plan.Questions)
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

// On an item that already existed, a missing crowler might be deliberate, so
// even a price every other drink shares is asked, not copied.
func TestPlanSquareCrowlerOnOldItemAsks(t *testing.T) {
	rows := []Beer{soda("Graviton Grapefruit Soda"), soda("Regulus Root Beer"), soda("Gaia Green Soda")}
	cat := sqCatalog{Items: []sqItem{
		rawItem("GRAV", "Graviton Grapefruit Soda", rawVar("GRAV-R", "Regular", 450), rawVar("GRAV-4", "4oz", 300)),
		rawItem("REG", "Regulus Root Beer", rawVar("REG-R", "Regular", 450), rawVar("REG-4", "4oz", 300)),
		rawItem("GAIA", "Gaia Green Soda", rawVar("GAIA-R", "Regular", 450), rawVar("GAIA-4", "4oz", 300)),
		rawItem("CSC", sodaCrowlerItem, rawVar("CSC-G", "Graviton Grapefruit Soda", 800), rawVar("CSC-R", "Regulus Root Beer", 800)),
	}}
	plan := planSquare(rows, cat, map[string]bool{})
	if len(plan.Adds) != 0 {
		t.Fatalf("filled in a crowler on an existing item:\n%s", summaries(plan))
	}
	if len(plan.Questions) != 1 {
		t.Fatalf("want one crowler question, got %+v", plan.Questions)
	}
	fix := plan.Questions[0].Options[0].Fix
	if fix.Kind != "variation" || fix.ObjectID != "CSC" || fix.Name != "Gaia Green Soda" || fix.Cents != 800 {
		t.Errorf("want Gaia added to %s at $8.00, got %+v", sodaCrowlerItem, fix)
	}
}

// The house size is a pitcher of beer up to 8%, and a drink missing from the
// crowler item is offered in it. A size the menu leaves off, like a 16oz of
// a strong beer, is never asked about, however many other beers sell one.
func TestPlanSquareHouseSizesFollowTheRules(t *testing.T) {
	full := func(id, name string) sqItem {
		return rawItem(id, name, rawVar(id+"16", "16oz", 650), rawVar(id+"4", "4oz", 300),
			rawVar(id+"P", "Pitcher", 2200), rawVar(id+"C", "32oz Crowler", 1200), rawVar(id+"G", "64oz Growler", 1800))
	}
	strong := Beer{Section: "Stouts", Name: "Tsar Bomba", ABV: "11%", Pours: pours("9oz Draft", "8", "4oz Draft", "4")}
	mild := beer("Little Dipper", "6.50", "3")
	rows := []Beer{beer("Newtonian", "6.50", "3"), beer("Cerveza", "6.50", "3"), mild, strong}
	cat := sqCatalog{Items: []sqItem{
		full("NEW", "Newtonian"), full("CER", "Cerveza"),
		rawItem("LD", "Little Dipper", rawVar("LD16", "16oz", 650), rawVar("LD4", "4oz", 300)),
		rawItem("TB", "Tsar Bomba", rawVar("TB9", "9oz", 800), rawVar("TB4", "4oz", 400)),
		rawItem("CTG", beerCrowlerItem, rawVar("CTG-N", "Newtonian", 1200), rawVar("CTG-C", "Cerveza", 1200)),
	}}
	// A crowler is priced by the ounce: Tsar Bomba pours at $8 for 9oz, and
	// the listed crowlers charge 92% of their draft price per ounce.
	plan := planSquare(rows, cat, map[string]bool{})
	var titles []string
	for _, q := range plan.Questions {
		titles = append(titles, q.Title)
		if len(q.Options) != 2 || q.Recommended != "yes" {
			t.Errorf("not a yes/no card: %+v", q)
		}
	}
	got := strings.Join(titles, "\n")
	want := "Add a Pitcher of Little Dipper at $22.00?\n" +
		"Sell a crowler of Little Dipper at $12.00?\n" +
		"Sell a crowler of Tsar Bomba at $26.00?"
	if got != want {
		t.Errorf("questions:\n%s\nwant:\n%s", got, want)
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

// A beer over 8% pours 12oz at most, over 10% 9oz. A bigger glass the menu
// lists is on purpose and goes into Square; one only Square has is reported.
func TestPlanSquareGlassCapByABV(t *testing.T) {
	strong := Beer{Section: "Belgian", Name: "Mendacious", ABV: "8.1%", Pours: pours("16oz Draft", "8", "9oz Draft", "5", "4oz Draft", "3")}
	huge := Beer{Section: "Stouts", Name: "Tsar Bomba", ABV: "11%", Pours: pours("9oz Draft", "8", "4oz Draft", "4")}
	cat := sqCatalog{Items: []sqItem{
		rawItem("MEN", "Mendacious", rawVar("M9", "9oz", 500), rawVar("M4", "4oz", 300), rawVar("MC", "32oz Crowler", 1300)),
		rawItem("TB", "Tsar Bomba", rawVar("T16", "16oz", 1000), rawVar("T9", "9oz", 800), rawVar("T4", "4oz", 400),
			rawVar("TC", "32oz Crowler", 1900)),
	}}
	plan := planSquare([]Beer{strong, huge}, cat, map[string]bool{})
	if !strings.Contains(summaries(plan), "Mendacious: added 16oz at $8.00") {
		t.Fatalf("left out the menu's own pint:\n%s", summaries(plan))
	}
	var keys []string
	for _, q := range plan.Questions {
		if !q.Notice {
			t.Errorf("want only notices, got %+v", q)
		}
		keys = append(keys, q.Key)
	}
	if got := strings.Join(keys, ","); got != "glass|tsar bomba|16oz" {
		t.Errorf("notices = %s", got)
	}
}
