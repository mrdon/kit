package menu

import (
	"errors"
	"slices"
	"strconv"
	"testing"
)

func overridesExtras() []Beer {
	// A fresh slice per row, so a test editing one row's price edits only it.
	can := func() []Pour { return []Pour{{Size: "12oz", Label: "12oz Can", Price: "6.00"}} }
	return []Beer{
		{Section: "Non Alcoholic", Name: "Athletic Free Wave", Pours: can()},
		{Section: "Non Alcoholic", Name: "Athletic and Sierra Nevada N/A Beer", Pours: can(), Show: ShowBoard},
		{Section: "Sodas & Juices", Name: "Lemonade", Pours: can(), Show: ShowPrint},
		{Section: "Sodas & Juices", Name: "Kombucha", Pours: can(), Show: ShowBoth},
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

func TestApplyBoardAddsWallExtrasBesideTheirSection(t *testing.T) {
	b := overridesBoard()
	Overrides{Extras: overridesExtras()}.applyBoard(b)
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

func TestApplyBoardTakesTheEightySixOffExtrasToo(t *testing.T) {
	b := overridesBoard()
	Overrides{
		Extras:    overridesExtras(),
		EightySix: []string{"athletic and sierra nevada n/a beer", "Mars Water"},
	}.applyBoard(b)
	want := []string{"Cerveza Espacial", "Zero-G Hop Water", "Regulus Root Beer", "Kombucha"}
	if got := tapNamesOf(b.Taps); !slices.Equal(got, want) {
		t.Errorf("taps = %v\nwant %v", got, want)
	}
}

func TestApplyRowsPrintsOnlyThePrintExtras(t *testing.T) {
	rows := []Beer{{Section: "Non Alcoholic", Name: "Zero-G Hop Water"}}
	got := Overrides{Extras: overridesExtras(), EightySix: []string{"Lemonade"}}.applyRows(rows)
	var names []string
	for _, r := range got {
		names = append(names, r.Name)
	}
	want := []string{"Zero-G Hop Water", "Athletic Free Wave", "Kombucha"}
	if !slices.Equal(names, want) {
		t.Errorf("rows = %v\nwant %v", names, want)
	}
}

func TestOverridesVersionMovesWithAWallExtraOnly(t *testing.T) {
	base := Overrides{Extras: overridesExtras()}.version("v")
	if base == "v" {
		t.Fatal("wall extras left the version alone")
	}
	priced := overridesExtras()
	priced[1].Pours[0].Price = "7"
	if (Overrides{Extras: priced}).version("v") == base {
		t.Error("a new price on a wall row did not move the version")
	}
	printOnly := overridesExtras()
	printOnly[2].Name = "Pink Lemonade"
	if (Overrides{Extras: printOnly}).version("v") != base {
		t.Error("a print-only row moved the wall's version")
	}
	if got := (Overrides{}).version("v"); got != "v" {
		t.Errorf("no overrides stamped %q, want v", got)
	}
}

func TestValidateExtrasRejectsAnUnknownShow(t *testing.T) {
	if err := validateExtras(overridesExtras()); err != nil {
		t.Fatalf("valid extras rejected: %v", err)
	}
	err := validateExtras([]Beer{{Name: "Cola", Show: "wall"}})
	if !errors.Is(err, ErrPayloadInvalid) {
		t.Errorf("err = %v, want ErrPayloadInvalid", err)
	}
}

func TestParseExtrasArgsTakesAnArrayOrAString(t *testing.T) {
	row := `[{"section":"Non Alcoholic","name":"Athletic","pours":[{"size":"12oz","label":"12oz Can","price":"6"}],"show":"board"}]`
	for _, raw := range []string{
		`{"extras":` + row + `}`,
		`{"extras":` + strconv.Quote(row) + `}`,
	} {
		got, err := parseExtrasArgs([]byte(raw))
		if err != nil || len(got) != 1 || got[0].Show != ShowBoard {
			t.Errorf("parse(%s) = %+v, %v", raw, got, err)
		}
	}
	if _, err := parseExtrasArgs([]byte(`{"extras":[{"name":"Cola","shows":"board"}]}`)); !errors.Is(err, ErrPayloadInvalid) {
		t.Errorf("a typo'd field got through: %v", err)
	}
	if _, err := parseExtrasArgs([]byte(`{}`)); !errors.Is(err, ErrPayloadInvalid) {
		t.Errorf("missing extras got through: %v", err)
	}
}
