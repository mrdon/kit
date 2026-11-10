package menu

import (
	"errors"
	"slices"
	"strconv"
	"testing"
)

func overridesItems() []Beer {
	// A fresh slice per row, so a test editing one row's price edits only it.
	can := func() []Pour { return []Pour{{Size: "12oz", Label: "12oz Can", Price: "6.00"}} }
	return []Beer{
		{Section: "Non Alcoholic", Name: "Athletic Free Wave", Pours: can(), Show: ShowPrint},
		{Section: "Non Alcoholic", Name: "Athletic and Sierra Nevada N/A Beer", Pours: can(), Show: ShowBoard},
		{Section: "Sodas & Juices", Name: "Lemonade", Pours: can(), Show: ShowPrint},
		{Section: "Sodas & Juices", Name: "Kombucha", Pours: can()},
		{Section: "Non Alcoholic", Name: "Unpriced", Show: ShowBoard},
	}
}

func overridesBoard() *Board {
	return &Board{Taps: []Tap{
		{Section: "Lagers", Name: "Cerveza Espacial", Price: "6.50"},
		{Section: "Non Alcoholic", Name: "Zero-G Hop Water", Price: "5"},
		{Section: "Non Alcoholic", Name: "Regulus Root Beer", Price: "4.50"},
		{Section: "Specialty", Name: "Mars Water", Price: "5"},
	}}
}

func tapNamesOf(taps []Tap) []string {
	out := make([]string, 0, len(taps))
	for _, t := range taps {
		out = append(out, t.Name)
	}
	return out
}

func TestApplyBoardAddsWallItemsBesideTheirSection(t *testing.T) {
	b := overridesBoard()
	Overrides{Edits: Edits{Items: overridesItems()}}.applyBoard(b)
	want := []string{
		"Cerveza Espacial", "Zero-G Hop Water", "Regulus Root Beer",
		"Athletic and Sierra Nevada N/A Beer", "Mars Water", "Kombucha",
	}
	if got := tapNamesOf(b.Taps); !slices.Equal(got, want) {
		t.Errorf("taps = %v\nwant %v", got, want)
	}
	if got := b.Taps[3]; got.Price != "6.00" || got.SizeLabel() != "12 oz can" {
		t.Errorf("the can row reads %q / %q, want 6.00 / 12 oz can", got.Price, got.SizeLabel())
	}
}

func TestApplyBoardTakesTheEightySixOffItemsToo(t *testing.T) {
	b := overridesBoard()
	Overrides{
		Edits:     Edits{Items: overridesItems()},
		EightySix: []string{"athletic and sierra nevada n/a beer", "Mars Water"},
	}.applyBoard(b)
	want := []string{"Cerveza Espacial", "Zero-G Hop Water", "Regulus Root Beer", "Kombucha"}
	if got := tapNamesOf(b.Taps); !slices.Equal(got, want) {
		t.Errorf("taps = %v\nwant %v", got, want)
	}
}

func TestApplyRowsLeavesOffWallOnlyItems(t *testing.T) {
	rows := []Beer{{Section: "Non Alcoholic", Name: "Zero-G Hop Water"}}
	got := Overrides{Edits: Edits{Items: overridesItems()}, EightySix: []string{"Lemonade"}}.applyRows(rows)
	var names []string
	for _, r := range got {
		names = append(names, r.Name)
	}
	want := []string{"Zero-G Hop Water", "Athletic Free Wave", "Kombucha"}
	if !slices.Equal(names, want) {
		t.Errorf("rows = %v\nwant %v", names, want)
	}
}

func TestOverridesVersionMovesWithAWallItemOnly(t *testing.T) {
	base := Overrides{Edits: Edits{Items: overridesItems()}}.version("v")
	if base == "v" {
		t.Fatal("wall extras left the version alone")
	}
	priced := overridesItems()
	priced[1].Pours[0].Price = "7"
	if (Overrides{Edits: Edits{Items: priced}}).version("v") == base {
		t.Error("a new price on a wall row did not move the version")
	}
	printOnly := overridesItems()
	printOnly[2].Name = "Pink Lemonade"
	if (Overrides{Edits: Edits{Items: printOnly}}).version("v") != base {
		t.Error("a print-only row moved the wall's version")
	}
	if got := (Overrides{}).version("v"); got != "v" {
		t.Errorf("no overrides stamped %q, want v", got)
	}
}

