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

//go:embed assets/bungee.woff2 assets/exo2.woff2 assets/sample-questions.csv assets/wits-wagers-questions.csv
var assetFS embed.FS

// SampleCSV is a starter question bank: 62 questions across eleven topics,
// every one of them numeric and several carrying two topics, which is enough
// to build the default 5x2 board several times over without repeating.
//
// It exists because "upload a CSV" is a worse instruction than "here is one
// that works" — a host with an empty bank and a blank spreadsheet has to
// guess the column names, and the first thing they learn otherwise is an
// error message.
func SampleCSV() ([]byte, error) {
	return assetFS.ReadFile("assets/sample-questions.csv")
}

// BuiltinPack is a question set Kit ships. Loading one creates an ordinary
// dataset — after that it is a host's to rename, re-upload over or delete,
// and nothing in the code treats it specially.
type BuiltinPack struct {
	Key   string `json:"key"`
	Name  string `json:"name"`
	Notes string `json:"notes"`
	file  string
}

// BuiltinPacks is the shipped set. Adding one is a line here plus a CSV in
// assets/ — there is no registry to update and no code that branches on which
// pack a dataset came from.
var BuiltinPacks = []BuiltinPack{
	{
		Key:   "starter",
		Name:  "Kit starter pack",
		Notes: "62 general-knowledge questions across eleven topics. A good first night.",
		file:  "assets/sample-questions.csv",
	},
	{
		Key:  "wits",
		Name: "Wits & Wagers questions",
		// Worth saying out loud: a chunk of these pin their answer to a year
		// ("as of 2007"). The question text carries the year, so they play
		// fine -- but somebody skimming the bank should know why the numbers
		// look dated rather than assuming they are wrong.
		Notes: "237 questions from the Wits & Wagers sets. Several state the year they were true.",
		file:  "assets/wits-wagers-questions.csv",
	},
}

// BuiltinPackCSV returns a shipped pack's sheet.
func BuiltinPackCSV(key string) ([]byte, BuiltinPack, error) {
	for _, p := range BuiltinPacks {
		if p.Key == key {
			body, err := assetFS.ReadFile(p.file)
			return body, p, err
		}
	}
	return nil, BuiltinPack{}, ErrNotFound
}

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
	Title string
	// Heading is what the corner of every screen shows: the night's name as
	// the host typed it.
	Heading string
	// JoinWords is the night's name broken across lines for the join screen's
	// hero. It is the TITLE, never the URL slug -- see nameWords.
	JoinWords []string
	JoinHost  string
	QR        template.HTML
	CSS       template.CSS
	JS        template.JS
	StreamURL template.URL
	PollURL   template.URL
	GameName  string
	// Version stamps what this rendered page depends on, and VersionURL is
	// where the screen polls for it — the menu board's pattern. A page served
	// from the stable latest-game address polls the workspace-level stamp, so
	// a newer game reloads it; a page for one specific game polls that game's
	// own, so a renamed night reloads it without ever switching games.
	Version    string
	VersionURL template.URL
	// Rules are shown on the lobby screen. A stranger who has just walked in
	// should be able to work the game out from the wall.
	Rules []string
}

// RenderDisplay produces the whole self-contained TV page.
//
// Self-contained matters: this is a kiosk stick on bar wifi, and a page that
// depends on /console/assets/ being reachable is a page that paints a blank
// wall the first time the network hiccups. Fonts are inlined as base64 data
// URIs for the same reason -- a board that falls back to a system font loses
// the size calibration the whole 1920x1080 layout is built around.
func RenderDisplay(baseURL, slug string, game *Game, followLatest bool) (string, error) {
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
	// NEVER the slug. It is a URL token: "vague-jaguar-coin" in 180px letters
	// on a wall means nothing to anybody in the room, and it was sitting
	// above the actual name of the night in small grey type. Games are
	// created with a title and the migration backfilled the ones that were
	// not, so this fallback is for a row somebody blanked by hand.
	heading := game.Title
	if heading == "" {
		heading = "Quiz night"
	}
	base := "/" + slug + "/trivia/" + game.Name
	versionURL := base + "/tv.version"
	if followLatest {
		versionURL = "/" + slug + "/trivia/tv.version"
	}

	var buf bytes.Buffer
	err = displayTmpl.ExecuteTemplate(&buf, "display.html.tmpl", displayData{
		Title:      title,
		Heading:    heading,
		JoinWords:  nameWords(heading),
		JoinHost:   displayHost(join),
		QR:         qr,
		CSS:        template.CSS(css),
		JS:         template.JS(js), //nolint:gosec // embedded first-party script, no interpolation
		StreamURL:  template.URL(base + "/tv/stream"),
		PollURL:    template.URL(base + "/tv/state"),
		GameName:   game.Name,
		Rules:      Rules(game.FinalWager),
		Version:    displayVersion(game),
		VersionURL: template.URL(versionURL),
	})
	if err != nil {
		return "", fmt.Errorf("rendering trivia display: %w", err)
	}
	return buf.String(), nil
}

// nameWords breaks the night's name across lines for the join screen's hero,
// one word per line at 180px, because short lines read from the back of a
// room in a way one long string does not.
//
// IT IS GIVEN THE TITLE, NOT THE URL SLUG. The slug was there originally so
// somebody could type it into a phone -- but there is no name-entry screen;
// players scan the QR or type the URL, and the URL is printed underneath in
// full. So the biggest text on the wall was a machine-readable token nobody
// needed, sitting above the name of the night in small grey letters.
func nameWords(s string) []string {
	fields := strings.Fields(strings.ReplaceAll(s, "-", " "))
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, strings.ToUpper(f))
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
