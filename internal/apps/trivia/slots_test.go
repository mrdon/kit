package trivia

import "testing"

// 1969 and 1969.0 are the same number and must be the same card. Two cards
// showing "1969" side by side on a TV is the kind of bug the whole room sees
// at once.
func TestBuildSlotsCollapsesIdenticalValues(t *testing.T) {
	ids := teamIDs(2)
	slots := BuildSlots([]TeamAnswer{
		{TeamID: ids[0], Value: 1969, Raw: "1969"},
		{TeamID: ids[1], Value: 1969.0, Raw: "1969.0"},
	})
	if len(slots) != 2 {
		t.Fatalf("got %d slots, want 2 (pseudo + one card)", len(slots))
	}
	if got := len(slots[1].TeamIDs); got != 2 {
		t.Fatalf("collapsed card carries %d teams, want both", got)
	}
}

// Sorting is what makes the betting spatial, so a table scanning the TV can
// reason about "somewhere in the middle" instead of reading twenty numbers.
func TestBuildSlotsSortsAscending(t *testing.T) {
	ids := teamIDs(4)
	slots := BuildSlots([]TeamAnswer{
		{TeamID: ids[0], Value: 300},
		{TeamID: ids[1], Value: 12},
		{TeamID: ids[2], Value: 1000},
		{TeamID: ids[3], Value: 55},
	})
	want := []float64{12, 55, 300, 1000}
	if len(slots) != len(want)+1 {
		t.Fatalf("got %d slots, want %d", len(slots), len(want)+1)
	}
	for i, w := range want {
		s := slots[i+1]
		if s.Value == nil || *s.Value != w {
			t.Fatalf("slot %d = %v, want %v", i+1, s.Value, w)
		}
		if s.Position != i+1 {
			t.Fatalf("slot at index %d has position %d", i+1, s.Position)
		}
	}
}

// The pseudo-slot is always there and always leftmost, which is what keeps
// the winner from ever being nothing.
func TestBuildSlotsAlwaysHasThePseudoSlotFirst(t *testing.T) {
	ids := teamIDs(1)
	for _, answers := range [][]TeamAnswer{
		nil,
		{{TeamID: ids[0], Value: 5}},
	} {
		slots := BuildSlots(answers)
		if len(slots) == 0 {
			t.Fatal("no slots at all")
		}
		first := slots[0]
		if first.Position != 0 || !first.IsPseudo() || first.Label != PseudoSlotLabel {
			t.Fatalf("first slot = %+v, want the pseudo-slot at position 0", first)
		}
		if len(first.TeamIDs) != 0 {
			t.Fatal("nobody writes the pseudo-slot, so it must carry no teams")
		}
	}
}

// A round where every team stayed silent still produces a bettable board:
// one card, the pseudo-slot, which is exactly right — the answer really is
// smaller than all zero of the guesses.
func TestBuildSlotsWithNoAnswers(t *testing.T) {
	slots := BuildSlots(nil)
	if len(slots) != 1 {
		t.Fatalf("got %d slots for an empty round, want just the pseudo-slot", len(slots))
	}
}

// The card shows what the team typed, not %g output. "1.2e+03" at 120px on a
// bar TV would be a real defect.
func TestBuildSlotsLabelsUseWhatWasTyped(t *testing.T) {
	ids := teamIDs(2)
	slots := BuildSlots([]TeamAnswer{
		{TeamID: ids[0], Value: 1200, Raw: "1,200"},
		{TeamID: ids[1], Value: 3000000},
	})
	if slots[1].Label != "1,200" {
		t.Fatalf("label = %q, want the typed spelling", slots[1].Label)
	}
	// With nothing typed we format it ourselves, still readably.
	if slots[2].Label != "3,000,000" {
		t.Fatalf("fallback label = %q, want 3,000,000", slots[2].Label)
	}
}

// The first spelling seen wins when two teams write the same number
// differently, so the card is stable rather than flickering between them.
func TestBuildSlotsKeepsTheFirstSpelling(t *testing.T) {
	ids := teamIDs(2)
	slots := BuildSlots([]TeamAnswer{
		{TeamID: ids[0], Value: 1200, Raw: "1200"},
		{TeamID: ids[1], Value: 1200, Raw: "$1,200"},
	})
	if slots[1].Label != "1200" {
		t.Fatalf("label = %q, want the first spelling", slots[1].Label)
	}
}
