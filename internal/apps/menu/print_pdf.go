package menu

import (
	"bytes"
	"embed"
	"fmt"
	"io"
	"strings"

	"github.com/go-pdf/fpdf"
)

// Drawing the printed menu.
//
// Everything is absolute placement in points, converted from the design's
// units by d(). A menu is a measured document -- the price columns have to
// line up with their headings down three pages, and a description has to sit
// under its beer -- and fpdf's cell-and-flow model fights that: one long style
// name and the whole row reflows into the column beside it.
//
// So the page is laid out top-down by a cursor. Each section asks how tall it
// would be, takes a page break if it will not fit, and draws. Pagination is
// the one thing the original could not do, because the original was three
// hand-positioned artboards: a beer added in Untappd moved every row below it
// by hand.
//
// Sections are kept whole. A heading whose beers do not all fit moves to the
// next page rather than leaving two of them behind under a repeated one --
// this is already a multi-page document, so a sheet with some air at the
// bottom costs nothing, while a section that resumes overleaf makes a reader
// wonder whether they have seen all of it. Splitting is still there for a
// section genuinely taller than a sheet, where there is no other option.

//go:embed fonts/Montserrat-Regular.ttf fonts/Montserrat-Bold.ttf
var printFonts embed.FS

// Font families, registered under these names on every document.
//
// Montserrat stands in for the geometric sans the original was set in. It is
// a close match at the sizes that matter -- the same double-storey a, the same
// straight-legged R -- and it is embedded rather than named as a system font
// because a menu that reflows differently on the taproom laptop than in the
// preview is worse than a file that is 400KB larger.
const (
	printBold = "menu-print-bold"
	printBody = "menu-print-body"
)

// Page geometry, in design units. Letter portrait at 96dpi.
const (
	pageW = 816.0
	pageH = 1056.0

	marginX  = 79.55
	contentW = 654.85

	// Where the tap list may start and must end. Page one gives up its top to
	// the masthead; later pages get a plain wordmark and start higher.
	bodyTopFirst = 262.0
	bodyTopRest  = 112.0
	bodyBottom   = 920.0

	// The masthead sits a little under three quarters of an inch down. The
	// original had it tighter, which read as cramped on paper and risked the
	// band being clipped by printers that will not go to the edge -- a colour
	// block is far less forgiving of a narrow top margin than text is.
	heroTop = 58.0
	heroH   = 170.66

	// Type sizes for the masthead and the running head.
	heroTitlePt = 101.3
	heroSubPt   = 33.79
	contTitlePt = 38.0
	contSubPt   = 30.0

	// Title and subtitle baselines, as offsets into the band, so moving the
	// masthead moves its contents with it. The pair is centred in the band as
	// a block: measured from the top of the title's capitals to the bottom of
	// the subtitle's descenders, not from the boxes the glyphs sit in.
	heroTitleY = 96.0
	heroSubY   = 141.0

	// The running head on pages two and up, and how far the first bar sits
	// below it.
	contHeadY = 78.0
)

// Section bar and the price columns it labels.
const (
	barH       = 31.36
	barTitlePt = 28.78
	barTitleEm = 0.175
	colLabelPt = 16.0
	colLabelEm = 0.129
	colABVMid  = 600.0
	// colGutter keeps the price column off the section bar's right edge. The
	// heading's left inset is deliberately small -- it lines the heading up
	// with the beer names underneath it, which is worth more than symmetry --
	// but the right edge has no such alignment to honour, and a label sitting
	// hard against the end of a colour bar reads as a printing error.
	colGutter = 14.0

	// The price column is right-aligned to that gutter rather than centred on
	// a midpoint. Most of its values are five characters wide, but a beer that
	// does not pour a pint carries its size with it, and a centred
	// "$8.00 (12oz)" grows in both directions into whatever is beside it.
	colLargeRight = marginX + contentW - colGutter
)

// Row rhythm, measured off the original.
const (
	rowNamePt  = 21.33
	rowMetaPt  = 16.0
	rowMetaMin = 12.5 // a style may shrink this far before it is clipped
	rowSizePt  = 11.5 // the "(12oz)" beside an odd pour
	rowDescPt  = 14.67
	rowDescLH  = 1.4

	barToName  = 41.8  // section bar top -> first name in it
	nameToDesc = 29.73 // name top -> description top
	rowPadding = 22.0  // description bottom -> next name
	rowNoDescH = 44.0  // a whole row that has no description
	sectionGap = 14.0  // last row bottom -> next section bar

	// A section blurb is set at the description's size, because that is what
	// it is: prose about what is on offer rather than a row. Reusing the size
	// and leading keeps the page on one rhythm.
	blurbGap = 12.0 // section bar bottom -> first line of the blurb
	blurbPad = 16.0 // last line of the blurb -> the first beer under it
)

