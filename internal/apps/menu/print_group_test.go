package menu

import (
	"strings"
	"testing"
)

const tsarNote = "Layers of roasted grains and the sweetness of molasses and brown sugar. This beer is huge!"

func ninePour(price string) []Pour {
	return []Pour{{Size: "9oz", Label: "9oz Draft", Price: price}}
}

// The case the rule was written for, as Untappd lists it: four vintages of one
// stout sharing a description, one of them a different style.
func tsarRows() []Beer {
	return []Beer{
		{Name: "2026 Tsar Bomba", Style: "Stout - Russian Imperial", ABV: "11%", Notes: tsarNote, Pours: ninePour("8"), GlutenReduced: true},
		{Name: "2026 Barrel Aged Tsar Bomba", Style: "Stout - Russian Imperial", ABV: "12%", Notes: tsarNote, Pours: ninePour("9")},
		{Name: "2025 Tsar Bomba", Style: "Stout - Russian Imperial", ABV: "11%", Notes: tsarNote, Pours: ninePour("8")},
		{Name: "Tsar Bomba W/ Snickers", Style: "Stout - Imperial", ABV: "11%", Notes: tsarNote, Pours: ninePour("8")},
	}
}

func TestSharedDescriptionsGroupUnderTheSharedName(t *testing.T) {
	rows := append(tsarRows(), Beer{Name: "Cherry Bomba", Notes: "Blend of cherry and stout.", Pours: ninePour("8")})
	units := sectionUnits(PrintSection{Name: "Stouts & Porters", Rows: rows})
	if len(units) != 2 || units[0].Group == nil || units[1].Beer == nil {
		t.Fatalf("got %d units; want the four Tsars as one group and Cherry Bomba alone", len(units))
	}
	g := units[0].Group
	if g.Name != "Tsar Bomba" {
		t.Errorf("group name = %q, want Tsar Bomba", g.Name)
	}
	if g.Style != "Russian Imperial Stout" || g.Size != "9oz" || g.Notes != tsarNote {
		t.Errorf("group shared = %q / %q / %q", g.Style, g.Size, g.Notes)
	}
	want := []struct{ label, style string }{
		{"2026", ""}, {"2026 Barrel Aged", ""}, {"2025", ""}, {"W/ Snickers", "Imperial Stout"},
	}
	for i, w := range want {
		v := g.Rows[i]
		if v.Label != w.label || v.Style != w.style {
			t.Errorf("variant %d = %q (%q), want %q (%q)", i, v.Label, v.Style, w.label, w.style)
		}
	}
	if !g.Rows[0].Beer.GlutenReduced || g.Rows[1].Beer.GlutenReduced {
		t.Error("each variant must keep its own GR mark")
	}
}

// Different descriptions never group, however alike the names: a brewery that
// writes each vintage up gets ordinary rows with no setting to change.
func TestDistinctDescriptionsStayRows(t *testing.T) {
	rows := tsarRows()
	rows[1].Notes = "Aged a year in bourbon barrels."
	units := sectionUnits(PrintSection{Rows: rows})
	if len(units) != 3 || units[0].Beer == nil || units[1].Beer == nil || units[2].Group == nil {
		t.Fatalf("want two plain rows, then the last two grouped; got %+v", units)
	}
}

// Only neighbours group. The same paragraph further down the section is a
// separate entry, so the menu's order is never rearranged to make a family.
func TestOnlyAdjacentRowsGroup(t *testing.T) {
	rows := tsarRows()[:2]
	rows = append(rows[:1], Beer{Name: "Other", Notes: "Different."}, rows[1])
	for _, u := range sectionUnits(PrintSection{Rows: rows}) {
		if u.Group != nil {
			t.Fatal("rows separated by another beer were grouped")
		}
	}
}

// Names that share only a short word are not a family: the description prints
// once, but each keeps its full name.
func TestUnrelatedNamesKeepTheirFullNames(t *testing.T) {
	rows := []Beer{
		{Name: "The Hammer", Notes: "Same blurb."},
		{Name: "The Anvil", Notes: "Same blurb."},
	}
	units := sectionUnits(PrintSection{Rows: rows})
	if len(units) != 1 || units[0].Group == nil {
		t.Fatalf("want one shared entry, got %d units", len(units))
	}
	g := units[0].Group
	if g.Name != "" || g.Rows[0].Label != "The Hammer" || g.Rows[1].Label != "The Anvil" {
		t.Errorf("got name %q and labels %q, %q", g.Name, g.Rows[0].Label, g.Rows[1].Label)
	}
}

// Rows with no description never group: two sodas with nothing written are
// not a family.
func TestEmptyDescriptionsNeverGroup(t *testing.T) {
	units := sectionUnits(PrintSection{Rows: []Beer{{Name: "Lemonade"}, {Name: "Lemonade Pink"}}})
	if len(units) != 2 {
		t.Fatalf("got %d units, want two plain rows", len(units))
	}
}

// Whitespace noise upstream must not split a family.
func TestDescriptionsCompareIgnoringSpacing(t *testing.T) {
	rows := tsarRows()[:2]
	rows[1].Notes = "  Layers of roasted grains and the sweetness of molasses and  brown sugar. This beer is huge! "
	if units := sectionUnits(PrintSection{Rows: rows}); len(units) != 1 {
		t.Fatalf("got %d units, want one group", len(units))
	}
}

// When the members pour different sizes, none is claimed for the group and
// each line carries its own.
func TestMixedSizesStayOnTheirLines(t *testing.T) {
	rows := tsarRows()[:2]
	rows[1].Pours = []Pour{{Size: "12oz", Label: "12oz Draft", Price: "9"}}
	g := sectionUnits(PrintSection{Rows: rows})[0].Group
	if g == nil || g.Size != "" {
		t.Fatalf("group size = %v, want empty for mixed pours", g)
	}
}

func TestCommonWordRun(t *testing.T) {
	for _, tc := range []struct {
		names []string
		want  string
	}{
		{[]string{"2026 Tsar Bomba", "Tsar Bomba W/ Snickers"}, "Tsar Bomba"},
		{[]string{"Little Dipper", "Pumpkin Little Dipper"}, "Little Dipper"},
		{[]string{"Galactic Orbit", "galactic orbit (Citra)"}, "Galactic Orbit"},
		{[]string{"Mendacious", "Newtonian"}, ""},
	} {
		got := commonWordRun(tc.names)
		if joined := strings.Join(got, " "); joined != tc.want {
			t.Errorf("commonWordRun(%q) = %q, want %q", tc.names, joined, tc.want)
		}
	}
}
