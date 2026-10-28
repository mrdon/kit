package menu

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func eightySixBoard() *Board {
	return &Board{Taps: []Tap{
		{Section: "Lagers", Name: "Cerveza Espacial", Price: "6.50"},
		{Section: "Lagers", Name: "Cerveza Espacial Lime", Price: "7"},
		{Section: "IPAs", Name: "Galactic  Orbit", Price: "8"},
	}}
}

func TestDropEightySixHidesOnlyTheListedTaps(t *testing.T) {
	b := eightySixBoard()
	dropEightySix(b, []string{"galactic orbit", "Cerveza Espacial"})
	if len(b.Taps) != 1 || b.Taps[0].Name != "Cerveza Espacial Lime" {
		t.Errorf("taps = %+v, want only the lime left", b.Taps)
	}
}

func TestDropEightySixLeavesTheStoredTapsAlone(t *testing.T) {
	// The wall and the version stamp each parse their own copy, but the
	// filter must not write through to a slice somebody else holds.
	b := eightySixBoard()
	orig := b.Taps
	dropEightySix(b, []string{"Cerveza Espacial"})
	if orig[0].Name != "Cerveza Espacial" {
		t.Errorf("the original slice was rewritten: %+v", orig)
	}
}

func TestStillPouringClearsWhatUntappdDropped(t *testing.T) {
	got := stillPouring([]string{"Galactic Orbit", "Long Gone Stout"}, eightySixBoard().Taps)
	if !slices.Equal(got, []string{"Galactic Orbit"}) {
		t.Errorf("kept %v, want only the beer still on the board", got)
	}
}

func TestMatchBeer(t *testing.T) {
	taps := []string{"Cerveza Espacial", "Cerveza Espacial Lime", "Galactic Orbit"}
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"galactic", "Galactic Orbit", true},
		{"cerveza espacial", "Cerveza Espacial", true}, // exact beats the longer name
		{"lime", "Cerveza Espacial Lime", true},
		{"cerveza", "", false}, // two beers contain it
		{"stout", "", false},
		{"  ", "", false},
	}
	for _, c := range cases {
		got, ok := match86(c.in, taps)
		if got != c.want || ok != c.ok {
			t.Errorf("match86(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestResolveAllListsTheChoicesOnAMiss(t *testing.T) {
	_, err := resolveAll([]string{"cerveza"}, []string{"Cerveza Espacial", "Cerveza Espacial Lime"}, "beer on the board")
	if !errors.Is(err, ErrPayloadInvalid) || !strings.Contains(err.Error(), "Cerveza Espacial Lime") {
		t.Errorf("err = %v, want the board listed", err)
	}
}

func TestEightySixVersionMovesWithTheList(t *testing.T) {
	if got := eightySixVersion(nil, "v"); got != "v" {
		t.Errorf("nothing 86'd should keep the stamp, got %q", got)
	}
	a := eightySixVersion([]string{"Galactic Orbit"}, "v")
	if a == "v" || !strings.HasSuffix(a, ".v") {
		t.Errorf("an 86 should prefix the stamp, got %q", a)
	}
	if b := eightySixVersion([]string{"galactic  orbit"}, "v"); b != a {
		t.Errorf("spelling the same beer differently should not reload the wall: %q vs %q", a, b)
	}
}

func TestDropEightySixRows(t *testing.T) {
	rows := []Beer{{Name: "Galactic Orbit"}, {Name: "Cerveza Espacial"}}
	got := dropEightySixRows(rows, []string{"GALACTIC ORBIT"})
	if len(got) != 1 || got[0].Name != "Cerveza Espacial" {
		t.Errorf("rows = %+v", got)
	}
}

func TestEightySixArgs(t *testing.T) {
	args, err := parseEightySixArgs([]byte(`{"add":["hazy"],"remove":["stout"]}`))
	if err != nil || !slices.Equal(args.Add, []string{"hazy"}) || !slices.Equal(args.Remove, []string{"stout"}) {
		t.Errorf("args = %+v, %v", args, err)
	}
}
