package menu

import (
	"os"
	"strings"
	"testing"
)

// A real capture of board 22128. Kept as a fixture because this parser reads
// markup nobody promised us: when Untappd reskins the board, this test is
// where that shows up, rather than a half-empty wall display.
func loadFixture(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/untappd_board.html")
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	return string(raw)
}

func TestParseUntappdBoard(t *testing.T) {
	taps := ParseUntappdBoard(loadFixture(t))
	// 17 items on the board, one of which (Cerveza Espacial) Untappd prices
	// nowhere and is therefore dropped.
	if len(taps) != 16 {
		t.Fatalf("want 16 priced taps, got %d", len(taps))
	}

	byName := map[string]Tap{}
	var order []string
	for _, tp := range taps {
		byName[tp.Name] = tp
		order = append(order, tp.Section)
	}

	// Section order is taken from the page, not imposed here. Lagers held
	// only the unpriced beer, so it drops out with it.
	if got := order[0]; got != "Pub Ales" {
		t.Errorf("first section = %q, want Pub Ales", got)
	}

	for _, c := range []struct{ name, section, style, abv, price, size string }{
		{"Newtonian", "Pub Ales", "Amber/ESB", "5.7%", "6.50", "16oz"},
		{"Acceleration", "Pale Ales & IPAs", "IPA - Imperial", "9.8%", "8", "12oz"},
		{"Tsar Bomba", "Stouts & Porters", "Stout - Russian Imperial", "11%", "8", "9oz"},
	} {
		got, ok := byName[c.name]
		if !ok {
			t.Errorf("%s missing from the parse", c.name)
			continue
		}
		if got.Section != c.section || got.Style != c.style || got.ABV != c.abv ||
			got.Price != c.price || got.Size != c.size {
			t.Errorf("%s = %+v, want section=%q style=%q abv=%q price=%q size=%q",
				c.name, got, c.section, c.style, c.abv, c.price, c.size)
		}
	}
}

// Largest DRAFT pour wins the one price column. Untappd lists growlers too,
// and showing a growler price beside a beer name would say a pint costs $26.
func TestHeadlinePourPrefersLargestDraft(t *testing.T) {
	taps := ParseUntappdBoard(loadFixture(t))
	for _, tp := range taps {
		if strings.Contains(strings.ToLower(tp.Size), "growler") && tp.Price != "" {
			// Only acceptable when the beer has no draft price at all.
			t.Logf("%s falls back to %s", tp.Name, tp.Size)
		}
	}
	for _, tp := range taps {
		if tp.Name == "Asteroid Abbey" {
			// Has 9oz + 4oz draft and a 64oz growler; the 9oz must win.
			if tp.Price != "7.50" || tp.Size != "9oz" {
				t.Errorf("Asteroid Abbey = %s/%s, want 7.50/9oz", tp.Price, tp.Size)
			}
		}
	}
}

// A beer Untappd prices nowhere is left off the board rather than shown with
// a blank price column, which reads as a bug.
func TestUnpricedBeerIsDropped(t *testing.T) {
	for _, tp := range ParseUntappdBoard(loadFixture(t)) {
		if tp.Price == "" {
			t.Errorf("%s has no price and should have been dropped", tp.Name)
		}
		if tp.Name == "Cerveza Espacial" {
			t.Error("Cerveza Espacial is unpriced on Untappd and should not be on the board")
		}
	}
}

// The tripwire: markup that no longer parses must fail loudly, not quietly
// publish a two-tap board to a wall.
func TestImplausibleParseIsRejected(t *testing.T) {
	if got := ParseUntappdBoard("<html><body>reskinned</body></html>"); len(got) != 0 {
		t.Errorf("want nothing from unrecognised markup, got %d", len(got))
	}
}
