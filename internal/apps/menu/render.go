package menu

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html/template"
	"io/fs"
	"math"
	"strings"
	"sync"
)

//go:embed templates/board.html.tmpl templates/board.css
var templateFS embed.FS

//go:embed assets/bungee.woff2 assets/exo2.woff2 assets/logo.png assets/untappd.svg
var assetFS embed.FS

// boardTmpl is parsed once at package scope; a template that fails to parse is
// a build-time mistake, not a runtime one.
var boardTmpl = template.Must(
	template.ParseFS(templateFS, "templates/board.html.tmpl"),
)

// RenderStamp fingerprints everything the page's appearance is built from --
// the template, the stylesheet, the fonts and the graphics -- so it can ride
// along in the board's version stamp.
//
// Without it a deploy reaches no screen already on the wall. The version a
// board polls is derived from the tap list alone, which is right for the tap
// list and wrong for everything else: a taproom whose menu is quiet for a week
// keeps rendering the CSS it booted with, and the first Untappd edit after a
// deploy swaps in markup the old stylesheet has never seen.
var RenderStamp = sync.OnceValue(func() string {
	h := sha256.New()
	for _, fsys := range []fs.FS{templateFS, assetFS} {
		err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			raw, err := fs.ReadFile(fsys, path)
			if err != nil {
				return err
			}
			h.Write([]byte(path))
			h.Write(raw)
			return nil
		})
		if err != nil {
			// Both trees are compiled into the binary; a read that fails here
			// means the embed is broken, and a stamp that silently ignored it
			// would pin every screen to a stylesheet we cannot account for.
			panic(fmt.Errorf("stamping menu render assets: %w", err))
		}
	}
	// Generated at startup rather than embedded, so it has to be folded in by
	// hand or a change to the sky would never reach a board.
	h.Write([]byte(starfieldURI))
	return hex.EncodeToString(h.Sum(nil))[:8]
})

// headerCost weights a section header against a beer row when balancing the
// two columns. A header is shorter than a row but not free, and getting this
// wrong shows up as one column overflowing into the footer.
const headerCost = 0.65

// Section is a run of taps sharing a style, as laid out on the page. A long
// section can continue into the second column, in which case the second piece
// is marked Continued and repeats the heading.
type Section struct {
	Name      string
	Taps      []Tap
	Continued bool
}

// renderData is what the template sees.
type renderData struct {
	Venue   Venue
	Columns [][]Section
	Panels  []panelView
	CSS     template.CSS
	Logo    template.URL
	// Untappd is the app icon, shown in the footer lockup. It is the mark
	// people have on their phone, so it is carried in full colour rather
	// than tinted to the board's palette like the house logo is.
	Untappd template.URL
	// Version is what the page compares against when it polls, so a screen
	// picks up a new tap list without anyone power-cycling the TV.
	Version string
}

// panelView wraps Panel so the poster's data URI can cross into the template
// as a typed URL. html/template rewrites a bare data: URI in src to
// "#ZgotmplZ", which would render as a broken image on the wall with nothing
// in the logs to explain it.
type panelView struct {
	Panel
	ImageURL template.URL
	// PhotoClass is set on a non-poster panel carrying an image, and names a
	// generated rule holding that image's URL. The URL cannot ride in a style
	// attribute: html/template rewrites a data: URI in CSS context to
	// "#ZgotmplZ", so it would silently render no background at all.
	PhotoClass string
}

// Render produces the whole self-contained page for a board.
//
// assets maps an asset key to its data URI; pass nil when a board has no
// images. A panel naming a key that is not in the map renders with no image
// rather than failing the page, and a poster panel omits its <img> entirely
// rather than emitting an empty src that paints a broken-image icon on the
// wall. One missing graphic should cost that panel, not the tap list beside
// it.
func Render(b *Board, assets map[string]string, version string) (string, error) {
	css, err := stylesheet()
	if err != nil {
		return "", err
	}
	logo, err := assetDataURI("assets/logo.png", "image/png")
	if err != nil {
		return "", err
	}
	untappd, err := assetDataURI("assets/untappd.svg", "image/svg+xml")
	if err != nil {
		return "", err
	}

	panels := make([]panelView, len(b.Panels))
	var photoCSS strings.Builder
	for i, p := range b.Panels {
		img := p.Image
		if key, ok := strings.CutPrefix(img, AssetRef); ok {
			img = assets[key]
		}
		v := panelView{Panel: p, ImageURL: template.URL(img)} //nolint:gosec // a data:image/ URI, validated in Panel.validate or built from stored bytes
		// A poster is the panel's content and renders as an <img>; on any
		// other kind the photo is atmosphere behind the text.
		if img != "" && p.Kind != PanelPoster {
			v.PhotoClass = fmt.Sprintf("pnl-photo-%d", i)
			fmt.Fprintf(&photoCSS, ".%s{--photo:url(%s)}\n", v.PhotoClass, img)
		}
		panels[i] = v
	}

	var buf bytes.Buffer
	err = boardTmpl.ExecuteTemplate(&buf, "board.html.tmpl", renderData{
		Venue:   b.Venue,
		Columns: Columns(b.Taps),
		Panels:  panels,
		CSS:     template.CSS(css + photoCSS.String()),
		Version: version,
		Logo:    template.URL(logo),    //nolint:gosec // locally embedded asset, not user input
		Untappd: template.URL(untappd), //nolint:gosec // locally embedded asset, not user input
	})
	if err != nil {
		return "", fmt.Errorf("rendering menu board: %w", err)
	}
	return buf.String(), nil
}

