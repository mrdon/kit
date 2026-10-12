package events

import (
	"bytes"
	"embed"
	"fmt"
	"io"
	"strings"

	"github.com/go-pdf/fpdf"
)

// Drawing the topper.
//
// Everything here is absolute placement in millimetres rather than fpdf's
// cell-and-flow model. A poster is a fixed composition -- the bands have to
// divide the panel evenly and end where the footer begins -- and flow layout
// fights that: one long title and the whole card reflows off the page. So the
// panel is measured once, the bands are given their share, and text is fitted
// into the space that remains.
//
// Two copies are printed side by side on a landscape Letter sheet. One sheet,
// one cut down the middle plus a trim around the pair, two tables covered.

//go:embed fonts/*.ttf
var topperFonts embed.FS

// Font families, registered by these names on every document.
//
// Anton is a single heavy condensed weight -- it is the poster voice, and it
// only works in caps at size. Barlow Condensed carries the small print, where
// Anton would be unreadable. Both are embedded rather than named as system
// fonts because a PDF that renders differently on the taproom laptop than in
// the preview is worse than one that is 300KB larger.
const (
	fontDisplay = "topper-display"
	fontText    = "topper-text"
)

// Page and panel geometry, in millimetres.
//
// A panel is a 4x6in card, because that is the size the table-top frames take:
// print, cut, slide the card in. Two sit side by side with no gutter -- the
// cut line down the middle is the shared edge, so a slightly off-centre cut
// costs a millimetre of margin, not content -- and the pair is centred on the
// sheet, which leaves 38mm of paper on each side, well clear of any printer's
// unprintable border.
//
// Every other measurement on the card is proportioned to this panel. Changing
// the panel size means re-fitting them, not just these two numbers.
const (
	sheetW  = 279.4
	sheetH  = 215.9
	panelW  = 101.6
	panelH  = 152.4
	originX = (sheetW - 2*panelW) / 2
	originY = (sheetH - panelH) / 2
	margin  = 8.0
	accentH = 3.6
)

// Band colours, cycled down the panel. Warm and saturated so the day reads
// from across a table, and distinct enough side by side that a customer can
// track one row without re-reading the day.
var topperBands = [][3]int{
	{79, 122, 46},   // green
	{109, 110, 113}, // slate
	{156, 74, 74},   // maroon
	{212, 91, 51},   // rust
	{230, 138, 46},  // amber
	{62, 107, 140},  // blue
	{123, 90, 140},  // plum
}

var (
	accentColor = [3]int{247, 214, 74}
	inkColor    = [3]int{26, 26, 26}
	mutedColor  = [3]int{120, 120, 120}
)

// RenderTopperPDF writes the two-up sheet.
func RenderTopperPDF(t Topper, out io.Writer) error {
	pdf := fpdf.NewCustom(&fpdf.InitType{
		UnitStr: "mm",
		Size:    fpdf.SizeType{Wd: sheetW, Ht: sheetH},
	})
	if err := loadTopperFonts(pdf); err != nil {
		return err
	}
	pdf.SetAutoPageBreak(false, 0)
	pdf.SetMargins(0, 0, 0)
	pdf.AddPage()

	registerTopperImages(pdf, t)
	// Everything below is drawn in panel coordinates -- (0,0) is the top left
	// of the left-hand card -- and the translation puts the pair on the sheet.
	// Cheaper than threading an origin through every draw call, and it keeps
	// the card's geometry readable as the card's own.
	pdf.TransformBegin()
	pdf.TransformTranslate(originX, originY)
	for i := range 2 {
		drawPanel(pdf, t, float64(i)*panelW)
	}
	drawCutLines(pdf)
	pdf.TransformEnd()

	if err := pdf.Error(); err != nil {
		return fmt.Errorf("composing topper: %w", err)
	}
	if err := pdf.Output(out); err != nil {
		return fmt.Errorf("writing topper pdf: %w", err)
	}
	return nil
}

