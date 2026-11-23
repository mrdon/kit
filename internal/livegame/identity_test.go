package livegame

import (
	"testing"

	"github.com/google/uuid"
)

// The token never touches a row: a database dump must not be a set of usable
// identities.
func TestTokenHashIsStableAndNotTheToken(t *testing.T) {
	tok := NewToken()
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
	if HashToken(NewToken()) == h {
		t.Fatal("two tokens hashed the same")
	}
}

func TestCookieRoundTrip(t *testing.T) {
	id := uuid.New()
	tok := NewToken()
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
	a := CookiePath("acme", "trivia", "brave-otter-lamp")
	b := CookiePath("acme", "trivia", "quiet-heron-anchor")
	if a == b {
		t.Fatal("two games share a cookie path")
	}
	if a != "/acme/trivia/brave-otter-lamp" {
		t.Fatalf("path = %q", a)
	}
}

// A reclaim code is bound to one participant in one game: the same four
// digits issued for somebody else must not redeem this one.
func TestReclaimSecretIsPerParticipant(t *testing.T) {
	game, a, b := uuid.New(), uuid.New(), uuid.New()
	if ReclaimSecret(game, a, "1234") == ReclaimSecret(game, b, "1234") {
		t.Fatal("two participants share a reclaim secret")
	}
	if ReclaimSecret(game, a, "1234") == ReclaimSecret(uuid.New(), a, "1234") {
		t.Fatal("two games share a reclaim secret")
	}
}
