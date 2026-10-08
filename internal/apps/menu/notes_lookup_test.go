package menu

import "testing"

// The case that started this: a description pushed in as "Mr Radar Nitro"
// must print under the board's "Mr. Radar (Nitro)", and a sync must report
// it as described rather than missing.
func TestFindNoteAcrossSpellings(t *testing.T) {
	store := map[string]string{
		"mr radar nitro": "Nitro version of our coffee porter.",
		"mars water":     "A lager.",
		"newtonian":      "An IPA.",
	}
	cases := map[string]string{
		"Mr. Radar (Nitro)":  "Nitro version of our coffee porter.",
		"Mr Radar Nitro":     "Nitro version of our coffee porter.",
		"Mr Radar":           "Nitro version of our coffee porter.",
		"Mars Water":         "A lager.",
		"MARS  WATER (16oz)": "A lager.",
		"Newtonian IPA":      "An IPA.",
	}
	for name, want := range cases {
		got, ok := findNote(store, name)
		if !ok || got != want {
			t.Errorf("findNote(%q) = %q, %v; want %q", name, got, ok, want)
		}
	}
	for _, name := range []string{"Oktoberfest", "Ma", ""} {
		if got, ok := findNote(store, name); ok {
			t.Errorf("findNote(%q) = %q, want nothing", name, got)
		}
	}
}

// The more specific stored name wins when both could match a nitro row.
func TestFindNotePrefersTheLongerMatch(t *testing.T) {
	store := map[string]string{"mr radar": "Coffee porter.", "mr radar nitro": "Nitro coffee porter."}
	if got, _ := findNote(store, "Mr. Radar (Nitro)"); got != "Nitro coffee porter." {
		t.Fatalf("nitro row got %q", got)
	}
	if got, _ := findNote(store, "Mr. Radar"); got != "Coffee porter." {
		t.Fatalf("plain row got %q", got)
	}
}

// Written beats stored, and a blank written entry is not a veto.
func TestMergedNotesWrittenWins(t *testing.T) {
	cache := map[string]string{"mr radar nitro": "Scraped."}
	written := map[string]string{"Mr. Radar (Nitro)": "Written.", "Mars Water": "  "}
	m := mergedNotes(cache, written)
	if got, _ := findNote(m, "Mr. Radar (Nitro)"); got != "Written." {
		t.Fatalf("got %q, want the written one", got)
	}
	if _, ok := findNote(m, "Mars Water"); ok {
		t.Fatal("a blank written note counted as a description")
	}
	if cache["mr radar nitro"] != "Scraped." {
		t.Fatal("mergedNotes mutated its input")
	}
}
