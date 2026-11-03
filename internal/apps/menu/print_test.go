package menu

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/go-pdf/fpdf"
)

func TestParseBeersKeepsEveryPour(t *testing.T) {
	raw, err := os.ReadFile("testdata/untappd_board.html")
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	rows := ParseBeers(string(raw))
	if len(rows) < minPlausibleTaps {
		t.Fatalf("parsed %d rows, want at least %d", len(rows), minPlausibleTaps)
	}

	var newtonian *Beer
	for i := range rows {
		if rows[i].Name == "Newtonian" {
			newtonian = &rows[i]
		}
	}
	if newtonian == nil {
		t.Fatal("Newtonian not found in the fixture")
	}
	// The board parse collapses this to one price; the print parse must not.
	if len(newtonian.Pours) != 4 {
		t.Fatalf("Newtonian has %d pours, want 4: %+v", len(newtonian.Pours), newtonian.Pours)
	}
	if pour, ok := newtonian.Headline(); !ok || pour.Price != "6.50" || pour.Size != "16oz" {
		t.Errorf("Headline() = %q/%q (%v), want 6.50/16oz", pour.Price, pour.Size, ok)
	}
	if newtonian.Section != "Pub Ales" {
		t.Errorf("Section = %q, want Pub Ales", newtonian.Section)
	}
}

// A beer whose price has been cleared upstream is still a beer. The board
// parse drops it; the printed menu keeps the row and prints a dash, because a
// customer can see it pouring.
func TestParseBeersKeepsPricelessRows(t *testing.T) {
	raw, err := os.ReadFile("testdata/untappd_board.html")
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	rows := ParseBeers(string(raw))
	found := false
	for _, r := range rows {
		if r.Name == "Cerveza Espacial" {
			found = true
			if len(r.Pours) != 0 {
				t.Errorf("Cerveza Espacial has pours %+v, fixture has none", r.Pours)
			}
		}
	}
	if !found {
		t.Error("Cerveza Espacial was dropped; a priceless beer should still print")
	}
}

// The odd pour is the whole reason the large column is not simply "16oz".
func TestHeadlinePicksBiggestDraft(t *testing.T) {
	for _, tc := range []struct {
		name       string
		pours      []Pour
		wantPrice  string
		wantSize   string
		wantDrafty bool
	}{
		{
			name: "pint beats growler",
			pours: []Pour{
				{Size: "16oz", Label: "16oz Draft", Price: "6.50"},
				{Size: "9oz", Label: "9oz Draft", Price: "4"},
				{Size: "64oz", Label: "64oz Growler", Price: "18"},
			},
			wantPrice: "6.50", wantSize: "16oz", wantDrafty: true,
		},
		{
			name: "imperial pours twelve",
			pours: []Pour{
				{Size: "12oz", Label: "12oz Draft", Price: "8"},
				{Size: "9oz", Label: "9oz Draft", Price: "6.50"},
			},
			wantPrice: "8", wantSize: "12oz", wantDrafty: true,
		},
		{
			// Poured small only. The column must still show a price, marked
			// with its size -- a blank here is the bug that dropping the
			// second column introduced.
			name: "barleywine pours nine only",
			pours: []Pour{
				{Size: "9oz", Label: "9oz Draft", Price: "9"},
				{Size: "4oz", Label: "4oz Draft", Price: "5"},
			},
			wantPrice: "9", wantSize: "9oz", wantDrafty: true,
		},
		{
			// A growler is takeaway and must never take the column, even when
			// it is the only thing bigger than a taster.
			name: "a growler never takes the column",
			pours: []Pour{
				{Size: "4oz", Label: "4oz Draft", Price: "3"},
				{Size: "64oz", Label: "64oz Growler", Price: "18"},
			},
			wantPrice: "3", wantSize: "4oz", wantDrafty: true,
		},
		{
			// Packaged-only, like the wall board: no draft price, so the can
			// is what gets quoted rather than nothing.
			name: "a can is quoted when there is no pour",
			pours: []Pour{
				{Size: "12oz", Label: "12oz Can", Price: "6"},
			},
			wantPrice: "6", wantSize: "12oz", wantDrafty: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := Beer{Pours: tc.pours}
			pour, _ := r.Headline()
			if pour.Price != tc.wantPrice || pour.Size != tc.wantSize {
				t.Errorf("Headline() = %q/%q, want %q/%q",
					pour.Price, pour.Size, tc.wantPrice, tc.wantSize)
			}
			if got := r.HasDraft(); got != tc.wantDrafty {
				t.Errorf("HasDraft() = %v, want %v", got, tc.wantDrafty)
			}
		})
	}
}