func TestValidateItemsRejectsWhatWouldFailQuietly(t *testing.T) {
	if err := validateItems(overridesItems()); err != nil {
		t.Fatalf("valid extras rejected: %v", err)
	}
	for _, bad := range []Beer{
		{Section: "Sodas", Name: "Cola", Show: "wall"},
		{Section: "Sodas", Name: " "},
		{Name: "Cola"},
	} {
		if err := validateItems([]Beer{bad}); !errors.Is(err, ErrPayloadInvalid) {
			t.Errorf("validateItems(%+v) = %v, want ErrPayloadInvalid", bad, err)
		}
	}
}

func TestParseItemsArgsTakesAnArrayOrAString(t *testing.T) {
	row := `[{"section":"Non Alcoholic","name":"Athletic","pours":[{"size":"12oz","label":"12oz Can","price":"6"}],"show":"board"}]`
	for _, raw := range []string{
		`{"items":` + row + `}`,
		`{"items":` + strconv.Quote(row) + `}`,
	} {
		got, err := parseItemsArgs([]byte(raw))
		if err != nil || !got.Set || len(got.Items) != 1 || got.Items[0].Show != ShowBoard {
			t.Errorf("parse(%s) = %+v, %v", raw, got, err)
		}
	}
	if _, err := parseItemsArgs([]byte(`{"items":[{"name":"Cola","shows":"board"}]}`)); !errors.Is(err, ErrPayloadInvalid) {
		t.Errorf("a typo'd field got through: %v", err)
	}
	got, err := parseItemsArgs([]byte(`{"hide":"Tapping Soon, Lemonade"}`))
	if err != nil || got.Set || !slices.Equal(got.Hide, []string{"Tapping Soon", "Lemonade"}) {
		t.Errorf("hide-only parse = %+v, %v", got, err)
	}
	if got, err := parseItemsArgs(nil); err != nil || got.Set {
		t.Errorf("no arguments should read the edits, got %+v, %v", got, err)
	}
}

func TestHiddenSectionsAndBeersLeaveBothMenus(t *testing.T) {
	ov := Overrides{Edits: Edits{
		Items:  overridesItems(),
		Hidden: Hidden{Sections: []string{"sodas & juices"}, Items: []string{"Mars Water"}},
	}}
	b := overridesBoard()
	ov.applyBoard(b)
	want := []string{"Cerveza Espacial", "Zero-G Hop Water", "Regulus Root Beer", "Athletic and Sierra Nevada N/A Beer"}
	if got := tapNamesOf(b.Taps); !slices.Equal(got, want) {
		t.Errorf("wall = %v\nwant %v", got, want)
	}
	rows := ov.applyRows([]Beer{{Section: "Specialty", Name: "Mars Water"}, {Section: "Lagers", Name: "Cerveza Espacial"}})
	var names []string
	for _, r := range rows {
		names = append(names, r.Name)
	}
	if want := []string{"Cerveza Espacial", "Athletic Free Wave"}; !slices.Equal(names, want) {
		t.Errorf("paper = %v\nwant %v", names, want)
	}
	if ov.version("v") == (Overrides{Edits: Edits{Items: overridesItems()}}).version("v") {
		t.Error("hiding something did not move the wall's version")
	}
}

func TestMissingDescriptionsSkipsWhatWillNotPrint(t *testing.T) {
	state := &PrintState{
		Notes: map[string]string{"cerveza espacial": "A crisp lager."},
		Rows: []Beer{
			{Section: "Lagers", Name: "Cerveza Espacial"},
			{Section: "IPAs", Name: "Wicked Nebula"},
			{Section: "IPAs", Name: "Hidden IPA"},
			{Section: "IPAs", Name: "Gone IPA"},
		},
		Config: PrintConfig{Notes: map[string]string{"Newtonian": "Amber."}},
	}
	ov := Overrides{Edits: Edits{Hidden: Hidden{Items: []string{"Hidden IPA"}}}, EightySix: []string{"Gone IPA"}}
	if got := missingDescriptions(state, ov); !slices.Equal(got, []string{"Wicked Nebula"}) {
		t.Errorf("missing = %v, want only Wicked Nebula", got)
	}
}
