package trivia

import (
	"strings"
	"testing"
)

// The one hard requirement: somebody reads it off a TV across a loud room and
// types it into a phone correctly on the first try.
func TestWordListsMeetTheReadAloudCriteria(t *testing.T) {
	lists := map[string][]string{
		"adjectives": adjectives, "animals": animals, "objects": objects,
	}
	for name, list := range lists {
		if len(list) < 100 {
			t.Errorf("%s has only %d words — too few for a name space this shallow", name, len(list))
		}
		for _, w := range list {
			if len(w) < 4 || len(w) > 7 {
				t.Errorf("%s: %q is %d letters, want 4-7", name, w, len(w))
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
	for name, list := range map[string][]string{
		"adjectives": adjectives, "animals": animals, "objects": objects,
	} {
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
		if len(parts) != 3 {
			t.Fatalf("%q has %d parts, want 3", name, len(parts))
		}
		if !IsValidGameName(name) {
			t.Fatalf("%q was generated but fails IsValidGameName", name)
		}
	}
}

// The validator guards a public URL segment, so anything path-shaped has to
// bounce before it reaches a query.
func TestIsValidGameName(t *testing.T) {
	good := []string{"brave-otter-lamp", "quiet-heron-anchor", "brave-otter-lamp-2"}
	for _, s := range good {
		if !IsValidGameName(s) {
			t.Errorf("%q should be valid", s)
		}
	}
	bad := []string{
		"", "brave", "brave-otter", "brave--lamp", "Brave-Otter-Lamp",
		"../../etc/passwd", "brave-otter-lamp-", "brave-otter-lamp/tv",
		"brave-otter-lamp-x", strings.Repeat("a-", 30),
	}
	for _, s := range bad {
		if IsValidGameName(s) {
			t.Errorf("%q should be rejected", s)
		}
	}
}

// Drawing from ten million combinations, two hundred draws should not repeat.
// Not a distribution proof — a guard against wiring every game to one seed.
func TestRandomNameDoesNotRepeatImmediately(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		n := randomName()
		if seen[n] {
			t.Fatalf("%q drawn twice in 200 tries — the source is not random", n)
		}
		seen[n] = true
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
