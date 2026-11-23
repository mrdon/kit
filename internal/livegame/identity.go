package livegame

import (
	"crypto/rand"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/auth/opaquetoken"
)

// A participant's identity (a trivia team, a Hoppers frog) is a random token
// held in an HttpOnly cookie, of which only a SHA-256 hash is stored.
//
// IT IS A COOKIE, NOT A LOCALSTORAGE BEARER TOKEN, and the deciding fact is
// EventSource: it cannot set headers, so a bearer token would have to ride in
// the stream's query string -- into access logs, and into any screenshot of
// the URL somebody posts. A cookie is sent automatically on the stream GET,
// survives a phone lock and a browser kill, and Path-scoping lets one phone
// hold identities for two different games at once.
//
// A participant is not an actor with a Kit identity, so this stays its own
// cookie mechanism; the random bytes and the hash come from opaquetoken.

// CookieMaxAge is six hours -- long enough for a game night plus the drinks
// after, short enough that a borrowed phone does not stay joined forever.
const CookieMaxAge = 6 * 60 * 60

// NewToken mints a participant's secret. crypto/rand failing is a broken
// machine; the empty string it returns then makes the join fail loudly
// rather than issuing a guessable token.
func NewToken() string {
	token, _, err := opaquetoken.New("")
	if err != nil {
		return ""
	}
	return token
}

// HashToken is what the database stores. The token itself never touches a
// row, so a database dump is not a set of usable identities.
func HashToken(token string) string {
	return opaquetoken.Hash(token)
}

// CookieValue packs the participant id and its token into one cookie. The id
// is carried so the lookup is a single indexed row rather than a scan
// comparing hashes across every participant in the game.
func CookieValue(id uuid.UUID, token string) string {
	return id.String() + "." + token
}

// ParseCookieValue unpacks it. A malformed cookie is treated as no cookie at
// all -- the phone becomes a spectator and can join again.
func ParseCookieValue(v string) (uuid.UUID, string, bool) {
	raw, token, ok := strings.Cut(v, ".")
	if !ok || token == "" {
		return uuid.Nil, "", false
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, "", false
	}
	return id, token, true
}

// CookiePath scopes the cookie to one game, so a phone that played two games
// tonight holds two identities without either shadowing the other.
func CookiePath(slug, app, gameName string) string {
	return "/" + slug + "/" + app + "/" + gameName
}

// SetCookie issues a participant's identity.
//
// HttpOnly because nothing in the page needs to read it and a token in JS is
// a token in a screenshot; SameSite=Lax because the phone arrives from a QR
// scan, which is a top-level navigation. Secure whenever the request or the
// configured base URL is https.
func SetCookie(w http.ResponseWriter, r *http.Request, baseURL, name, path, value string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     path,
		HttpOnly: true,
		Secure:   r.TLS != nil || strings.HasPrefix(baseURL, "https://"),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   CookieMaxAge,
	})
}

// ReclaimCode is the four digits a host reads out to a participant whose
// phone died. Short because it is spoken across a bar, and single-use at the
// service layer.
//
// This is deliberately the ONLY re-entry path: "pick yourself from this list"
// with twenty names on a TV screen is an impersonation hole. The trust
// boundary belongs with the person standing in the room who can see who is
// asking -- not with four digits being hard to guess.
func ReclaimCode() string {
	var b [2]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "0000"
	}
	n := (int(b[0])<<8 | int(b[1])) % 10000
	digits := []byte{
		byte('0' + n/1000%10),
		byte('0' + n/100%10),
		byte('0' + n/10%10),
		byte('0' + n%10),
	}
	return string(digits)
}

// ReclaimSecret binds a spoken code to one participant in one game, so the
// same four digits issued for someone else are not interchangeable. Its hash
// is stored in place of the participant's token until the code is redeemed,
// which also signs the lost phone out.
func ReclaimSecret(gameID, participantID uuid.UUID, code string) string {
	return "reclaim:" + gameID.String() + ":" + participantID.String() + ":" + code
}
