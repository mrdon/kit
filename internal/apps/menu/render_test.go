package menu

import (
	"os"
	"strings"
	"testing"
)

func tap(section, name, size string) Tap {
	return Tap{Section: section, Name: name, Style: "Style", ABV: "5.0%", Price: "7", Size: size}
}

func taps(section string, n int) []Tap {
	out := make([]Tap, n)
	for i := range out {
		out[i] = tap(section, "Beer", DefaultPour)
	}
	return out
}

func TestSplitSectionsBalancesColumns(t *testing.T) {
	// The real board's shape: 1 + 3 + 5 on the left, 3 + 2 + 3 on the right.
	var all []Tap
	for _, s := range []struct {
		name string
		n    int
	}{
		{"Lagers", 1}, {"Pub Ales", 3}, {"Pale Ales & IPAs", 5},
		{"Belgian", 3}, {"Stouts & Porters", 2}, {"Specialty", 3},
	} {
		all = append(all, taps(s.name, s.n)...)
	}

	cols := Columns(all)
	if len(cols) != 2 {
		t.Fatalf("want 2 columns, got %d", len(cols))
	}
	count := func(secs []Section) int {
		n := 0
		for _, s := range secs {
			n += len(s.Taps)
		}
		return n
	}
	left, right := count(cols[0]), count(cols[1])
	if left+right != 17 {
		t.Fatalf("lost taps in the split: %d + %d", left, right)
	}
	// A column running more than two beers longer than the other overflows
	// the grid at 46px names, which is the bug this split exists to prevent.
	if diff := left - right; diff > 2 || diff < -2 {
		t.Errorf("columns unbalanced: left=%d right=%d", left, right)
	}
}

func TestSplitSectionsNeverEmptiesRightColumn(t *testing.T) {
	// One enormous section must not take the whole page and leave column two
	// blank; the guard clamps the cut so the right column always gets one.
	all := taps("Everything", 12)
	all = append(all, taps("Tail", 1)...)
	cols := Columns(all)
	if len(cols[1]) == 0 {
		t.Fatal("right column is empty")
	}
}

func TestSectionsKeepTapOrder(t *testing.T) {
	all := []Tap{
		tap("Lagers", "First", DefaultPour),
		tap("Lagers", "Second", DefaultPour),
		tap("Stouts", "Third", "9oz"),
	}
	cols := Columns(all)
	var names []string
	for _, col := range cols {
		for _, s := range col {
			for _, tp := range s.Taps {
				names = append(names, tp.Name)
			}
		}
	}
	got := strings.Join(names, ",")
	if got != "First,Second,Third" {
		t.Errorf("tap order changed: %s", got)
	}
}

func TestSizeLabelHidesTheHousePour(t *testing.T) {
	if got := tap("s", "n", DefaultPour).SizeLabel(); got != "" {
		t.Errorf("house pour should carry no label, got %q", got)
	}
	if got := tap("s", "n", "9oz").SizeLabel(); got != "9 oz" {
		t.Errorf("want %q, got %q", "9 oz", got)
	}
	if got := tap("s", "n", "").SizeLabel(); got != "" {
		t.Errorf("missing size should carry no label, got %q", got)
	}
}

func TestRenderIsSelfContained(t *testing.T) {
	b := &Board{
		Venue: Venue{Wordmark: "Gravity Brewing", Footer: []string{"Pours are 16oz unless marked"}},
		Taps: []Tap{
			{Section: "Lagers", Name: "Cerveza Espacial", Style: "American Lager", ABV: "5.1%", Price: "6.50", Size: "16oz"},
			{Section: "Specialty", Name: "Viener Vino", Style: "Barrel-Aged Barleywine", ABV: "15%", Price: "9", Size: "9oz"},
		},
		Panels: []Panel{
			{Kind: PanelCTA, Label: "Book the space", Headline: "Your party, here.", Body: "Birthdays.", Contact: []string{"info@example.com"}},
		},
	}
	html, err := Render(b)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	// The board hangs on a wall with no one watching it, so nothing on the
	// page may depend on a network fetch succeeding.
	for _, bad := range []string{"https://fonts.", "http://", "<link"} {
		if strings.Contains(html, bad) {
			t.Errorf("page references external resource %q", bad)
		}
	}
	for _, want := range []string{
		// Title case in the markup; the caps are a CSS text-transform, so
		// the payload stays reusable for a website menu.
		"Cerveza Espacial", "Viener Vino", "9 oz", "6.50",
		"@font-face", "data:font/woff2;base64,", "data:image/png;base64,",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered page missing %q", want)
		}
	}
	// The 16oz row must not print a size; the 9oz one must.
	if strings.Contains(html, "16 oz") {
		t.Error("house pour was labelled")
	}
}

