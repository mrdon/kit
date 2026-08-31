package menu

import (
	"bytes"
	"embed"
	"encoding/base64"
	"fmt"
	"html/template"
	"strings"
)

//go:embed templates/board.html.tmpl templates/board.css
var templateFS embed.FS

//go:embed assets/bungee.woff2 assets/exo2.woff2 assets/logo.png
var assetFS embed.FS

// boardTmpl is parsed once at package scope; a template that fails to parse is
// a build-time mistake, not a runtime one.
var boardTmpl = template.Must(
	template.ParseFS(templateFS, "templates/board.html.tmpl"),
)

// headerCost weights a section header against a beer row when balancing the
// two columns. A header is shorter than a row but not free, and getting this
// wrong shows up as one column overflowing into the footer.
const headerCost = 0.65

// Section is a run of taps sharing a style, as laid out on the page.
type Section struct {
	Name string
	Taps []Tap
}

// renderData is what the template sees.
type renderData struct {
	Venue   Venue
	Columns [][]Section
	Panels  []panelView
	CSS     template.CSS
	Logo    template.URL
}

// panelView wraps Panel so the poster's data URI can cross into the template
// as a typed URL. html/template rewrites a bare data: URI in src to
// "#ZgotmplZ", which would render as a broken image on the wall with nothing
// in the logs to explain it.
type panelView struct {
	Panel
	ImageURL template.URL
}

// Render produces the whole self-contained page for a board.
func Render(b *Board) (string, error) {
	css, err := stylesheet()
	if err != nil {
		return "", err
	}
	logo, err := assetDataURI("assets/logo.png", "image/png")
	if err != nil {
		return "", err
	}

	panels := make([]panelView, len(b.Panels))
	for i, p := range b.Panels {
		panels[i] = panelView{Panel: p, ImageURL: template.URL(p.Image)} //nolint:gosec // validated as a data:image/ URI in Panel.validate
	}

	var buf bytes.Buffer
	err = boardTmpl.ExecuteTemplate(&buf, "board.html.tmpl", renderData{
		Venue:   b.Venue,
		Columns: Columns(b.Taps),
		Panels:  panels,
		CSS:     template.CSS(css),
		Logo:    template.URL(logo), //nolint:gosec // locally embedded asset, not user input
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

// Columns groups taps into sections in the order given, then splits the
// sections across the page's two columns.
func Columns(taps []Tap) [][]Section {
	var sections []Section
	for _, t := range taps {
		if n := len(sections); n == 0 || sections[n-1].Name != t.Section {
			sections = append(sections, Section{Name: t.Section})
		}
		last := &sections[len(sections)-1]
		last.Taps = append(last.Taps, t)
	}
	left, right := splitSections(sections)
	return [][]Section{left, right}
}

// splitSections cuts the section list into two columns of roughly equal
// height. A section is never split across columns: a style group broken in
// half is worse than a column that runs slightly short, because the header
// only sits over one of the two pieces.
func splitSections(sections []Section) (left, right []Section) {
	if len(sections) < 2 {
		return sections, nil
	}
	cost := func(s Section) float64 { return float64(len(s.Taps)) + headerCost }
	var total float64
	for _, s := range sections {
		total += cost(s)
	}
	half := total / 2

	var running float64
	cut := 0
	for i, s := range sections {
		// Take the section only if its midpoint still lands in the top half,
		// and always leave at least one for the right column.
		if i > 0 && running+cost(s)/2 > half {
			break
		}
		running += cost(s)
		cut = i + 1
	}
	if cut >= len(sections) {
		cut = len(sections) - 1
	}
	return sections[:cut], sections[cut:]
}
