package menu

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// TestBoardAcceptsTheTapListsVenuesActuallyHave pins the cap to the layout
// rather than to the day it was written.
//
// The board arrived carrying 20 beers and the sync refused every one of them,
// because MaxTaps was 18 -- a number fixed when the rows were drawn at one
// size and never revisited after the fit pass taught the board to shrink. The
// cost of refusing is not a missing beer, it is a stale wall: a rejected sync
// leaves the previous list up, still naming kegs that have blown.
//
// Re-measure before moving this. The board is only honest about its capacity
// in a browser, because the fit pass is what sets it:
//
//	MENU_PREVIEW_UNTAPPD=... MENU_PREVIEW_OUT=/tmp/board.html \
//	  go test ./internal/apps/menu/ -run TestPreview
//
// then open it at 1920x1080 and check that no .tapcol scrolls.
func TestBoardAcceptsTheTapListsVenuesActuallyHave(t *testing.T) {
	for _, n := range []int{1, 18, 20, MaxTaps} {
		b := &Board{Venue: Venue{Wordmark: "G"}, Taps: manyTaps(n)}
		if err := b.Validate(); err != nil {
			t.Errorf("%d taps rejected: %v", n, err)
		}
	}
}

// TestBoardRejectsMoreThanFits keeps the door shut past the point the fit pass
// bottoms out, where the overflow stops being small type and starts being rows
// clipped off the bottom of the screen with nothing to say so.
func TestBoardRejectsMoreThanFits(t *testing.T) {
	b := &Board{Venue: Venue{Wordmark: "G"}, Taps: manyTaps(MaxTaps + 1)}
	err := b.Validate()
	if !errors.Is(err, ErrPayloadInvalid) {
		t.Fatalf("want ErrPayloadInvalid, got %v", err)
	}
	// The message has to name both numbers: somebody reading a failing sync
	// needs to know how far over the board is, not just that it is over.
	for _, want := range []string{strconv.Itoa(MaxTaps + 1), strconv.Itoa(MaxTaps)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}

func manyTaps(n int) []Tap {
	// Spread across sections the way a real list is, since a heading costs
	// column height too and a single-section board is the easy case.
	out := make([]Tap, 0, n)
	sections := []string{"Lagers", "Pub Ales", "Belgian Styles", "Pale Ales & IPAs"}
	for i := range n {
		t := tap(sections[i*len(sections)/n], fmt.Sprintf("Beer %d", i+1), DefaultPour)
		out = append(out, t)
	}
	return out
}
