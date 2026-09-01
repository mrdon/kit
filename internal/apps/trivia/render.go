package trivia

import (
	"bytes"
	"embed"
	"encoding/base64"
	"fmt"
	"html/template"
	"strings"
)

//go:embed templates/display.html.tmpl templates/display.css templates/display.js
var templateFS embed.FS

//go:embed assets/bungee.woff2 assets/exo2.woff2
var assetFS embed.FS

// displayTmpl is parsed once at package scope; a template that fails to parse
// is a build-time mistake, not a runtime one.
var displayTmpl = template.Must(template.ParseFS(templateFS, "templates/display.html.tmpl"))

// displayData is what the TV template sees.
//
// The typed fields are load-bearing, not decoration. html/template rewrites a
// bare data: URI to "#ZgotmplZ" and escapes anything it thinks is markup, so
// an untyped CSS string or QR would render as a blank wall with nothing in
// the logs to explain it -- the trap menu/render.go documents.
type displayData struct {
	Title     string
	NameWords []string
	JoinHost  string
	QR        template.HTML
	CSS       template.CSS
	JS        template.JS
	StreamURL template.URL
	PollURL   template.URL
}

// RenderDisplay produces the whole self-contained TV page.
//
// Self-contained matters: this is a kiosk stick on bar wifi, and a page that
// depends on /console/assets/ being reachable is a page that paints a blank
// wall the first time the network hiccups. Fonts are inlined as base64 data
// URIs for the same reason -- a board that falls back to a system font loses
// the size calibration the whole 1920x1080 layout is built around.
func RenderDisplay(baseURL, slug string, game *Game) (string, error) {
	css, err := stylesheet()
	if err != nil {
		return "", err
	}
	js, err := templateFS.ReadFile("templates/display.js")
	if err != nil {
		return "", fmt.Errorf("reading display.js: %w", err)
	}
	join := JoinURL(baseURL, slug, game.Name)
	qr, err := RenderQR(join, 640)
	if err != nil {
		return "", err
	}

	title := game.Title
	if title == "" {
		title = "Trivia"
	}
	base := "/" + slug + "/trivia/" + game.Name

	var buf bytes.Buffer
	err = displayTmpl.ExecuteTemplate(&buf, "display.html.tmpl", displayData{
		Title:     title,
		NameWords: nameWords(game.Name),
		JoinHost:  displayHost(join),
		QR:        qr,
		CSS:       template.CSS(css),
		JS:        template.JS(js), //nolint:gosec // embedded first-party script, no interpolation
		StreamURL: template.URL(base + "/tv/stream"),
		PollURL:   template.URL(base + "/tv/state"),
	})
	if err != nil {
		return "", fmt.Errorf("rendering trivia display: %w", err)
	}
	return buf.String(), nil
}

// nameWords splits brave-otter-lamp into its words, one per line at 180px.
// Three short lines read from the back of a room in a way one long
// hyphenated string does not.
func nameWords(name string) []string {
	parts := strings.Split(name, "-")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.ToUpper(p))
	}
	return out
}

// displayHost strips the scheme from the join URL. Somebody typing it into a
// phone does not type "https://", and the shorter string sets bigger.
func displayHost(join string) string {
	s := strings.TrimPrefix(join, "https://")
	return strings.TrimPrefix(s, "http://")
}

// stylesheet is the embedded CSS with the two @font-face rules prepended.
func stylesheet() (string, error) {
	bungee, err := assetDataURI("assets/bungee.woff2")
	if err != nil {
		return "", err
	}
	exo, err := assetDataURI("assets/exo2.woff2")
	if err != nil {
		return "", err
	}
	base, err := templateFS.ReadFile("templates/display.css")
	if err != nil {
		return "", fmt.Errorf("reading display.css: %w", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "@font-face{font-family:bungee;src:url(%s) format(\"woff2\");"+
		"font-weight:400;font-style:normal;font-display:block}\n", bungee)
	fmt.Fprintf(&b, "@font-face{font-family:exo2;src:url(%s) format(\"woff2\");"+
		"font-weight:400 700;font-style:normal;font-display:block}\n", exo)
	b.Write(base)
	return b.String(), nil
}

func assetDataURI(name string) (string, error) {
	raw, err := assetFS.ReadFile(name)
	if err != nil {
		return "", fmt.Errorf("reading embedded asset %s: %w", name, err)
	}
	return "data:font/woff2;base64," + base64.StdEncoding.EncodeToString(raw), nil
}