// Footer.
const (
	footRuleTop = 950.0
	footStrapY  = 972.0
	footSizesY  = 992.0
	footRule2Y  = 1003.07
	footLineY   = 1021.07
	footPt      = 15.98
	footSizesPt = 13.0
	footStrapEm = 0.32
	footSizesEm = 0.12
	footLineEm  = 0.234
)

var (
	printInk   = [3]int{4, 7, 7}
	printBlack = [3]int{0, 0, 0}
)

// RenderPrintPDF writes the menu.
func RenderPrintPDF(m PrintMenu, out io.Writer) error {
	pdf := fpdf.NewCustom(&fpdf.InitType{
		UnitStr: "pt",
		Size:    fpdf.SizeType{Wd: d(pageW), Ht: d(pageH)},
	})
	if err := loadPrintFonts(pdf); err != nil {
		return err
	}
	// Pagination is decided here, not by fpdf: a break must fall between rows
	// and repeat the section heading, which auto-break cannot know to do.
	pdf.SetAutoPageBreak(false, 0)
	pdf.SetMargins(0, 0, 0)
	registerPrintImages(pdf, m)

	first := true
	atTop := false
	y := 0.0
	newPage := func() {
		pdf.AddPage()
		if first {
			drawMasthead(pdf, m)
			y = bodyTopFirst
			first = false
		} else {
			drawContinuedHead(pdf, m)
			y = bodyTopRest
		}
		drawPrintFooter(pdf, m)
		atTop = true
	}
	newPage()

	for _, s := range m.Sections {
		rows := s.Rows
		for {
			// How many rows fit under a heading drawn at y.
			n := rowsThatFit(pdf, s, rows, y)
			// Anything short of the whole section moves to a fresh page, as
			// does a heading with no room left under it at all -- which is the
			// only test a section of pure blurb has, since it has no rows to
			// count. Already being at the top of a page means there is no
			// fresher page to move to.
			if (n < len(rows) || !headingFits(pdf, s, y)) && !atTop {
				newPage()
				n = rowsThatFit(pdf, s, rows, y)
			}
			// Still nothing fits on a page of its own: this section is taller
			// than a sheet and has to be split. Force a row through rather
			// than loop forever, and let it overrun.
			if n == 0 && len(rows) > 0 {
				n = 1
			}
			// Never send one row over on its own. A page holding a heading and
			// a single soda reads as a printer error, so the break moves up a
			// row and the two travel together.
			if len(rows)-n == 1 && n > 1 {
				n--
			}
			y = drawSection(pdf, s, rows[:n], y)
			atTop = false
			rows = rows[n:]
			if len(rows) == 0 {
				break
			}
			newPage()
		}
		y += sectionGap
	}

	if err := pdf.Error(); err != nil {
		return fmt.Errorf("composing printed menu: %w", err)
	}
	if err := pdf.Output(out); err != nil {
		return fmt.Errorf("writing printed menu pdf: %w", err)
	}
	return nil
}

func loadPrintFonts(pdf *fpdf.Fpdf) error {
	for name, file := range map[string]string{
		printBold: "fonts/Montserrat-Bold.ttf",
		printBody: "fonts/Montserrat-Regular.ttf",
	} {
		raw, err := printFonts.ReadFile(file)
		if err != nil {
			return fmt.Errorf("reading print font %s: %w", file, err)
		}
		pdf.AddUTF8FontFromBytes(name, "", raw)
	}
	return pdf.Error()
}

// registerPrintImages embeds the masthead art once. A bad image is not worth
// losing the menu over: the error is cleared and the masthead falls back to a
// flat band, which is a clean result rather than a broken one.
func registerPrintImages(pdf *fpdf.Fpdf, m PrintMenu) {
	if len(m.Hero) > 0 {
		pdf.RegisterImageOptionsReader("menu-print-hero",
			fpdf.ImageOptions{ImageType: m.HeroKind}, bytes.NewReader(m.Hero))
	}
	if len(m.Logo) > 0 {
		pdf.RegisterImageOptionsReader("menu-print-logo",
			fpdf.ImageOptions{ImageType: "PNG"}, bytes.NewReader(m.Logo))
	}
	if pdf.Error() != nil {
		pdf.ClearError()
	}
}

