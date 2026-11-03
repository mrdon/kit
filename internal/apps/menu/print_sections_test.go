package menu

import (
	"testing"
)

// Extras land beside their own heading rather than at the end of the document.
func TestMergeExtrasGroupsWithItsSection(t *testing.T) {
	rows := []Beer{
		{Section: "Specialty", Name: "Mars Water"},
		{Section: "Lagers", Name: "Cerveza"},
	}
	extras := []Beer{
		{Section: "Specialty", Name: "A can"},
		{Section: "Sodas", Name: "Lemonade"},
	}
	sections := buildSections(mergeExtras(rows, extras), nil)
	byName := map[string][]string{}
	for _, s := range sections {
		for _, r := range s.Rows {
			byName[s.Name] = append(byName[s.Name], r.Name)
		}
	}
	if len(byName["Specialty"]) != 2 {
		t.Errorf("Specialty has %v, want the tap and the can together", byName["Specialty"])
	}
	if len(byName["Sodas"]) != 1 {
		t.Errorf("Sodas has %v, want its own section", byName["Sodas"])
	}
}

// A blurb with no matching heading becomes a section of its own, at the end,
// with no rows. This is the snacks case: one heading, one line, no prices.
func TestBlurbBecomesItsOwnSection(t *testing.T) {
	rows := []Beer{
		{Section: "Lagers", Name: "Cerveza"},
		{Section: "Sodas", Name: "Lemonade"},
	}
	blurbs := map[string]string{
		"Snacks": "Pretzels, chips and popcorn — ask at the bar.",
		"sodas":  "All fountain, all refillable.",
	}
	got := buildSections(rows, blurbs)
	if len(got) != 3 {
		t.Fatalf("got %d sections, want Lagers, Sodas and Snacks", len(got))
	}
	// A blurb naming a heading that exists attaches to it, matched the same
	// case-insensitive way, and does not spawn a duplicate.
	if got[1].Name != "Sodas" || got[1].Blurb != blurbs["sodas"] {
		t.Errorf("Sodas = %+v, want the blurb attached to the existing section", got[1])
	}
	if len(got[1].Rows) != 1 {
		t.Errorf("Sodas has %d rows, want its lemonade kept", len(got[1].Rows))
	}
	// One that names nothing lands last, and carries no rows at all.
	if got[2].Name != "Snacks" || len(got[2].Rows) != 0 {
		t.Errorf("last section = %+v, want a rowless Snacks", got[2])
	}
}

// An empty blurb is not a section. A console that writes every key it knows
// about would otherwise print a heading with nothing under it.
func TestEmptyBlurbIsIgnored(t *testing.T) {
	got := buildSections([]Beer{{Section: "Lagers", Name: "a"}},
		map[string]string{"Snacks": "   "})
	if len(got) != 1 {
		t.Fatalf("got %d sections, want only Lagers", len(got))
	}
}

// Untappd's heading and a hand-typed extra that differ only in punctuation are
// one section, under the spelling the tap list used.
func TestSectionsMatchIgnoringPunctuation(t *testing.T) {
	rows := []Beer{{Section: "Non Alcoholic", Name: "Zero-G Hop Water"}}
	extras := []Beer{{Section: "Non-Alcoholic", Name: "Athletic Upside Dawn"}}
	got := buildSections(mergeExtras(rows, extras), nil)
	if len(got) != 1 || len(got[0].Rows) != 2 {
		t.Fatalf("got %d sections; want one holding both rows", len(got))
	}
	if got[0].Name != "Non Alcoholic" {
		t.Errorf("section name = %q, want the tap list's spelling", got[0].Name)
	}
}