func TestRenderEscapesContent(t *testing.T) {
	b := &Board{
		Venue: Venue{Wordmark: "Gravity", Footer: []string{"Also in 4oz & 9oz"}},
		Taps:  []Tap{{Section: "Sours", Name: "<script>alert(1)</script>", Style: "Sour & Tart", ABV: "6%", Price: "7", Size: "16oz"}},
	}
	html, err := Render(b)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(html, "<script>alert(1)</script>") {
		t.Error("tap name was not escaped")
	}
	// An ampersand typed as an ampersand should render as one, not as
	// "&amp;amp;" — payloads are plain text, never pre-escaped HTML.
	if strings.Contains(html, "&amp;amp;") {
		t.Error("ampersand double-escaped")
	}
	if !strings.Contains(html, "Sour &amp; Tart") {
		t.Error("ampersand not escaped once")
	}
}

func TestRenderPosterKeepsItsDataURI(t *testing.T) {
	const img = "data:image/png;base64,iVBORw0KGgo="
	b := &Board{
		Venue:  Venue{Wordmark: "Gravity"},
		Taps:   []Tap{tap("Lagers", "Beer", DefaultPour)},
		Panels: []Panel{{Kind: PanelPoster, Label: "Don't miss", Image: img, Alt: "Poster"}},
	}
	html, err := Render(b)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// html/template rewrites an untyped data: URI in src to "#ZgotmplZ",
	// which would show as a broken image on the wall and nothing in the log.
	if strings.Contains(html, "ZgotmplZ") {
		t.Fatal("poster data URI was sanitised away")
	}
	if !strings.Contains(html, img) {
		t.Error("poster image missing from the page")
	}
}

// TestRealBoardPayload runs the actual taproom board -- the same document the
// authoring repo pushes -- through parse and render. It is the one test that
// would catch a schema drift between what gets authored and what this app
// accepts, which is otherwise only discovered on a wall.
func TestRealBoardPayload(t *testing.T) {
	raw, err := os.ReadFile("testdata/board.json")
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	b, err := ParseBoard(raw)
	if err != nil {
		t.Fatalf("ParseBoard: %v", err)
	}
	if len(b.Taps) != 17 {
		t.Errorf("want 17 taps, got %d", len(b.Taps))
	}

	cols := Columns(b.Taps)
	names := func(secs []Section) []string {
		var out []string
		for _, s := range secs {
			out = append(out, s.Name)
		}
		return out
	}
	// The section order is chosen so this split lands 9 against 8; if it
	// drifts the board overflows into the footer at full type size.
	if got := strings.Join(names(cols[0]), "|"); got != "Lagers|Pub Ales|Pale Ales & IPAs" {
		t.Errorf("left column sections changed: %s", got)
	}

	html, err := Render(b)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{"Cerveza Espacial", "Mango Peach Quasar", "Wavelength", "This week"} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered board missing %q", want)
		}
	}
	if out := os.Getenv("MENU_BOARD_OUT"); out != "" {
		if err := os.WriteFile(out, []byte(html), 0o600); err != nil {
			t.Fatalf("writing %s: %v", out, err)
		}
		t.Logf("wrote %s (%d KB)", out, len(html)/1024)
	}
}

func TestDefaultBoardHasNoKeyInItsPath(t *testing.T) {
	// The workspace menu should be one obvious address, not one with
	// "default" stuck on the end.
	if got := PublicPath("gravity", DefaultKey); got != "/gravity/menu" {
		t.Errorf("default path = %q, want /gravity/menu", got)
	}
	if got := PublicPath("gravity", ""); got != "/gravity/menu" {
		t.Errorf("empty key path = %q, want /gravity/menu", got)
	}
	if got := PublicPath("gravity", "patio"); got != "/gravity/menu/patio" {
		t.Errorf("keyed path = %q, want /gravity/menu/patio", got)
	}
}

func TestBlankKeyMeansTheWorkspaceMenu(t *testing.T) {
	// Slugifying the name here is how you end up with a fresh URL every time
	// someone rewords the heading.
	in := BoardInput{Name: "Taproom wall", Payload: []byte(`{"taps":[{"section":"s","name":"n"}]}`)}
	in.normalize()
	if in.Key != DefaultKey {
		t.Errorf("blank key normalised to %q, want %q", in.Key, DefaultKey)
	}
}

func TestStarfieldIsStableAndEmbedded(t *testing.T) {
	// A wall display that reloads should come back to the same sky, so the
	// field is generated once from a fixed seed rather than per render.
	if buildStarfield() != starfieldURI {
		t.Error("starfield is not deterministic")
	}
	b := &Board{Venue: Venue{Wordmark: "G"}, Taps: []Tap{tap("Lagers", "Beer", DefaultPour)}}
	html, err := Render(b)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(html, "--starfield:url(data:image/svg+xml;base64,") {
		t.Error("starfield missing from the rendered page")
	}
}