// rowsThatFit reports how many of rows can be drawn under a heading at y.
//
// Zero means not even the first row fits, and the caller must start a page.
// It deliberately does NOT force one row through: a heading low on the page
// with a long description under it used to be drawn anyway, and the last two
// lines ran underneath the footer rule. Better to move the heading and its
// beer together than to print a sentence into the furniture.
func rowsThatFit(pdf *fpdf.Fpdf, s PrintSection, rows []Beer, y float64) int {
	if !headingFits(pdf, s, y) {
		return 0
	}
	cursor := y + sectionLead(pdf, s)
	n := 0
	for _, r := range rows {
		next := cursor + rowHeight(pdf, s, r)
		if next > bodyBottom {
			break
		}
		cursor = next
		n++
	}
	return n
}

// headingFits reports whether a section bar drawn at y, and whatever sentence
// sits under it, land above the footer rule. It is the whole test for a
// section that is nothing but a blurb.
func headingFits(pdf *fpdf.Fpdf, s PrintSection, y float64) bool {
	return y+sectionLead(pdf, s) <= bodyBottom
}

// sectionLead is the distance from a section bar's top to its first row.
//
// Without a blurb that is barToName, the rhythm the original was set on. With
// one it is the bar's own height plus the sentence, because the sentence has
// to go between them.
func sectionLead(pdf *fpdf.Fpdf, s PrintSection) float64 {
	lines := blurbLines(pdf, s)
	if len(lines) == 0 {
		return barToName
	}
	return barH + blurbGap + float64(len(lines))*rowDescPt*rowDescLH + blurbPad
}

// blurbLines measures a section's blurb at the width it will be set in.
func blurbLines(pdf *fpdf.Fpdf, s PrintSection) []string {
	text := strings.TrimSpace(s.Blurb)
	if text == "" {
		return nil
	}
	pdf.SetFont(printBody, "", rowDescPt*designScale)
	return wrapText(pdf, text, d(contentW))
}

// rowHeight is what one row advances the cursor by.
func rowHeight(pdf *fpdf.Fpdf, s PrintSection, r Beer) float64 {
	lines := descLines(pdf, s, r)
	if lines == 0 {
		// No description -- a soda, or a beer nobody has written up yet. The
		// row is just its name line, but it still needs air under it: packed
		// tight against the next beer, the NEXT beer's description reads as
		// though it belongs to this one.
		return rowNoDescH
	}
	return nameToDesc + float64(lines)*rowDescPt*rowDescLH + rowPadding
}

// descLines measures a row's description at the width it will be set in.
func descLines(pdf *fpdf.Fpdf, _ PrintSection, r Beer) int {
	note := strings.TrimSpace(r.Notes)
	if note == "" {
		return 0
	}
	pdf.SetFont(printBody, "", rowDescPt*designScale)
	return len(wrapText(pdf, note, d(contentW)))
}

// drawSection draws the heading and its rows, returning the new cursor.
func drawSection(pdf *fpdf.Fpdf, s PrintSection, rows []Beer, y float64) float64 {
	drawSectionBar(pdf, s, y)
	drawSectionBlurb(pdf, s, y)
	cursor := y + sectionLead(pdf, s)
	for _, r := range rows {
		drawRow(pdf, s, r, cursor)
		cursor += rowHeight(pdf, s, r)
	}
	return cursor
}

// drawSectionBar is the coloured strip: the heading on the left, the price
// column labels on the right, both knocked out in white.
func drawSectionBar(pdf *fpdf.Fpdf, s PrintSection, y float64) {
	r, g, b := hexRGB(s.Color)
	pdf.SetFillColor(r, g, b)
	pdf.Rect(d(marginX), d(y), d(contentW), d(barH), "F")

	pdf.SetTextColor(255, 255, 255)
	pdf.SetFont(printBold, "", barTitlePt*designScale)
	drawSpaced(pdf, d(marginX+2.05), d(capCentredBaseline(y, barH, barTitlePt)),
		strings.ToUpper(s.Name), barTitleEm, barTitlePt*designScale)

	// A heading that carries a sentence rather than a list has nothing to
	// label. "Price" over a line about pretzels is furniture, and the ABV of a
	// bowl of popcorn is not a question anyone has.
	if len(s.Rows) == 0 {
		return
	}

	// The column labels are half the size of the heading, so they need their
	// own centreline rather than the heading's.
	pdf.SetFont(printBold, "", colLabelPt*designScale)
	base := d(capCentredBaseline(y, barH, colLabelPt))
	if s.Draft() {
		drawSpacedCentre(pdf, d(colABVMid), base, "ABV", colLabelEm, colLabelPt*designScale)
		drawSpacedRight(pdf, d(colLargeRight), base, DefaultPour, colLabelEm, colLabelPt*designScale)
		return
	}
	// A section of cans and sodas has one price and no ABV worth printing.
	drawSpacedRight(pdf, d(colLargeRight), base, s.PackagedSize(), colLabelEm, colLabelPt*designScale)
}

