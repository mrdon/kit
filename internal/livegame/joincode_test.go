package livegame

import (
	"strings"
	"testing"
)

// Codes are drawn from an alphabet with the characters that get misread
// stripped out — a code is read aloud across a bar when a camera will not
// focus, and "was that an i or a 1" is the failure that causes.
func TestJoinCodesAvoidAmbiguousCharacters(t *testing.T) {
	for range 200 {
		code := NewJoinCode()
		if len(code) != joinCodeLength {
			t.Fatalf("code %q is not %d characters", code, joinCodeLength)
		}
		for _, bad := range []string{"i", "l", "o", "u"} {
			if strings.Contains(code, bad) {
				t.Fatalf("code %q contains the ambiguous character %q", code, bad)
			}
		}
	}
}

// Every game app gets its own one-letter segment under /j/, and a trailing
// slash on the base URL must not double up.
func TestShortJoinURL(t *testing.T) {
	if got := ShortJoinURL("https://kit.example.com/", "t", "ab3k9"); got != "https://kit.example.com/j/t/ab3k9" {
		t.Fatalf("ShortJoinURL = %q", got)
	}
}
