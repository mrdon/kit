package hoppers

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"regexp"
	"strings"

	"github.com/mrdon/kit/internal/livegame"
	"github.com/mrdon/kit/internal/web/qrcode"
	consoleassets "github.com/mrdon/kit/web/console"
)

//go:embed templates/display.html.tmpl templates/display.css templates/tv/*.js
var templateFS embed.FS

var displayTmpl = template.Must(template.ParseFS(templateFS, "templates/display.html.tmpl"))

// sharedScripts are the files the TV shares with the phone, in dependency
// order. They are ES modules in web/console/src/hoppers/, flattened here.
var sharedScripts = []string{"consts.js", "art/frog.js", "art/looks.js", "art/fx.js", "art/scenery.js"}

// tvScripts are the TV's own files. core first (the shared vars), boot last
// (the statements that start it).
var tvScripts = []string{"core.js", "scene.js", "race.js", "screens.js", "boot.js"}

// displayData is what the TV template fills in. Typed template values on
// purpose: html/template rewrites an untyped data: URI or markup to a blank.
type displayData struct {
	Title      string
	QR         template.HTML
	JoinHost   string
	CSS        template.CSS
	JS         template.JS
	StreamURL  template.URL
	PollURL    template.URL
	Version    string
	VersionURL template.URL
}

// RenderDisplay produces the whole self-contained TV page: fonts, styles and
// scripts inlined, so a kiosk stick on bar wifi paints from one response.
func RenderDisplay(baseURL, slug string, game *Game, followLatest bool) ([]byte, error) {
	join := ShortJoinURL(baseURL, game.JoinCode)
	qr, err := qrcode.RenderSVG(join, 640, "Join code")
	if err != nil {
		return nil, fmt.Errorf("drawing the hoppers join code: %w", err)
	}
	css, err := displayCSS()
	if err != nil {
		return nil, err
	}
	js, err := displayJS()
	if err != nil {
		return nil, err
	}
	title := game.Title
	if title == "" {
		title = "Hoppers"
	}
	base := JoinURL(slug, game.Name)
	versionURL := base + "/tv.version"
	if followLatest {
		versionURL = "/" + slug + "/" + AppName + "/tv.version"
	}
	data := displayData{
		Title:      title,
		QR:         qr,
		JoinHost:   livegame.DisplayHost(join),
		CSS:        template.CSS(css), //nolint:gosec // embedded first-party CSS, no interpolation
		JS:         template.JS(js),   //nolint:gosec // embedded first-party script, no interpolation
		StreamURL:  template.URL(base + "/tv/stream"),
		PollURL:    template.URL(base + "/tv/state"),
		Version:    displayVersion(game),
		VersionURL: template.URL(versionURL), //nolint:gosec // built from the slug and a validated name
	}
	var buf bytes.Buffer
	if err := displayTmpl.ExecuteTemplate(&buf, "display.html.tmpl", data); err != nil {
		return nil, fmt.Errorf("rendering hoppers display: %w", err)
	}
	return buf.Bytes(), nil
}

// displayCSS is the fonts, the shared theme, then the TV's own sheet.
func displayCSS() (string, error) {
	fonts, err := livegame.FontFaceCSS()
	if err != nil {
		return "", err
	}
	theme, err := consoleassets.HoppersShared("theme.css")
	if err != nil {
		return "", err
	}
	own, err := templateFS.ReadFile("templates/display.css")
	if err != nil {
		return "", fmt.Errorf("reading hoppers display.css: %w", err)
	}
	return fonts + string(theme) + "\n" + string(own), nil
}

// displayJS is KitLive, then one IIFE holding the flattened shared modules
// and the TV's files, so every file sees every other's declarations.
func displayJS() (string, error) {
	var b strings.Builder
	b.Write(livegame.TVClientJS())
	b.WriteString("\n(function () {\n'use strict';\n")
	for _, name := range sharedScripts {
		src, err := consoleassets.HoppersShared(name)
		if err != nil {
			return "", err
		}
		flat, err := flattenModule(string(src))
		if err != nil {
			return "", fmt.Errorf("flattening %s: %w", name, err)
		}
		b.WriteString(flat)
		b.WriteString("\n")
	}
	for _, name := range tvScripts {
		src, err := templateFS.ReadFile("templates/tv/" + name)
		if err != nil {
			return "", fmt.Errorf("reading hoppers tv/%s: %w", name, err)
		}
		b.Write(src)
		b.WriteString("\n")
	}
	b.WriteString("})();\n")
	return b.String(), nil
}

var (
	importLine = regexp.MustCompile(`(?m)^import [^\n]*;\n`)
	exportDecl = regexp.MustCompile(`(?m)^export (function|const|let) `)
	exportAny  = regexp.MustCompile(`(?m)^\s*(export|import)\b`)
)

// flattenModule turns one of the shared ES modules into plain script for the
// TV's IIFE: its imports go (the files are concatenated in dependency order
// instead) and "export " comes off each top-level declaration. Anything else
// module-shaped is refused rather than passed through, because a stray
// `export` is a syntax error that blanks the whole wall.
func flattenModule(src string) (string, error) {
	out := importLine.ReplaceAllString(src, "")
	out = exportDecl.ReplaceAllString(out, "$1 ")
	if loc := exportAny.FindStringIndex(out); loc != nil {
		line, _, _ := strings.Cut(out[loc[0]:], "\n")
		return "", fmt.Errorf("unsupported module syntax: %q", strings.TrimSpace(line))
	}
	return out, nil
}
