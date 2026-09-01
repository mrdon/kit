package trivia

import (
	"slices"
	"strings"
	"testing"
)

// The one hard requirement: somebody reads it off a TV across a loud room and
// types it into a phone correctly on the first try.
func TestWordListsMeetTheReadAloudCriteria(t *testing.T) {
	lists := map[string][]string{"verbs": verbs, "animals": animals}
	for name, list := range lists {
		if len(list) < 100 {
			t.Errorf("%s has only %d words — too few for a name space this shallow", name, len(list))
		}
		for _, w := range list {
			if len(w) < 3 || len(w) > 9 {
				t.Errorf("%s: %q is %d letters, want 3-9", name, w, len(w))
			}
			for _, r := range w {
				if r < 'a' || r > 'z' {
					t.Errorf("%s: %q is not lowercase ASCII", name, w)
					break
				}
			}
		}
	}
}

// A real and easy bug: the same word twice in one list skews nothing but
// looks sloppy, and the same word in two lists produces "otter-otter-lamp".
func TestWordListsHaveNoDuplicatesOrOverlap(t *testing.T) {
	seen := map[string]string{}
	for name, list := range map[string][]string{"verbs": verbs, "animals": animals} {
		local := map[string]bool{}
		for _, w := range list {
			if local[w] {
				t.Errorf("%s contains %q twice", name, w)
			}
			local[w] = true
			if other, ok := seen[w]; ok && other != name {
				t.Errorf("%q appears in both %s and %s", w, other, name)
			}
			seen[w] = name
		}
	}
}

// The shape the URL validator and the TV layout both depend on.
func TestRandomNameShape(t *testing.T) {
	for range 200 {
		name := randomName()
		parts := strings.Split(name, "-")
		if len(parts) != 2 {
			t.Fatalf("%q has %d parts, want 2", name, len(parts))
		}
		// The first word is the verb, which is what gives the name a shape
		// somebody can hold on to between the screen and their phone.
		if !slices.Contains(verbs, parts[0]) {
			t.Fatalf("%q does not start with a verb", name)
		}
		if !IsValidGameName(name) {
			t.Fatalf("%q was generated but fails IsValidGameName", name)
		}
	}
}

// The validator guards a public URL segment, so anything path-shaped has to
// bounce before it reaches a query.
func TestIsValidGameName(t *testing.T) {
	// The two-word names the generator makes now, AND the three-word ones it
	// used to. Names are permanent and public — a validator that rejects
	// yesterday's names 404s games that are still on a screen.
	good := []string{
		"jumping-lion", "prowling-otter", "jumping-lion-2",
		"vague-jaguar-coin", "brave-otter-lamp", "brave-otter-lamp-3",
	}
	for _, s := range good {
		if !IsValidGameName(s) {
			t.Errorf("%q should be valid", s)
		}
	}
	bad := []string{
		"", "jumping", "tv", "state", "jumping--lion", "Jumping-Lion",
		"../../etc/passwd", "jumping-lion-", "jumping-lion/tv",
		"a-b-c-d-e", "1-2", strings.Repeat("a-", 30),
	}
	for _, s := range bad {
		if IsValidGameName(s) {
			t.Errorf("%q should be rejected", s)
		}
	}
}

// A guard against wiring every game to one seed — not a distribution proof.
//
// Deliberately NOT "no repeats in 200 draws". The two-word space is about
// 65,000 combinations, so the birthday paradox makes at least one collision
// in 200 draws a coin-flip; asserting zero would be a test that fails a
// quarter of the time for no reason. Uniqueness is guaranteed by the unique
// index and the retry, not by the size of the space.
func TestRandomNameIsActuallyRandom(t *testing.T) {
	const draws = 200
	seen := map[string]bool{}
	for range draws {
		seen[randomName()] = true
	}
	if len(seen) < draws*9/10 {
		t.Fatalf("only %d distinct names in %d draws — the source is not random", len(seen), draws)
	}
}

// The reclaim code is four digits, always, including when the draw is small.
func TestReclaimCodeIsFourDigits(t *testing.T) {
	for range 100 {
		c := ReclaimCode()
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
		if !IsValidGameName(name) {
			t.Errorf("%q no longer resolves — a game that exists would 404", name)
		}
	}
}
