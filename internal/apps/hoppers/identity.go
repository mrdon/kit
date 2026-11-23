package hoppers

import "github.com/mrdon/kit/internal/livegame"

// PlayerCookieName is the cookie a phone carries: livegame's participant
// cookie, Path-scoped to one game.
const PlayerCookieName = "kit_hoppers"

// shortJoinRoute is Hoppers' segment of the root-level short join link,
// /j/h/{code}.
const shortJoinRoute = "h"

// CookiePath scopes the cookie to one game.
func CookiePath(slug, gameName string) string {
	return livegame.CookiePath(slug, AppName, gameName)
}

// ShortJoinURL is what the QR encodes and the TV prints.
func ShortJoinURL(baseURL, code string) string {
	return livegame.ShortJoinURL(baseURL, shortJoinRoute, code)
}

// JoinURL is the game's full address, which the short link redirects to.
func JoinURL(slug, gameName string) string {
	return "/" + slug + "/" + AppName + "/" + gameName
}