// stylesheet is the embedded CSS with the two @font-face rules prepended.
// The faces are inlined rather than linked because the screen must repaint
// correctly with no network: a board that falls back to a system font loses
// the size calibration the whole layout is built around.
func stylesheet() (string, error) {
	bungee, err := assetDataURI("assets/bungee.woff2", "font/woff2")
	if err != nil {
		return "", err
	}
	exo, err := assetDataURI("assets/exo2.woff2", "font/woff2")
	if err != nil {
		return "", err
	}
	base, err := templateFS.ReadFile("templates/board.css")
	if err != nil {
		return "", fmt.Errorf("reading board.css: %w", err)
	}
	var b strings.Builder
	// The generated sky rides in as a token so board.css can compose it with
	// the washes rather than having a data URI pasted into the file.
	fmt.Fprintf(&b, ":root{--starfield:url(%s)}\n", starfieldURI)
	fmt.Fprintf(&b, "@font-face{font-family:bungee;src:url(%s) format(\"woff2\");"+
		"font-weight:400;font-style:normal;font-display:block}\n", bungee)
	fmt.Fprintf(&b, "@font-face{font-family:exo2;src:url(%s) format(\"woff2\");"+
		"font-weight:400 700;font-style:normal;font-display:block}\n", exo)
	b.Write(base)
	return b.String(), nil
}

func assetDataURI(name, mime string) (string, error) {
	raw, err := assetFS.ReadFile(name)
	if err != nil {
		return "", fmt.Errorf("reading embedded asset %s: %w", name, err)
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw), nil
}

// Columns groups taps into sections in the order given, then divides them
// across the page's two columns as evenly as it can.
//
// Sections may be split. Keeping every section whole reads better in the
// abstract, but the tap list comes from Untappd and its section sizes are not
// ours to choose: the real board divides 6 against 10 at the best whole-
// section cut, which leaves a third of one column empty. A printed menu
// solves this the same way — carry the heading over and continue the list.
func Columns(taps []Tap) [][]Section {
	var sections []Section
	for _, t := range taps {
		if n := len(sections); n == 0 || sections[n-1].Name != t.Section {
			sections = append(sections, Section{Name: t.Section})
		}
		last := &sections[len(sections)-1]
		last.Taps = append(last.Taps, t)
	}
	return balanceColumns(sections)
}

// balanceColumns picks the cut that leaves the two columns closest in height.
//
// Every position is tried rather than cutting at the first crossing of the
// halfway mark, because the two candidates either side of that mark are not
// symmetric: splitting a section adds a repeated heading to the right column,
// so the better cut is often one row past where a running total says to stop.
// Enumerating is exact, and at seventeen taps it is free.
func balanceColumns(sections []Section) [][]Section {
	if len(sections) == 0 {
		return [][]Section{nil, nil}
	}

	// Flatten to (section index, row index) so a cut can land mid-section.
	type pos struct{ sec, row int }
	var rows []pos
	for si, sec := range sections {
		for ri := range sec.Taps {
			rows = append(rows, pos{si, ri})
		}
	}

	best, bestDiff := 0, math.Inf(1)
	for cut := 1; cut < len(rows); cut++ {
		left, right := splitAt(sections, rows[cut].sec, rows[cut].row)
		// A heading with nothing under it reads as a section whose beers have
		// all gone, so those cuts are not candidates at all.
		if hasEmptySection(left) || hasEmptySection(right) {
			continue
		}
		diff := math.Abs(columnHeight(left) - columnHeight(right))
		if diff < bestDiff {
			best, bestDiff = cut, diff
		}
	}
	if math.IsInf(bestDiff, 1) {
		return [][]Section{sections, nil} // one row: nothing to balance
	}
	left, right := splitAt(sections, rows[best].sec, rows[best].row)
	return [][]Section{left, right}
}

// splitAt divides the sections so that row `row` of section `sec` is the first
// entry of the right column, repeating the heading when a section straddles.
func splitAt(sections []Section, sec, row int) (left, right []Section) {
	for i, s := range sections {
		switch {
		case i < sec:
			left = append(left, s)
		case i > sec:
			right = append(right, s)
		case row == 0:
			right = append(right, s)
		default:
			left = append(left, Section{Name: s.Name, Taps: s.Taps[:row]})
			right = append(right, Section{Name: s.Name, Taps: s.Taps[row:], Continued: true})
		}
	}
	return left, right
}

// columnHeight is the cost model the balance is measured against: a heading
// takes real vertical space, just less than a beer.
func columnHeight(secs []Section) float64 {
	var h float64
	for _, s := range secs {
		h += headerCost + float64(len(s.Taps))
	}
	return h
}

func hasEmptySection(secs []Section) bool {
	for _, s := range secs {
		if len(s.Taps) == 0 {
			return true
		}
	}
	return false
}
