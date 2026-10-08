package trivia

import "strings"

// JoinURL is the address a phone lands on after scanning, and the one a host
// reads out. Built from the configured base URL so the console and the TV can
// never disagree about where a game lives.
func JoinURL(baseURL, slug, gameName string) string {
	return strings.TrimRight(baseURL, "/") + "/" + slug + "/trivia/" + gameName
}