// drawSectionBlurb sets the sentence under a heading.
//
// It repeats with the bar on a continuation page, for the same reason the bar
// does: somebody landing on the second page of a section should not have to
// turn back to find out what it says.
func drawSectionBlurb(pdf *fpdf.Fpdf, s PrintSection, y float64) {
	lines := blurbLines(pdf, s)
	if len(lines) == 0 {
		return
	}
	pdf.SetTextColor(printInk[0], printInk[1], printInk[2])
	pdf.SetFont(printBody, "", rowDescPt*designScale)
	dy := d(y + barH + blurbGap + rowDescPt*0.78)
	for _, line := range lines {
		pdf.Text(d(marginX+2.05), dy, line)
		dy += d(rowDescPt * rowDescLH)
	}
}

// drawRow is one beer: name, style beside it, the price columns, and the
// description on the line below.
func drawRow(pdf *fpdf.Fpdf, s PrintSection, r Beer, y float64) {
	pdf.SetTextColor(printBlack[0], printBlack[1], printBlack[2])

	pdf.SetFont(printBold, "", rowNamePt*designScale)
	baseline := d(y + rowNamePt*0.78)
	pdf.Text(d(marginX+1.92), baseline, r.Name)
	nameW := pdf.GetStringWidth(r.Name)

	// The style sits on the same line, set smaller and following the name
	// rather than in a column of its own -- names vary from "Newtonian" to
	// "Wavelength Watermelon Session", and a fixed column would either crowd
	// the long ones or strand the short ones.
	// Tidied here rather than at parse time: Untappd files styles genus-first
	// so they sort together, and turning "IPA - West Coast" into "West Coast
	// IPA" is a decision about how a menu reads, not about what the beer is.
	// The board and the till want the raw string.
	if style := tidyStyle(r.Style); style != "" {
		x := d(marginX+1.92) + nameW + d(12)
		// The gutter clears half the widest strength ("15.0%") plus a gap. A
		// section with no ABV column has no such neighbour, so the style may
		// run on until it reaches the prices.
		limit := d(colABVMid - 32)
		if !s.Draft() {
			limit = d(colLargeRight - 90)
		}
		if x < limit {
			// A long beer with a long style -- "Wavelength Watermelon Session",
			// "English Barleywine" -- runs out of line before it runs out of
			// words. Shrinking the style a little saves it; clipping is the
			// last resort, because half a style name says less than none.
			size := fitStyleSize(pdf, style, limit-x)
			pdf.SetFont(printBody, "", size)
			pdf.Text(x, baseline, clipTo(pdf, style, limit-x))
		}
	}

	pdf.SetFont(printBody, "", rowMetaPt*designScale)
	if s.Draft() {
		if abv := tidyABV(r.ABV); abv != "" {
			centreText(pdf, d(colABVMid), baseline, abv)
		}
		drawLargePour(pdf, baseline, r)
	} else if pour, ok := r.Headline(); ok {
		rightText(pdf, d(colLargeRight), baseline, money(pour.Price))
	}

	note := strings.TrimSpace(r.Notes)
	if note == "" {
		return
	}
	pdf.SetFont(printBody, "", rowDescPt*designScale)
	dy := d(y + nameToDesc + rowDescPt*0.78)
	for _, line := range wrapText(pdf, note, d(contentW)) {
		pdf.Text(d(marginX+2.05), dy, line)
		dy += d(rowDescPt * rowDescLH)
	}
}

// clipTo shortens a string to fit, with an ellipsis. Used only for the style
// beside a name, where the alternative is running into the ABV column.
func clipTo(pdf *fpdf.Fpdf, s string, width float64) string {
	if width <= 0 || pdf.GetStringWidth(s) <= width {
		return s
	}
	runes := []rune(s)
	for len(runes) > 1 {
		runes = runes[:len(runes)-1]
		if pdf.GetStringWidth(string(runes)+"…") <= width {
			return string(runes) + "…"
		}
	}
	return ""
}

// money puts the currency symbol back on. Untappd stores "6.50" and "8"; a
// menu prints $6.50 and $8.00, because a column of prices where some have
// cents and some do not reads as a mistake.
func money(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "$") {
		return s
	}
	if !strings.Contains(s, ".") {
		return "$" + s + ".00"
	}
	// One decimal place ("6.5") is a price, not a typo -- pad it.
	if i := strings.Index(s, "."); len(s)-i == 2 {
		return "$" + s + "0"
	}
	return "$" + s
}
