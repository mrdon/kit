package trivia

import "testing"

// Trivia's cookie lives under its own app segment, so it never shadows (or
// is shadowed by) another live game's cookie on the same phone.
func TestCookiePathIsTrivias(t *testing.T) {
	if got := CookiePath("acme", "brave-otter-lamp"); got != "/acme/trivia/brave-otter-lamp" {
		t.Fatalf("path = %q", got)
	}
}