func loadTopperFonts(pdf *fpdf.Fpdf) error {
	for name, file := range map[string]string{
		fontDisplay: "fonts/Anton-Regular.ttf",
		fontText:    "fonts/BarlowCondensed-SemiBold.ttf",
	} {
		raw, err := topperFonts.ReadFile(file)
		if err != nil {
			return fmt.Errorf("reading topper font %s: %w", file, err)
		}
		pdf.AddUTF8FontFromBytes(name, "", raw)
	}
	return pdf.Error()
}

// registerTopperImages loads every image once, under a stable name, so the two
// panels share one embedded copy instead of doubling the file size.
func registerTopperImages(pdf *fpdf.Fpdf, t Topper) {
	for i, row := range t.Rows {
		if row.Poster == nil {
			continue
		}
		pdf.RegisterImageOptionsReader(posterName(i),
			fpdf.ImageOptions{ImageType: row.Poster.Kind},
			bytes.NewReader(row.Poster.Data))
	}
	if len(t.Logo) > 0 {
		pdf.RegisterImageOptionsReader("topper-logo",
			fpdf.ImageOptions{ImageType: "PNG"}, bytes.NewReader(t.Logo))
	}
	// A bad image is not worth losing the sheet over -- clear the error so the
	// rest of the document still writes, and skip the picture at draw time.
	if pdf.Error() != nil {
		pdf.ClearError()
	}
}

func posterName(i int) string { return fmt.Sprintf("topper-poster-%d", i) }

// drawCutLines marks where to cut: the trim around the pair and the split
// between them. Dashed and pale -- a guide for scissors that does not read as
// part of either card, and that a frame's lip hides anyway.
func drawCutLines(pdf *fpdf.Fpdf) {
	pdf.SetDrawColor(190, 190, 190)
	pdf.SetLineWidth(0.2)
	pdf.SetDashPattern([]float64{2, 2}, 0)
	pdf.Rect(0, 0, 2*panelW, panelH, "D")
	pdf.Line(panelW, 0, panelW, panelH)
	pdf.SetDashPattern(nil, 0)
}

// drawPanel draws one card at horizontal offset x0.
func drawPanel(pdf *fpdf.Fpdf, t Topper, x0 float64) {
	pdf.SetFillColor(accentColor[0], accentColor[1], accentColor[2])
	pdf.Rect(x0, 0, panelW, accentH, "F")
	pdf.Rect(x0, panelH-accentH, panelW, accentH, "F")

	headBottom := drawTopperHead(pdf, t, x0)
	footTop := drawTopperFoot(pdf, t, x0)
	drawBands(pdf, t.Rows, x0, headBottom, footTop-headBottom)
}

// drawTopperHead lays out the title block and returns the y the bands may
// start at.
func drawTopperHead(pdf *fpdf.Fpdf, t Topper, x0 float64) float64 {
	inner := panelW - 2*margin
	pdf.SetTextColor(inkColor[0], inkColor[1], inkColor[2])

	size := fitFontSize(pdf, fontDisplay, strings.ToUpper(t.Heading), inner, 25)
	pdf.SetFont(fontDisplay, "", size)
	y := accentH + 4.5 + ptToMM(size)*0.75
	centreText(pdf, x0, strings.ToUpper(t.Heading), y)

	pdf.SetFont(fontText, "", 11)
	y += 5.5
	centreText(pdf, x0, spaced(strings.ToUpper(t.DateRange)), y)
	return y + 4.5
}

// drawTopperFoot draws the branding strip and returns the y the bands must end
// by.
func drawTopperFoot(pdf *fpdf.Fpdf, t Topper, x0 float64) float64 {
	top := panelH - accentH - 12
	baseline := top + 7.5
	if len(t.Logo) > 0 {
		pdf.ImageOptions("topper-logo", x0+margin, baseline-6.5, 8, 8, false,
			fpdf.ImageOptions{ImageType: "PNG"}, 0, "")
	}
	pdf.SetFont(fontText, "", 9)
	if t.Site != "" {
		pdf.SetTextColor(mutedColor[0], mutedColor[1], mutedColor[2])
		right := strings.ToUpper(t.Site)
		pdf.Text(x0+panelW-margin-pdf.GetStringWidth(spaced(right)), baseline, spaced(right))
	}
	return top
}
