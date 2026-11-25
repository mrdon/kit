package menu

import "testing"

func flightCatalog(mods ...sqMod) sqCatalog {
	return sqCatalog{Items: []sqItem{littleDipper()}, Flight: flightList(mods...)}
}

// A flight option whose drink is off the menu is taken out; the tap
// drink's own option stays.
func TestPlanSquareOffMenuFlightOptionRemoved(t *testing.T) {
	rows := []Beer{beer("Little Dipper", "7", "2.50")}
	cat := flightCatalog(sqMod{ID: "M-LD", Name: "Little Dipper"}, sqMod{ID: "M-OK", Name: "Oktoberfest"})
	got := changes(planSquare(rows, cat, map[string]bool{}))
	if got["M-OK"] != "delete" || got["M-LD"] != "" {
		t.Fatalf("want only Oktoberfest taken out of the flight, got %v", got)
	}
}

// A beer whose prices were cleared is no longer pouring, so its option goes.
// So does one whose item no longer rings a taster.
func TestPlanSquareFlightOptionOfClearedBeerRemoved(t *testing.T) {
	rows := []Beer{beer("Little Dipper", "7", "2.50"), {Section: "Ales", Name: "Coal Kriek", ABV: "6%"}}
	cat := flightCatalog(sqMod{ID: "M-LD", Name: "Little Dipper"}, sqMod{ID: "M-CK", Name: "Coal Kriek"})
	if got := changes(planSquare(rows, cat, map[string]bool{})); got["M-CK"] != "delete" {
		t.Fatalf("want Coal Kriek taken out of the flight, got %v", got)
	}
}

// A vintage on the menu keeps the option Square names without the year.
func TestPlanSquareFlightOptionKeptAcrossVintage(t *testing.T) {
	rows := []Beer{beer("Little Dipper", "7", "2.50"), beer("2026 Tsar Bomba", "8", "4")}
	cat := flightCatalog(sqMod{ID: "M-LD", Name: "Little Dipper"}, sqMod{ID: "M-TB", Name: "Tsar Bomba", Cents: 150})
	cat.Items = append(cat.Items, rawItem("TB", "Tsar Bomba", rawVar("TB-16", "16oz", 800), rawVar("TB-4", "4oz", 400)))
	if got := changes(planSquare(rows, cat, map[string]bool{})); got["M-TB"] != "" {
		t.Fatalf("want Tsar Bomba kept, got %v", got)
	}
}

// No option matching anything on tap reads as a bad sync: nothing is removed.
func TestPlanSquareFlightNothingOnTapRemovesNothing(t *testing.T) {
	rows := []Beer{beer("Little Dipper", "7", "2.50")}
	cat := flightCatalog(sqMod{ID: "M-OK", Name: "Oktoberfest"}, sqMod{ID: "M-CK", Name: "Coal Kriek"})
	p := planSquare(rows, cat, map[string]bool{})
	if got := changes(p); len(got) != 0 {
		t.Fatalf("want no flight option removed, got %v", got)
	}
}
