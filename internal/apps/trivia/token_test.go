package trivia

import (
	"testing"

	"github.com/google/uuid"
)

// The token never touches a row: a database dump must not be a set of usable
// identities.
func TestTokenHashIsStableAndNotTheToken(t *testing.T) {
	tok := NewTeamToken()
	if tok == "" {
		t.Fatal("empty token")
	}
	h := HashToken(tok)
	if h == tok {
		t.Fatal("the stored value is the token itself")
	}
	if HashToken(tok) != h {
		t.Fatal("hash is not stable")
	}
	if HashToken(NewTeamToken()) == h {
		t.Fatal("two tokens hashed the same")
	}
}

func TestCookieRoundTrip(t *testing.T) {
	id := uuid.New()
	tok := NewTeamToken()
	gotID, gotTok, ok := ParseCookieValue(CookieValue(id, tok))
	if !ok || gotID != id || gotTok != tok {
		t.Fatalf("round trip gave (%v, %q, %v)", gotID, gotTok, ok)
	}
}

// A malformed cookie is treated as no cookie at all: the phone becomes a
// spectator and can join again, rather than hitting an error it cannot clear.
func TestParseCookieValueRejectsGarbage(t *testing.T) {
	for _, v := range []string{"", "nonsense", "not-a-uuid.token", uuid.New().String(), uuid.New().String() + "."} {
		if _, _, ok := ParseCookieValue(v); ok {
			t.Errorf("%q was accepted", v)
		}
	}
}

// Path-scoping is what lets one phone hold identities for two games at once
// without either shadowing the other.
func TestCookiePathIsPerGame(t *testing.T) {
	a := CookiePath("acme", "brave-otter-lamp")
	b := CookiePath("acme", "quiet-heron-anchor")
	if a == b {
		t.Fatal("two games share a cookie path")
	}
	if a != "/acme/trivia/brave-otter-lamp" {
		t.Fatalf("path = %q", a)
	}
}
