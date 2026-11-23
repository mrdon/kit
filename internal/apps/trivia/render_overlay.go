package trivia

import "html/template"

// The OBS overlay: the TV page reduced to what the room needs while a game is
// on, laid out as an L down the left and along the bottom, with the rest of
// the 1920x1080 stage left TRANSPARENT so a compositor can put the broadcast
// underneath. Served from the same addresses as the wall with ?overlay=1.
//
// The hole is 1440x810 at the top right: exactly 75% of the stage and the
// same 16:9 shape, so the game scales to 75% in OBS once and fits with no
// crop. Change it in overlay.css and that sentence stops being true.
//
// It is deliberately NOT the wall squeezed into a corner. The board, the
// dealt cards and the podium are built for a whole screen; here they become
// a topic list, a strip of answers and a line of text. Only the transport
// (core.js) and the boot are shared, so the overlay hears exactly what the
// wall hears and recovers from bad wifi the same way.

var overlayTmpl = template.Must(template.ParseFS(templateFS, "templates/overlay.html.tmpl"))

var (
	overlaySheets  = []string{"overlay.css"}
	overlayScripts = []string{"core.js", "overlay.js", "boot.js"}
)

// RenderOverlay produces the self-contained overlay page. Same data as the
// wall, and the same version stamp, so a renamed night reloads it too.
//
// explainerLink is not shown -- there is no lobby video here -- but it is
// part of the stamp tv.version answers with, and a page whose stamp leaves it
// out would never match and would reload every fifteen seconds all night.
func RenderOverlay(baseURL, slug string, game *Game, followLatest bool, explainerLink string) (string, error) {
	data, err := displayDataFor(baseURL, slug, game, followLatest, explainerLink)
	if err != nil {
		return "", err
	}
	return renderPage(overlayTmpl, "overlay.html.tmpl", overlaySheets, overlayScripts, data)
}
