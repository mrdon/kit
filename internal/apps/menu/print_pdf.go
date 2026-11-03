package menu

import (
	"bytes"
	"embed"
	"fmt"
	"io"
	"math"
	"slices"

	"github.com/go-pdf/fpdf"
)

// Drawing the printed menu.
//
// The sheet follows the wall board: a navy masthead with the venue's mark and
// name, headings in tracked capitals over a gold rule, each beer's name in
// capitals with its style and a gold ABV under it and the price on the right,
// and the size beneath the price when it is not a pint. Paper adds what the
// wall has no room for: every beer's description, the cans and sodas, and the
// house lines in the footer.
//
// It is laid out in two columns that flow: down the left, down the right, on
// to the next page. Everything is absolute placement in points, because fpdf's
// cell-and-flow model cannot keep a price on its row or a heading with the
// beer under it. Layout is planned first and drawn second, so the footer can
// say "Page 1 of 2" and a test can walk the same plan the printer does.
//
// Within a column the rule is greedy: fill it, and move on only when the next
// thing will not fit above the column's bottom line, which planLayout sets so
// the columns come out level. A heading never sits alone at the foot of a
// column, and a section that carries over repeats its heading marked "cont.",
// as the board does. Tidier-sounding rules were tried on the old one-column
// sheet and both lost: keeping sections whole left half a page white, and a
// widow guard gave up a quarter of a page to save one beer.

//go:embed fonts/Archivo-Regular.ttf fonts/Archivo-Medium.ttf fonts/Archivo-SemiBold.ttf fonts/Archivo-Bold.ttf
var printFonts embed.FS

// Font families, registered under these names on every document. Archivo is
// the board's face, embedded at the four weights the sheet uses rather than
// named as a system font: a menu that reflows differently on the taproom
// laptop than in the preview is worse than a larger file.
const (
	printBody   = "menu-print-400"
	printMedium = "menu-print-500"
	printSemi   = "menu-print-600"
	printBold   = "menu-print-700"
)

// Page geometry, in points. Letter portrait, half-inch side margins.
const (
	pageW    = 612.0
	pageH    = 792.0
	marginX  = 36.0
	contentW = pageW - 2*marginX
	colGap   = 22.0
	colW     = (contentW - colGap) / 2

	// The masthead band on page one, and the gold rule under it.
	mastTop   = 30.0
	mastH     = 50.0
	mastRuleH = 2.25

	// Where the columns may start and must end. Later pages carry a one-line
	// running head instead of the band, and start higher.
	bodyTopFirst = mastTop + mastH + mastRuleH + 14
	bodyTopRest  = 68.0
	bodyBottom   = 736.0
)

// The board's palette, as the wall sets it (templates/board.css), turned for
// paper: navy ink rather than white on navy, and the gold ABV darkened until
// it reads on white.
var (
	printNavy  = rgb{27, 43, 56}
	printGold  = rgb{217, 160, 60}  // rules
	printHot   = rgb{255, 194, 71}  // the flight badge, the NEW tag
	printMuted = rgb{91, 107, 120}  // styles, sizes, footer
	printAmber = rgb{154, 106, 18}  // ABV, about 5:1 on white
	printPale  = rgb{185, 196, 207} // secondary type on the navy band
	printWhite = rgb{255, 255, 255}
)

// placement is one thing the plan put somewhere: a section heading or a unit.
type placement struct {
	page, col int
	y         float64
	sec       *PrintSection
	heading   bool // the section's heading rather than one of its units
	cont      bool // a heading repeated at the top of a column
	unit      printUnit
}

// sheet is one print run's state: the document and what the plan made of it.
type sheet struct {
	pdf        *fpdf.Fpdf
	m          PrintMenu
	logoAspect float64 // width over height; zero when there is no logo
	anyGR      bool    // whether the footer needs the GR key
}

// RenderPrintPDF writes the menu.
func RenderPrintPDF(m PrintMenu, out io.Writer) error {
	pdf := fpdf.NewCustom(&fpdf.InitType{
		UnitStr: "pt",
		Size:    fpdf.SizeType{Wd: pageW, Ht: pageH},
	})
	if err := loadPrintFonts(pdf); err != nil {
		return err
	}
	// Pagination is decided by the plan, not by fpdf.
	pdf.SetAutoPageBreak(false, 0)
	pdf.SetMargins(0, 0, 0)

	sh := &sheet{pdf: pdf, m: m, anyGR: menuHasGR(m)}
	sh.registerImages()
	sections := m.Sections
	if m.Featured != nil {
		sections = append([]PrintSection{{featured: sh.m.Featured}}, sections...)
	}
	plan, pages := planLayout(pdf, sections)

	for p := range pages {
		pdf.AddPage()
		if p == 0 {
			sh.drawMasthead()
		} else {
			sh.drawRunningHead()
		}
		sh.drawFooter(p+1, pages)
		for _, pl := range plan {
			if pl.page == p {
				drawPlacement(pdf, pl)
			}
		}
	}

	if err := pdf.Error(); err != nil {
		return fmt.Errorf("composing printed menu: %w", err)
	}
	if err := pdf.Output(out); err != nil {
		return fmt.Errorf("writing printed menu pdf: %w", err)
	}
	return nil
}

