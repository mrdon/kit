package trivia

import (
	"testing"

	"github.com/mrdon/kit/internal/livegame"
)

// The reclaim code is four digits, always, including when the draw is small.
func TestReclaimCodeIsFourDigits(t *testing.T) {
	for range 100 {
		c := livegame.ReclaimCode()
		if len(c) != 4 {
			t.Fatalf("code %q is not four characters", c)
		}
		for _, r := range c {
			if r < '0' || r > '9' {
				t.Fatalf("code %q is not all digits", c)
			}
		}
	}
}

// A name minted by ANY scheme this app has used must keep resolving. Names go
// on a whiteboard behind the bar and into a URL a television is parked on;
// changing the generator must never invalidate one that already exists.
//
// This is a regression test with a specific history: shortening the generator
// from three words to two also tightened the validator to "exactly two", and
// every game created before that 404'd — their TV screens and their players'
// join links all at once.
func TestNamesFromEveryGeneratorStillResolve(t *testing.T) {
	for _, name := range []string{
		"vague-jaguar-coin",  // the original three-word scheme
		"brave-otter-lamp",   // ditto
		"quiet-heron-anchor", // ditto
		"jumping-lion",       // the current verb-animal scheme
		"prowling-otter",     // ditto
		"jumping-lion-2",     // a collision suffix on the current scheme
		"brave-otter-lamp-2", // a collision suffix on the old one
	} {
		if !livegame.IsValidGameName(name) {
			t.Errorf("%q no longer resolves — a game that exists would 404", name)
		}
	}
}