func TestTidyStyle(t *testing.T) {
	for in, want := range map[string]string{
		"Lager - American":          "American Lager",
		"IPA - West Coast":          "West Coast IPA",
		"Porter - Coffee":           "Coffee Porter",
		"Stout - Russian Imperial":  "Russian Imperial Stout",
		"Scotch Ale":                "Scotch Ale",
		"Belgian Strong Golden Ale": "Belgian Strong Golden Ale",
		"Amber/ESB":                 "Amber/ESB",
		"":                          "",
	} {
		if got := tidyStyle(in); got != want {
			t.Errorf("tidyStyle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTidyABV(t *testing.T) {
	for in, want := range map[string]string{
		"11%":  "11.0%",
		"5.7%": "5.7%",
		"15%":  "15.0%",
		"":     "",
		"n/a":  "n/a",
	} {
		if got := tidyABV(in); got != want {
			t.Errorf("tidyABV(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeBeerNameAndMatching(t *testing.T) {
	index := map[string]string{
		"newtonian amber ale":      "https://untappd.com/b/newtonian/1",
		"acceleration":             "https://untappd.com/b/acceleration/2",
		"barrel aged acceleration": "https://untappd.com/b/ba-acceleration/3",
		"viener vino barrel aged":  "https://untappd.com/b/viener/4",
		"mr radar nitro":           "https://untappd.com/b/radar/5",
	}
	for name, want := range map[string]string{
		"Newtonian":                 "https://untappd.com/b/newtonian/1",
		"Mr. Radar Nitro (Nitro)":   "https://untappd.com/b/radar/5",
		"Viener Vino (Barrel-Aged)": "https://untappd.com/b/viener/4",
	} {
		got, ok := matchBeer(index, name)
		if !ok || got != want {
			t.Errorf("matchBeer(%q) = %q/%v, want %q", name, got, ok, want)
		}
	}
	if _, ok := matchBeer(index, "Something Else Entirely"); ok {
		t.Error("matchBeer matched an unrelated name")
	}
}

func TestFetchBeerNoteReadsTheDescription(t *testing.T) {
	// The markup Untappd serves, class-name typo included.
	page := `<div class="desc">
		<div class="beer-descrption-read-more">Award winning British style ale.</div>
		<div class="beer-descrption-read-less" style="display: none;">
			Award winning British style ale. Marris Otter Malt and East Kent Golding Hops.
			<a href="#" class="read-less">Show Less</a>
		</div>
	</div>`
	note := parseBeerNote(page)
	want := "Award winning British style ale. Marris Otter Malt and East Kent Golding Hops."
	if note != want {
		t.Errorf("parseBeerNote() = %q, want %q", note, want)
	}
	if parseBeerNote("<html>no description here</html>") != "" {
		t.Error("a page with no description should yield empty, not junk")
	}
}

// The document has to actually compose: fonts embed, pages break, and the
// bytes come out looking like a PDF.
func TestRenderPrintPDF(t *testing.T) {
	raw, err := os.ReadFile("testdata/untappd_board.html")
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	rows := ParseBeers(string(raw))
	// A description on every row, so the pagination is exercised rather than
	// the empty-note fast path. The text is arbitrary; the layout is not.
	// Each one different, or the grouping rule folds them into one entry.
	for i := range rows {
		rows[i].Notes = "A beer with a description long enough to wrap onto a " +
			"second line, which is what makes a row tall and a page break happen: " +
			rows[i].Name + "."
	}
	cfg := samplePrintConfig()
	m := PrintMenu{
		Title:     cfg.Title,
		Subtitle:  cfg.Subtitle,
		Flight:    cfg.Flight,
		Sizes:     defaultSizes,
		FootLeft:  cfg.FootLeft,
		FootRight: cfg.FootRight,
		Sections:  buildSections(mergeExtras(rows, cfg.Extras), cfg.Blurbs),
	}
	var buf bytes.Buffer
	if err := RenderPrintPDF(m, &buf); err != nil {
		t.Fatalf("rendering: %v", err)
	}
	if !bytes.HasPrefix(buf.Bytes(), []byte("%PDF-")) {
		t.Fatal("output is not a PDF")
	}
	if buf.Len() < 10_000 {
		t.Errorf("PDF is %d bytes, suspiciously small for an embedded font", buf.Len())
	}
	// Letter portrait, and more than one page for a full board.
	if n := strings.Count(buf.String(), "/Type /Page\n"); n < 2 {
		t.Errorf("got %d pages, want a multi-page menu", n)
	}
}

// A menu with nothing on it still has to render rather than panic: a brand
// new workspace hits this path before anybody has set a tap list.
func TestRenderPrintPDFEmpty(t *testing.T) {
	var buf bytes.Buffer
	err := RenderPrintPDF(PrintMenu{Title: "Beers", Subtitle: "& Beverages"}, &buf)
	if err != nil {
		t.Fatalf("rendering an empty menu: %v", err)
	}
	if !bytes.HasPrefix(buf.Bytes(), []byte("%PDF-")) {
		t.Fatal("output is not a PDF")
	}
}

// The till is the third consumer, and it needs a pour the other two discard:
// a flight is four 4oz pours, so the price it sells at is never the headline.
func TestPourBySizeServesAFlight(t *testing.T) {
	b := Beer{Name: "Newtonian", Pours: []Pour{
		{Size: "16oz", Label: "16oz Draft", Price: "6.50"},
		{Size: "9oz", Label: "9oz Draft", Price: "4"},
		{Size: "4oz", Label: "4oz Draft", Price: "3"},
		{Size: "64oz", Label: "64oz Growler", Price: "18"},
	}}
	pour, ok := b.PourBySize("4oz")
	if !ok || pour.Price != "3" {
		t.Errorf("PourBySize(4oz) = %q (%v), want 3", pour.Price, ok)
	}
	// Case and spacing come off a scrape, so the lookup must not be brittle.
	if pour, ok := b.PourBySize(" 16OZ "); !ok || pour.Price != "6.50" {
		t.Errorf("PourBySize(\" 16OZ \") = %q (%v), want 6.50", pour.Price, ok)
	}
	if _, ok := b.PourBySize("32oz"); ok {
		t.Error("PourBySize matched a container the beer does not come in")
	}
}

// The whole point of the shared parse is that one walk feeds every surface.
// If the board's narrowing drifts from the source, this catches it.
func TestBoardNarrowsTheSharedParse(t *testing.T) {
	raw, err := os.ReadFile("testdata/untappd_board.html")
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	beers := ParseBeers(string(raw))
	taps := ParseUntappdBoard(string(raw))

	// Every tap is a beer, in order, and the only beers missing are the ones
	// with no price at all.
	var priced int
	for _, b := range beers {
		if _, ok := b.Headline(); ok {
			priced++
		}
	}
	if len(taps) != priced {
		t.Errorf("board shows %d taps but %d of %d beers are priced",
			len(taps), priced, len(beers))
	}
	if len(beers) <= len(taps) {
		t.Error("the fixture should carry at least one priceless beer the board drops")
	}
	byName := map[string]Beer{}
	for _, b := range beers {
		byName[b.Name] = b
	}
	for _, tp := range taps {
		b, ok := byName[tp.Name]
		if !ok {
			t.Errorf("board shows %q, which the shared parse never produced", tp.Name)
			continue
		}
		if tp.Section != b.Section || tp.ABV != b.ABV || tp.Style != b.Style {
			t.Errorf("%s: board and source disagree on the beer's own facts", tp.Name)
		}
	}
}

// The printed menu reorders styles for reading; the shared model must not.
// This pins both halves, because the reorder was briefly lost when the parse
// moved into source.go and nothing failed.
func TestStyleIsTidiedForPrintButRawInTheSource(t *testing.T) {
	raw, err := os.ReadFile("testdata/untappd_board.html")
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	var found bool
	for _, b := range ParseBeers(string(raw)) {
		if b.Style != "IPA - West Coast" {
			continue
		}
		found = true
		if got := tidyStyle(b.Style); got != "West Coast IPA" {
			t.Errorf("tidyStyle(%q) = %q, want West Coast IPA", b.Style, got)
		}
	}
	if !found {
		t.Fatal("fixture no longer carries a genus-first style to check")
	}
}

// newMeasuringPDF builds a document with the fonts loaded, so text can be
// measured without drawing anything.
func newMeasuringPDF(t *testing.T) *fpdf.Fpdf {
	t.Helper()
	pdf := fpdf.NewCustom(&fpdf.InitType{
		UnitStr: "pt",
		Size:    fpdf.SizeType{Wd: pageW, Ht: pageH},
	})
	if err := loadPrintFonts(pdf); err != nil {
		t.Fatalf("loading fonts: %v", err)
	}
	pdf.AddPage()
	return pdf
}

// Only beers that actually pour belong on a menu. A can sitting in the board's
// markup, or a beer whose prices were cleared upstream, is something a
// customer cannot order by the glass.
func TestOnTapNeedsAPricedTaster(t *testing.T) {
	for _, tc := range []struct {
		name  string
		pours []Pour
		want  bool
	}{
		{"a full tap", []Pour{
			{Size: "16oz", Label: "16oz Draft", Price: "6.50"},
			{Size: "9oz", Label: "9oz Draft", Price: "4"},
			{Size: "4oz", Label: "4oz Draft", Price: "3"},
		}, true},
		{"poured small only", []Pour{
			{Size: "9oz", Label: "9oz Draft", Price: "9"},
			{Size: "4oz", Label: "4oz Draft", Price: "5"},
		}, true},
		{"prices cleared upstream", nil, false},
		{"a can is not a pour", []Pour{
			{Size: "12oz", Label: "12oz Can", Price: "6"},
		}, false},
		{"a growler is takeaway", []Pour{
			{Size: "64oz", Label: "64oz Growler", Price: "18"},
		}, false},
		{"a four-ounce can is still a can", []Pour{
			{Size: "4oz", Label: "4oz Can", Price: "3"},
		}, false},
		{"an unpriced taster does not count", []Pour{
			{Size: "4oz", Label: "4oz Draft", Price: ""},
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := (Beer{Pours: tc.pours}).OnTap(); got != tc.want {
				t.Errorf("OnTap() = %v, want %v", got, tc.want)
			}
		})
	}
}

// The fixture carries a beer with its prices cleared, which is exactly the row
// that was printing with no price beside it.
func TestOnTapOnlyDropsThePricelessBeer(t *testing.T) {
	raw, err := os.ReadFile("testdata/untappd_board.html")
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	all := ParseBeers(string(raw))
	pouring := OnTapOnly(all)
	if len(pouring) >= len(all) {
		t.Fatal("the fixture should carry at least one beer that is not pouring")
	}
	for _, b := range pouring {
		if _, ok := b.Headline(); !ok {
			t.Errorf("%s is on tap but has no price to quote", b.Name)
		}
	}
	for _, b := range all {
		if b.Name == "Cerveza Espacial" && b.OnTap() {
			t.Error("a beer with no containers at all should not count as pouring")
		}
	}
	// Order is preserved, so the sections still read in the order staff set.
	var j int
	for _, b := range all {
		if j < len(pouring) && b.Name == pouring[j].Name {
			j++
		}
	}
	if j != len(pouring) {
		t.Error("filtering reordered the tap list")
	}
}
