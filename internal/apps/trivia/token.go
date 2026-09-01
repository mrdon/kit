package trivia

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"

	"github.com/google/uuid"
)

// A team's identity is a random token held in an HttpOnly cookie, of which
// only a SHA-256 hash is stored.
//
// IT IS A COOKIE, NOT A LOCALSTORAGE BEARER TOKEN, and the deciding fact is
// EventSource: it cannot set headers, so a bearer token would have to ride in
// the stream's query string -- into access logs, and into any screenshot of
// the URL somebody posts. A cookie is sent automatically on the stream GET,
// survives a phone lock and a browser kill, and Path-scoping lets one phone
// hold identities for two different games at once.

// TeamCookieName is the cookie a phone carries.
const TeamCookieName = "kit_trivia"

// CookieMaxAge is six hours -- long enough for a quiz night plus the drinks
// after, short enough that a borrowed phone does not stay joined forever.
const CookieMaxAge = 6 * 60 * 60

// NewTeamToken mints a team's secret.
func NewTeamToken() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is a broken machine. Returning an empty token
		// makes the join fail loudly rather than issuing a guessable one.
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// HashToken is what the database stores. The token itself never touches a row,
// so a database dump is not a set of usable identities.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// CookieValue packs the team id and its token into one cookie. The id is
// carried so the lookup is a single indexed row rather than a scan comparing
// hashes across every team in the game.
func CookieValue(teamID uuid.UUID, token string) string {
	return teamID.String() + "." + token
}

// ParseCookieValue unpacks it. A malformed cookie is treated as no cookie at
// all -- the phone becomes a spectator and can join again.
func ParseCookieValue(v string) (uuid.UUID, string, bool) {
	id, token, ok := strings.Cut(v, ".")
	if !ok || token == "" {
		return uuid.Nil, "", false
	}
	teamID, err := uuid.Parse(id)
	if err != nil {
		return uuid.Nil, "", false
	}
	return teamID, token, true
}

// CookiePath scopes the cookie to one game, so a phone that played two games
// tonight holds two identities without either shadowing the other.
func CookiePath(slug, gameName string) string {
	return "/" + slug + "/trivia/" + gameName
}

// ReclaimCode is the four digits a host reads out to a table whose phone
// died. Short because it is spoken across a bar, and single-use at the
// service layer.
//
// This is deliberately the ONLY re-entry path: "pick your team from this
// list" with twenty names on a TV screen is an impersonation hole. The trust
// boundary belongs with the person standing in the room who can see who is
// asking.
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
