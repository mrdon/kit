package menu

import (
	"bytes"
	"fmt"
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

// A section of beers must not be relabelled as cans because one beer lost its
// price upstream.
func TestSectionDraftSurvivesAPricelessBeer(t *testing.T) {
	beers := PrintSection{Rows: []Beer{
		{Name: "no price yet"},
		{Name: "priced", Pours: []Pour{{Size: "16oz", Label: "16oz Draft", Price: "7"}}},
	}}
	if !beers.Draft() {
		t.Error("a section holding a draft beer must print draft columns")
	}

	sodas := PrintSection{Rows: []Beer{
		{Name: "root beer", Pours: []Pour{{Size: "12oz", Label: "12oz Glass", Price: "4.50"}}},
		{Name: "lemonade", Pours: []Pour{{Size: "12oz", Label: "12oz Glass", Price: "3.50"}}},
	}}
	if sodas.Draft() {
		t.Error("a section of packaged drinks should print one price column")
	}
	if got := sodas.PackagedSize(); got != "12oz" {
		t.Errorf("PackagedSize() = %q, want 12oz", got)
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

func TestMoney(t *testing.T) {
	for in, want := range map[string]string{
		"6.50": "$6.50",
		"8":    "$8.00",
		"6.5":  "$6.50",
		"$7":   "$7",
		"":     "",
	} {
		if got := money(in); got != want {
			t.Errorf("money(%q) = %q, want %q", in, got, want)
		}
	}
}

// Section colours are configured by name; a section nobody has coloured still
// gets one, and no two adjacent headings share it.
func TestSectionColours(t *testing.T) {
	rows := []Beer{
		{Section: "Lagers", Name: "a"},
		{Section: "Pub Ales", Name: "b"},
		{Section: "Belgian Styles", Name: "c"},
	}
	got := buildSections(rows, map[string]string{"pub ales": "#123456"}, nil)
	if len(got) != 3 {
		t.Fatalf("got %d sections, want 3", len(got))
	}
	// Configured, matched case-insensitively.
	if got[1].Color != "#123456" {
		t.Errorf("Pub Ales colour = %q, want #123456", got[1].Color)
	}
	for i := 1; i < len(got); i++ {
		if got[i].Color == got[i-1].Color {
			t.Errorf("sections %d and %d share colour %s", i-1, i, got[i].Color)
		}
	}
	// Order follows the source, not the alphabet.
	if got[0].Name != "Lagers" || got[2].Name != "Belgian Styles" {
		t.Errorf("section order = %s, %s, %s; want source order",
			got[0].Name, got[1].Name, got[2].Name)
	}
}

// Extras land beside their own heading rather than at the end of the document.
func TestMergeExtrasGroupsWithItsSection(t *testing.T) {
	rows := []Beer{
		{Section: "Specialty", Name: "Mars Water"},
		{Section: "Lagers", Name: "Cerveza"},
	}
	extras := []Beer{
		{Section: "Specialty", Name: "A can"},
		{Section: "Sodas", Name: "Lemonade"},
	}
	sections := buildSections(mergeExtras(rows, extras), nil, nil)
	byName := map[string][]string{}
	for _, s := range sections {
		for _, r := range s.Rows {
			byName[s.Name] = append(byName[s.Name], r.Name)
		}
	}
	if len(byName["Specialty"]) != 2 {
		t.Errorf("Specialty has %v, want the tap and the can together", byName["Specialty"])
	}
	if len(byName["Sodas"]) != 1 {
		t.Errorf("Sodas has %v, want its own section", byName["Sodas"])
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
	for i := range rows {
		rows[i].Notes = "A beer with a description long enough to wrap onto a " +
			"second line, which is what makes a row tall and a page break happen."
	}
	cfg := samplePrintConfig()
	m := PrintMenu{
		Title:     cfg.Title,
		Subtitle:  cfg.Subtitle,
		Flight:    cfg.Flight,
		Sizes:     defaultSizes,
		FootLeft:  cfg.FootLeft,
		FootRight: cfg.FootRight,
		Sections:  buildSections(mergeExtras(rows, cfg.Extras), cfg.Colors, cfg.Blurbs),
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

// Nothing may be drawn below the footer rule. A long description on a heading
// low down used to overrun it, which on paper reads as a printer fault.
func TestRowsNeverOverrunTheFooter(t *testing.T) {
	long := "Made with Verboten Brewing for the 2026 Collab Fest, this English-style " +
		"barleywine was made with Root Shoot Vienna malt (Weiner Malz) and then aged " +
		"in Spirit Hound Honey Whisky barrels for nine long months."
	section := PrintSection{Name: "Specialty", Color: "#1b4525", Rows: []Beer{
		{Name: "Viener Vino", Style: "Barleywine - English", ABV: "15%", Notes: long,
			Pours: []Pour{{Size: "9oz", Label: "9oz Draft", Price: "9"}}},
	}}
	var buf bytes.Buffer
	if err := RenderPrintPDF(PrintMenu{Title: "Beers", Sections: []PrintSection{section}}, &buf); err != nil {
		t.Fatalf("rendering: %v", err)
	}

	// Walk every y a heading could land on, including the ones that leave just
	// too little room, and assert the section is never started there.
	pdf := newMeasuringPDF(t)
	for y := bodyTopRest; y < bodyBottom+120; y += 7 {
		n := rowsThatFit(pdf, section, section.Rows, y)
		if n == 0 {
			continue
		}
		bottom := y + sectionLead(pdf, section) + rowHeight(pdf, section, section.Rows[0])
		if bottom > bodyBottom {
			t.Fatalf("at y=%.0f the section was allowed to run to %.0f, past the %.0f limit",
				y, bottom, bodyBottom)
		}
	}
}

// newMeasuringPDF builds a document with the fonts loaded, so text can be
// measured without drawing anything.
func newMeasuringPDF(t *testing.T) *fpdf.Fpdf {
	t.Helper()
	pdf := fpdf.NewCustom(&fpdf.InitType{
		UnitStr: "pt",
		Size:    fpdf.SizeType{Wd: d(pageW), Ht: d(pageH)},
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

// Two sizes sharing a baseline read as bottom-aligned. The smaller run has to
// sit higher so their capitals share a centreline.
func TestAlignedBaselineCentresCapitals(t *testing.T) {
	const big, small, baseline = 38.0, 30.0, 100.0
	got := alignedBaseline(baseline, big, small)
	if got >= baseline {
		t.Fatalf("alignedBaseline() = %.2f, must lift the smaller run above %.2f",
			got, baseline)
	}
	// Their cap centres must land on the same line.
	bigCentre := baseline - big*capHeight/2
	smallCentre := got - small*capHeight/2
	if diff := bigCentre - smallCentre; diff > 0.01 || diff < -0.01 {
		t.Errorf("cap centres differ by %.3f, want them on one line", diff)
	}
	// Equal sizes must not move -- otherwise the helper would nudge type that
	// was already correct.
	if got := alignedBaseline(baseline, big, big); got != baseline {
		t.Errorf("alignedBaseline() moved equal sizes to %.2f, want %.2f", got, baseline)
	}
}

// A heading placed by a fraction of its bar's height sits high, because
// capitals ride above the middle of a line box. This pins the geometry the
// section bars depend on.
func TestCapCentredBaselineBalancesABar(t *testing.T) {
	const top, height = 100.0, 31.36
	base := capCentredBaseline(top, height, barTitlePt)
	above := (base - barTitlePt*capHeight) - top // air over the capitals
	below := (top + height) - base               // air under the baseline
	if diff := above - below; diff > 0.01 || diff < -0.01 {
		t.Errorf("bar heading has %.2f above and %.2f below, want them equal", above, below)
	}
	// The smaller column labels need their own centreline, not the heading's.
	if capCentredBaseline(top, height, colLabelPt) >= base {
		t.Error("column labels must sit higher than the heading's baseline")
	}
	// Nothing may escape the bar.
	if above < 0 || below < 0 {
		t.Errorf("heading overflows its bar: %.2f above, %.2f below", above, below)
	}
}

// The price column must not touch the section bar's right edge. It was flush
// against it once, which reads as a printing error rather than a design.
func TestPriceColumnClearsTheBarEdge(t *testing.T) {
	if colGutter <= 0 {
		t.Fatal("the price column needs a gutter off the bar edge")
	}
	barRight := marginX + contentW
	if colLargeRight >= barRight {
		t.Errorf("price column right edge %.1f is not inside the bar edge %.1f",
			colLargeRight, barRight)
	}
	// It must clear the ABV column on the other side just as surely.
	if colLargeRight-colABVMid < 100 {
		t.Errorf("only %.1f between the ABV column and the prices",
			colLargeRight-colABVMid)
	}
}

// A blurb with no matching heading becomes a section of its own, at the end,
// with no rows. This is the snacks case: one heading, one line, no prices.
func TestBlurbBecomesItsOwnSection(t *testing.T) {
	rows := []Beer{
		{Section: "Lagers", Name: "Cerveza"},
		{Section: "Sodas", Name: "Lemonade"},
	}
	blurbs := map[string]string{
		"Snacks": "Pretzels, chips and popcorn — ask at the bar.",
		"sodas":  "All fountain, all refillable.",
	}
	got := buildSections(rows, nil, blurbs)
	if len(got) != 3 {
		t.Fatalf("got %d sections, want Lagers, Sodas and Snacks", len(got))
	}
	// A blurb naming a heading that exists attaches to it, matched the same
	// case-insensitive way colours are, and does not spawn a duplicate.
	if got[1].Name != "Sodas" || got[1].Blurb != blurbs["sodas"] {
		t.Errorf("Sodas = %+v, want the blurb attached to the existing section", got[1])
	}
	if len(got[1].Rows) != 1 {
		t.Errorf("Sodas has %d rows, want its lemonade kept", len(got[1].Rows))
	}
	// One that names nothing lands last, and carries no rows at all.
	if got[2].Name != "Snacks" || len(got[2].Rows) != 0 {
		t.Errorf("last section = %+v, want a rowless Snacks", got[2])
	}
	if got[2].Color == "" {
		t.Error("a standalone blurb section got no bar colour")
	}
	if got[2].Color == got[1].Color {
		t.Errorf("Snacks took the same colour as the heading above it (%s)", got[2].Color)
	}
}

// The palette cycle cannot see a hand-picked colour, so an appended section
// has to step past whatever the heading above it was actually given.
func TestBlurbSectionAvoidsTheColourAboveIt(t *testing.T) {
	rows := []Beer{{Section: "Sodas", Name: "Lemonade"}}
	// Sodas is coloured by hand with the very colour the cycle would hand to
	// the section after it.
	got := buildSections(rows,
		map[string]string{"Sodas": printPalette[1]},
		map[string]string{"Snacks": "Pretzels and popcorn."})
	if len(got) != 2 {
		t.Fatalf("got %d sections, want Sodas and Snacks", len(got))
	}
	if got[1].Color == got[0].Color {
		t.Errorf("both headings are %s", got[0].Color)
	}
}

// An empty blurb is not a section. A console that writes every key it knows
// about would otherwise print a heading with nothing under it.
func TestEmptyBlurbIsIgnored(t *testing.T) {
	got := buildSections([]Beer{{Section: "Lagers", Name: "a"}}, nil,
		map[string]string{"Snacks": "   "})
	if len(got) != 1 {
		t.Fatalf("got %d sections, want only Lagers", len(got))
	}
}

// A rowless section still renders, and takes up only its bar and its sentence.
func TestBlurbOnlySectionRenders(t *testing.T) {
	snacks := PrintSection{
		Name:  "Snacks",
		Color: "#1b4525",
		Blurb: "Pretzels, chips and popcorn — ask at the bar.",
	}
	var buf bytes.Buffer
	err := RenderPrintPDF(PrintMenu{
		Title: "Beers",
		Sections: []PrintSection{{Name: "Lagers", Color: "#fec111", Rows: []Beer{
			{Name: "Cerveza", Style: "Lager - Mexican", ABV: "4.5%",
				Pours: []Pour{{Size: "16oz", Label: "16oz Draft", Price: "6"}}},
		}}, snacks},
	}, &buf)
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}
	if !bytes.HasPrefix(buf.Bytes(), []byte("%PDF-")) {
		t.Fatal("output is not a PDF")
	}

	pdf := newMeasuringPDF(t)
	// The blurb decides the section's height; without one it would be the
	// full row lead, which for a rowless heading is empty space.
	if lead := sectionLead(pdf, snacks); lead <= barH || lead >= barToName+80 {
		t.Errorf("blurb-only lead = %.1f, want a bar plus one or two lines", lead)
	}
	if got := sectionLead(pdf, PrintSection{Name: "Lagers"}); got != barToName {
		t.Errorf("lead with no blurb = %.1f, want the plain %.1f", got, barToName)
	}
}

// Pages are filled, not left half empty. A break may only happen where the
// next row genuinely would not have fitted -- keeping whole sections together
// was tried instead and left the bottom of a page white, which on a menu reads
// as a mistake rather than as design.
func TestPagesAreFilledBeforeBreaking(t *testing.T) {
	note := "A beer with a description long enough to wrap onto a second line, " +
		"which is what makes a row tall enough to force the issue."
	beer := func(n string) Beer {
		return Beer{Name: n, Style: "IPA - West Coast", ABV: "6.5%", Notes: note,
			Pours: []Pour{{Size: "16oz", Label: "16oz Draft", Price: "7"}}}
	}
	var sections []PrintSection
	for i := range 6 {
		sec := PrintSection{Name: fmt.Sprintf("Section %d", i), Color: "#1b4525"}
		for j := range 4 {
			sec.Rows = append(sec.Rows, beer(fmt.Sprintf("Beer %d-%d", i, j)))
		}
		sections = append(sections, sec)
	}
	var buf bytes.Buffer
	if err := RenderPrintPDF(PrintMenu{Title: "Beers", Sections: sections}, &buf); err != nil {
		t.Fatalf("rendering: %v", err)
	}
	if n := strings.Count(buf.String(), "/Type /Page\n"); n < 3 {
		t.Fatalf("got %d pages; the fixture is meant to overflow several", n)
	}

	// Walk the layout the renderer walks. Every break must be forced, and
	// nothing may cross the footer rule.
	pdf := newMeasuringPDF(t)
	y, atTop := bodyTopFirst, true
	for _, s := range sections {
		rows := s.Rows
		for {
			n := rowsThatFit(pdf, s, rows, y)
			if ((n == 0 && len(rows) > 0) || !headingFits(pdf, s, y)) && !atTop {
				if n > 0 {
					t.Errorf("%s broke the page with %d rows still fitting", s.Name, n)
				}
				// atTop is not raised: drawing lowers it again below with no
				// read in between.
				y = bodyTopRest
				n = rowsThatFit(pdf, s, rows, y)
			}
			if n == 0 && len(rows) > 0 {
				n = 1
			}
			// Everything that fits is drawn -- no row is held back.
			if n < len(rows) && n < rowsThatFit(pdf, s, rows, y) {
				t.Errorf("%s drew %d rows with %d fitting", s.Name, n, rowsThatFit(pdf, s, rows, y))
			}
			cursor := y + sectionLead(pdf, s)
			for _, r := range rows[:n] {
				cursor += rowHeight(pdf, s, r)
			}
			if cursor > bodyBottom {
				t.Errorf("%s ran to %.0f, past the %.0f limit", s.Name, cursor, bodyBottom)
			}
			y, atTop = cursor, false
			rows = rows[n:]
			if len(rows) == 0 {
				break
			}
			y, atTop = bodyTopRest, true
		}
		y += sectionGap
	}
}
