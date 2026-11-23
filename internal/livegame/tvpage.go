package livegame

import (
	"crypto/sha256"
	"encoding/hex"
	"html"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mrdon/kit/internal/buildinfo"
)

// DisplayVersion stamps a server-rendered TV page, in the menu board's
// style: a short readable token the screen polls and compares against its
// own, reloading when it changes.
//
// A stream is not enough on its own. The SSE frames carry the live state, but
// the QR code, the join words, the fonts and the heading are baked into the
// HTML at render time -- so a renamed night, or a newer game appearing on the
// stable address, leaves a screen showing something that is simply wrong
// until somebody walks over to it.
//
// CREATED_AT is the primary input, and it is the right one precisely because
// it is IMMUTABLE. A game's updated_at bumps on every move, so stamping that
// would reload the TV in the middle of play; created_at moves only when a
// genuinely different game becomes the newest, which is exactly when a
// screen parked on the stable address needs to repaint. created is nil when
// there is no game yet, and "empty" is a real value: a screen with no game
// must notice the first one.
//
// The title rides along because it is rendered into the HTML, and it is the
// only other thing a host can change mid-life.
//
// THE BUILD RIDES ALONG so a deploy reaches a wall that is already switched
// on. The reload costs a second of black and nothing else: every piece of
// game state lives on the server and the screen repaints from the stream.
func DisplayVersion(created *time.Time, title string) string {
	build := buildinfo.Token()
	if created == nil {
		return "empty-" + build
	}
	v := strconv.FormatInt(created.UnixNano(), 36)
	if title != "" {
		sum := sha256.Sum256([]byte(title))
		v += "-" + hex.EncodeToString(sum[:3])
	}
	return v + "-" + build
}

// WriteHTML serves a TV or phone page.
//
// Cache-Control: no-store is load-bearing rather than hygiene. These pages'
// whole purpose is to change, and an intermediary that caches one pins a wall
// panel to a game that finished last Tuesday with nothing on the admin side
// to explain why.
func WriteHTML(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, must-revalidate")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(body); err != nil {
		slog.Warn("writing live game page", "error", err)
	}
}

// NoGamePage is a stable TV address before any game exists.
//
// A 200 with a readable page beats a 404: the screen is working and correctly
// addressed, there is simply no game yet. A meta refresh is safe here and
// nowhere else in a game's pages: there is no SSE stream on this page to tear
// down, and it is what lets a screen hung on the wall pick up the first game
// without anyone rebooting it.
func NoGamePage(title string) []byte {
	return []byte(strings.Replace(noGameHTML, "{{TITLE}}", html.EscapeString(title), 1))
}

const noGameHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="refresh" content="15">
<title>{{TITLE}}</title>
<style>
  html,body{height:100%;margin:0}
  body{display:flex;align-items:center;justify-content:center;text-align:center;
       background:linear-gradient(160deg,#1b2b38,#14222d);color:#b9c4cf;
       font:400 1rem/1.5 system-ui,-apple-system,"Segoe UI",sans-serif;padding:2rem}
  h1{font-size:clamp(2rem,6vw,4.5rem);margin:0 0 .5rem;color:#f4efe7}
  p{margin:0;font-size:clamp(1rem,2.2vw,1.6rem);color:#8ea2b3}
</style>
</head>
<body><div>
  <h1>No game yet</h1>
  <p>This screen is working. Create a game and it will appear here.</p>
</div></body>
</html>
`
