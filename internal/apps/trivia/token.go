package trivia

import "github.com/mrdon/kit/internal/livegame"

// A team's identity is the shared participant cookie (livegame/identity.go):
// a random token in an HttpOnly cookie, of which only a SHA-256 hash is
// stored on the team row.

// TeamCookieName is the cookie a phone carries.
const TeamCookieName = "kit_trivia"

// CookiePath scopes the cookie to one game, so a phone that played two games
// tonight holds two identities without either shadowing the other.
func CookiePath(slug, gameName string) string {
	return livegame.CookiePath(slug, AppName, gameName)
}