// planLayout flows every section through the columns and reports where each
// heading and unit lands, and how many pages that took. An empty menu is one
// page: the masthead and footer still print.
//
// The columns are balanced rather than packed. Filling each column to the
// footer and moving on left the last one holding three sodas under a page of
// beers, which reads as lopsided however good the rows are. So the plan first
// finds how many pages the menu needs, then the highest common bottom line at
// which it still fits in every column of those pages, and breaks there: the
// columns end level, and the white space, if any, is shared out evenly at the
// foot of every column instead of pooled in the last.
func planLayout(pdf *fpdf.Fpdf, sections []PrintSection) ([]placement, int) {
	plan, cols := flowAt(pdf, sections, bodyBottom)
	pages := (cols + 1) / 2
	bottom := balancedBottom(pdf, sections, 2*pages)
	if bottom < bodyBottom {
		plan, cols = flowAt(pdf, sections, bottom)
	}
	return plan, (cols + 1) / 2
}

// balancedBottom is the bottom line that makes the columns most even. Of the
// lines at which the menu still fits in cols columns, it keeps the one whose
// columns end closest together, breaking ties toward the shorter. It is not
// simply the lowest line that fits: a section's last unit may run past the
// line, and the lowest line is often the one that makes the most of them do
// so, leaving one column long and the next short. Candidates are tried every
// few points; a menu is a few dozen units, so this is cheap.
func balancedBottom(pdf *fpdf.Fpdf, sections []PrintSection, cols int) float64 {
	best, bestScore := bodyBottom, math.Inf(1)
	for line := bodyBottom; line >= bodyTopFirst; line -= 4 {
		plan, n := flowAt(pdf, sections, line)
		if n > cols {
			break // lower lines only need more columns
		}
		lo, hi := columnEnds(pdf, plan, cols)
		if score := (hi - lo) + hi/100; score < bestScore {
			best, bestScore = line, score
		}
	}
	return best
}

// columnEnds reports how far down the shortest and the longest of cols
// columns run. An empty column ends at its own top.
func columnEnds(pdf *fpdf.Fpdf, plan []placement, cols int) (float64, float64) {
	ends := make([]float64, cols)
	for c := range ends {
		ends[c] = bodyTop(c / 2)
	}
	for _, pl := range plan {
		h := headingH
		if !pl.heading {
			h = unitHeight(pdf, pl.unit) - rowGap
		}
		if c := 2*pl.page + pl.col; c < cols {
			ends[c] = max(ends[c], pl.y+h)
		}
	}
	return slices.Min(ends), slices.Max(ends)
}

// flowAt runs the flow with every column ending at bottom, and reports the
// placements and how many columns they used.
func flowAt(pdf *fpdf.Fpdf, sections []PrintSection, bottom float64) ([]placement, int) {
	f := &flow{pdf: pdf, y: bodyTopFirst, atTop: true, bottom: bottom}
	for i := range sections {
		f.section(&sections[i])
	}
	return f.out, 2*f.page + f.col + 1
}

// flow is the cursor the plan walks.
type flow struct {
	pdf       *fpdf.Fpdf
	page, col int
	y         float64
	atTop     bool    // nothing placed in this column yet
	bottom    float64 // where every column ends; bodyBottom at the most
	out       []placement
}

// section places one heading and its units. The heading travels with the
// first unit, so it never strands at a column's foot; a unit that moves to a
// fresh column mid-section takes a "cont." heading with it.
func (f *flow) section(s *PrintSection) {
	units := sectionUnits(*s)
	if len(units) == 0 {
		return
	}
	for i, u := range units {
		h := unitHeight(f.pdf, u)
		if s.Name == "" {
			// A headless section: the featured box. No heading, never split.
			f.fit(h, true)
			f.place(placement{sec: s, unit: u}, h)
			continue
		}
		// The balanced line is a target, not a wall, for a section's last
		// unit: running it past the line to the footer beats leaving one beer
		// alone at the top of the next column under "cont." with half a page
		// of white above where it could have gone.
		last := i == len(units)-1
		if i == 0 {
			// Nor does a section start with its heading and one unit at the
			// foot of a column and the rest overleaf: it starts where its first
			// two fit, or in the next column.
			need, needLast := headingH+h, last
			if len(units) > 1 {
				need += unitHeight(f.pdf, units[1])
				needLast = len(units) == 2
			}
			f.fit(need, needLast)
			f.place(placement{sec: s, heading: true}, headingH)
		} else if f.fit(h, last) {
			f.place(placement{sec: s, heading: true, cont: true}, headingH)
		}
		f.place(placement{sec: s, unit: u}, h)
	}
	f.y += sectionGap
}

