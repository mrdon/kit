package menu

import (
	"os"
	"strings"
	"testing"
)

func tap(section, name, size string) Tap {
	return Tap{Section: section, Name: name, Style: "Style", ABV: "5.0%", Price: "7", Size: size}
}

func countTaps(secs []Section) int {
	n := 0
	for _, s := range secs {
		n += len(s.Taps)
	}
	return n
}

func sectionsFrom(spec [][2]any) []Tap {
	var out []Tap
	for _, s := range spec {
		name, n := s[0].(string), s[1].(int)
		for range n {
			out = append(out, tap(name, "Beer", DefaultPour))
		}
	}
	return out
}

// The real board's shape. Kept whole, the best contiguous cut is 6 against
// 10, which leaves a third of a column empty; splitting the long section gets
// it to 8/8.
func TestColumnsBalanceTheRealBoard(t *testing.T) {
	all := sectionsFrom([][2]any{
		{"Pub Ales", 3}, {"Belgian Styles", 3}, {"Pale Ales & IPAs", 5},
		{"Stouts & Porters", 2}, {"Specialty", 3},
	})
	cols := Columns(all)
	left, right := countTaps(cols[0]), countTaps(cols[1])
	if left+right != 16 {
		t.Fatalf("lost taps: %d + %d", left, right)
	}
	if diff := left - right; diff > 1 || diff < -1 {
		t.Errorf("columns unbalanced: left=%d right=%d", left, right)
	}
}

// A section carried over repeats its heading, marked as a continuation so it
// does not read as a second section with the same name.
func TestSplitSectionRepeatsItsHeading(t *testing.T) {
	all := sectionsFrom([][2]any{{"Lagers", 2}, {"Pale Ales & IPAs", 10}})
	cols := Columns(all)

	var carried *Section
	for i := range cols[1] {
		if cols[1][i].Continued {
			carried = &cols[1][i]
		}
	}
	if carried == nil {
		t.Fatal("a 10-tap section should have been carried into column two")
	}
	if carried.Name != "Pale Ales & IPAs" {
		t.Errorf("continuation heading = %q, want the original name", carried.Name)
	}
	if left, right := countTaps(cols[0]), countTaps(cols[1]); left-right > 1 || right-left > 1 {
		t.Errorf("unbalanced after split: left=%d right=%d", left, right)
	}
}

// A heading with nothing under it at the foot of a column reads as a section
// whose beers have all gone.
func TestNoStrandedHeading(t *testing.T) {
	for _, spec := range [][][2]any{
		{{"A", 4}, {"B", 1}, {"C", 4}},
		{{"A", 1}, {"B", 9}},
		{{"A", 8}, {"B", 8}},
		{{"Only", 5}},
	} {
		for _, col := range Columns(sectionsFrom(spec)) {
			for _, sec := range col {
				if len(sec.Taps) == 0 {
					t.Errorf("%v produced an empty section %q", spec, sec.Name)
				}
			}
		}
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
	html, err := Render(b, nil, "v1")
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

// The lockup replaces the "Subscribe to us on Untappd" line the Untappd-hosted
// board carried in the same corner. It is what tells a regular the taproom
// still runs on Untappd now that the screen is served from here, so it is
// chrome on every board rather than a panel a venue can forget to add -- and
// like everything else on the page it has to survive with no network.
func TestFooterCarriesTheUntappdLockup(t *testing.T) {
	b := &Board{
		Venue: Venue{Wordmark: "Gravity Brewing", Footer: []string{"Pours are 16oz unless marked"}},
		Taps:  []Tap{{Section: "Lagers", Name: "Cerveza Espacial", Price: "6.50"}},
	}
	html, err := Render(b, nil, "v1")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{
		"Check in on <span>Untappd</span>",
		"data:image/svg+xml;base64,",
		"foot-untappd",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("footer lockup missing %q", want)
		}
	}
	// A board with no venue footer notes at all still gets the lockup, and
	// the notes still get their own box so the two do not collapse together.
	if !strings.Contains(html, `<div class="foot-notes">`) {
		t.Error("footer notes lost their own container")
	}
}

func TestRenderEscapesContent(t *testing.T) {
	b := &Board{
		Venue: Venue{Wordmark: "Gravity", Footer: []string{"Also in 4oz & 9oz"}},
		Taps:  []Tap{{Section: "Sours", Name: "<script>alert(1)</script>", Style: "Sour & Tart", ABV: "6%", Price: "7", Size: "16oz"}},
	}
	html, err := Render(b, nil, "v1")
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
	html, err := Render(b, nil, "v1")
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
	// Both columns should carry close to half the taps.
	if left, right := countTaps(cols[0]), countTaps(cols[1]); left-right > 1 || right-left > 1 {
		t.Errorf("unbalanced: left=%d right=%d (%v | %v)", left, right, names(cols[0]), names(cols[1]))
	}

	html, err := Render(b, nil, "v1")
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

func TestPublicPathHasNoKey(t *testing.T) {
	// One menu per workspace, so one obvious address.
	if got := PublicPath("gravity"); got != "/gravity/menu" {
		t.Errorf("path = %q, want /gravity/menu", got)
	}
}

func TestStarfieldIsStableAndEmbedded(t *testing.T) {
	// A wall display that reloads should come back to the same sky, so the
	// field is generated once from a fixed seed rather than per render.
	if buildStarfield() != starfieldURI {
		t.Error("starfield is not deterministic")
	}
	b := &Board{Venue: Venue{Wordmark: "G"}, Taps: []Tap{tap("Lagers", "Beer", DefaultPour)}}
	html, err := Render(b, nil, "v1")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(html, "--starfield:url(data:image/svg+xml;base64,") {
		t.Error("starfield missing from the rendered page")
	}
}
