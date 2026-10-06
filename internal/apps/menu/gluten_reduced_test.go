package menu

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestApplyGlutenReducedMatchesCaseAndSpacingOnly(t *testing.T) {
	b := &Board{Taps: []Tap{
		{Section: "Lagers", Name: "Cerveza  Espacial", Price: "6.50"},
		{Section: "Lagers", Name: "Cerveza Espacial Lime", Price: "7"},
		{Section: "IPAs", Name: "Galactic Orbit", Price: "8"},
	}}
	applyGlutenReduced(b, []string{"cerveza espacial"})
	if !b.Taps[0].GlutenReduced {
		t.Error("a case and spacing difference should still match")
	}
	if b.Taps[1].GlutenReduced {
		t.Error("a longer name is a different beer; a dietary mark must not land on it")
	}
	if b.Taps[2].GlutenReduced {
		t.Error("a beer not on the list must not be marked")
	}
}

func TestRenderGlutenReducedBadgeAndFooterKey(t *testing.T) {
	b := &Board{
		Venue: Venue{Wordmark: "Gravity", Footer: []string{"Pours are 16oz unless marked"}},
		Taps: []Tap{
			{Section: "Lagers", Name: "Cerveza Espacial", Price: "6.50"},
			{Section: "IPAs", Name: "Galactic Orbit", Price: "8"},
		},
	}
	plain, err := Render(b, nil, "v")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain, `class="tap-gr"`) || strings.Contains(plain, GlutenReducedNote) {
		t.Error("with nothing marked there should be no badge and no footer key")
	}

	applyGlutenReduced(b, []string{"Cerveza Espacial"})
	marked, err := Render(b, nil, "v")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(marked, `Cerveza Espacial<span class="tap-gr">GR</span>`) {
		t.Error("the badge should sit inside the fitted name, right after it")
	}
	if strings.Count(marked, `<span class="tap-gr">GR</span>`) != 1 {
		t.Error("only the marked beer should wear the badge")
	}
	if !strings.Contains(marked, GlutenReducedNote) {
		t.Error("the footer must carry the caveat whenever a badge is showing")
	}
	if strings.Contains(strings.ToLower(marked), "gluten free") {
		t.Error("never say gluten free")
	}
}

func TestGlutenStampMovesWithTheList(t *testing.T) {
	if glutenStamp(nil) != "" {
		t.Error("nothing marked, no stamp, so an unused feature leaves the version alone")
	}
	a, b := glutenStamp([]string{"Wavelength"}), glutenStamp([]string{"Wavelength", "Newtonian"})
	if a == "" || a == b {
		t.Error("marking a beer must move the stamp, or the wall never shows it")
	}
	if glutenStamp([]string{"wavelength "}) != a {
		t.Error("a respelling that matches the same beers should not reload every screen")
	}

	row := &BoardRow{UpdatedAt: time.Unix(1, 0), Payload: []byte(`{}`)}
	if liveVersion(row, nil, nil, denver) == liveVersion(row, nil, []string{"Wavelength"}, denver) {
		t.Error("the live version should carry the gluten reduced stamp")
	}
}

func TestGlutenReducedArgs(t *testing.T) {
	current := []string{"Wavelength", "Newtonian"}

	args, err := parseGlutenReducedArgs([]byte(`{"add": "Cerveza Espacial, wavelength", "remove": ["NEWTONIAN"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := args.apply(current); !slices.Equal(got, []string{"Wavelength", "Cerveza Espacial"}) {
		t.Errorf("add and remove = %v", got)
	}

	args, err = parseGlutenReducedArgs([]byte(`{"beers": []}`))
	if err != nil {
		t.Fatal(err)
	}
	if args.Beers == nil {
		t.Fatal("an empty list must read as 'clear it', not as absent")
	}
	if got := args.apply(current); len(got) != 0 {
		t.Errorf("an empty beers list should clear, got %v", got)
	}

	args, err = parseGlutenReducedArgs([]byte(`{"add": ["Golden Mosaic"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := args.apply(current); len(got) != 3 {
		t.Errorf("add alone should keep the list, got %v", got)
	}
}

func TestPrintedMenuCarriesTheCaveatInTheRow(t *testing.T) {
	rows := []Beer{
		{Name: "Cerveza Espacial", Notes: "Crisp Mexican-style lager."},
		{Name: "Wavelength"},
		{Name: "Galactic Orbit", Notes: "West Coast IPA."},
	}
	markGlutenReducedRows(rows, []string{"Cerveza Espacial", "Wavelength"})
	if rows[0].Notes != GlutenReducedNote+" Crisp Mexican-style lager." {
		t.Errorf("described beer = %q", rows[0].Notes)
	}
	if rows[1].Notes != GlutenReducedNote {
		t.Errorf("a beer with no description still gets the line, got %q", rows[1].Notes)
	}
	if rows[2].Notes != "West Coast IPA." {
		t.Errorf("an unmarked beer must be untouched, got %q", rows[2].Notes)
	}
}