// fit moves to the next column when h will not fit below the cursor, and
// reports whether it moved. The column ends at the balanced line, or at the
// footer for a section's last unit. A column with nothing in it yet takes
// whatever it is given: a unit taller than a whole column is drawn and allowed
// to overrun rather than looping forever.
func (f *flow) fit(h float64, last bool) bool {
	limit := f.bottom
	if last {
		limit = bodyBottom
	}
	if f.atTop || f.y+h <= limit {
		return false
	}
	f.col++
	if f.col == 2 {
		f.page++
		f.col = 0
	}
	f.y, f.atTop = bodyTop(f.page), true
	return true
}

// bodyTop is where a column starts: below the masthead on page one, below the
// running head after that.
func bodyTop(page int) float64 {
	if page == 0 {
		return bodyTopFirst
	}
	return bodyTopRest
}

func (f *flow) place(p placement, h float64) {
	p.page, p.col, p.y = f.page, f.col, f.y
	f.out = append(f.out, p)
	f.y += h
	f.atTop = false
}

// colX is the left edge of a column.
func colX(col int) float64 { return marginX + float64(col)*(colW+colGap) }

func drawPlacement(pdf *fpdf.Fpdf, pl placement) {
	x := colX(pl.col)
	switch {
	case pl.heading:
		drawHeading(pdf, x, pl.y, pl.sec.Name, pl.cont)
	case pl.unit.Featured != nil:
		drawFeatured(pdf, x, pl.y, pl.unit.Featured)
	case pl.unit.Group != nil:
		drawGroup(pdf, x, pl.y, pl.unit.Group)
	case pl.unit.Beer != nil:
		drawBeer(pdf, x, pl.y, *pl.unit.Beer)
	default:
		drawBlurb(pdf, x, pl.y, pl.unit.Blurb)
	}
}

// menuHasGR reports whether any row carries the GR mark, so the footer only
// explains a badge that is actually on the page.
func menuHasGR(m PrintMenu) bool {
	for _, s := range m.Sections {
		for _, r := range s.Rows {
			if r.GlutenReduced {
				return true
			}
		}
	}
	return false
}

func loadPrintFonts(pdf *fpdf.Fpdf) error {
	for name, file := range map[string]string{
		printBody:   "fonts/Archivo-Regular.ttf",
		printMedium: "fonts/Archivo-Medium.ttf",
		printSemi:   "fonts/Archivo-SemiBold.ttf",
		printBold:   "fonts/Archivo-Bold.ttf",
	} {
		raw, err := printFonts.ReadFile(file)
		if err != nil {
			return fmt.Errorf("reading print font %s: %w", file, err)
		}
		pdf.AddUTF8FontFromBytes(name, "", raw)
	}
	return pdf.Error()
}

// registerImages embeds the masthead art once. A bad image is not worth losing
// the menu over: the error is cleared and the masthead goes without it, which
// is a clean result rather than a broken one.
func (sh *sheet) registerImages() {
	pdf := sh.pdf
	if len(sh.m.Hero) > 0 {
		pdf.RegisterImageOptionsReader("menu-print-hero",
			fpdf.ImageOptions{ImageType: sh.m.HeroKind}, bytes.NewReader(sh.m.Hero))
		if pdf.Error() != nil {
			pdf.ClearError()
			sh.m.Hero = nil
		}
	}
	if f := sh.m.Featured; f != nil && len(f.Photo) > 0 {
		pdf.RegisterImageOptionsReader("menu-print-featured",
			fpdf.ImageOptions{ImageType: f.PhotoKind}, bytes.NewReader(f.Photo))
		if pdf.Error() != nil {
			pdf.ClearError()
			f.Photo = nil
		}
	}
	if len(sh.m.Logo) > 0 {
		info := pdf.RegisterImageOptionsReader("menu-print-logo",
			fpdf.ImageOptions{ImageType: "PNG"}, bytes.NewReader(sh.m.Logo))
		if pdf.Error() != nil || info == nil || info.Height() == 0 {
			pdf.ClearError()
			sh.m.Logo = nil
			return
		}
		sh.logoAspect = info.Width() / info.Height()
	}
}
