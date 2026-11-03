package menu

import (
	"math"
	"strings"

	"github.com/go-pdf/fpdf"
)

// The featured beer: one boxed callout at the head of the first column.
//
// One, because a box works by being the only one -- three boxes and the eye
// has no reason to stop at any of them. At the head of the menu, because a
// menu is read top to bottom like a page, not in some sweet spot. The beer
// keeps its row in its own section too, marked with a star, so the section
// is still complete for someone reading it rather than the box.

// Featured is the beer the box pushes.
type Featured struct {
	Beer      Beer
	Label     string // why it is featured: "Brewer's pick", "Last keg"
	Blurb     string // the box's copy; the beer's description when empty
	Photo     []byte // optional, portrait, JPEG or PNG
	PhotoKind string
}

// Box geometry, in points.
const (
	featPad    = 9.0
	featLabelH = 14.0
	featPhotoW = 66.0
	featPhotoH = 99.0
	featNamePt = 14.0
	featBorder = 1.25
)

func (f *Featured) blurb() string {
	if b := strings.TrimSpace(f.Blurb); b != "" {
		return b
	}
	return f.Beer.Notes
}

// textWidth is the width the box's words get, beside the photo if any.
func (f *Featured) textWidth() float64 {
	w := colW - 2*featPad
	if len(f.Photo) > 0 {
		w -= featPhotoW + 10
	}
	return w
}

func featuredHeight(pdf *fpdf.Fpdf, f *Featured) float64 {
	pdf.SetFont(printBody, "", descPt)
	lines := len(wrapText(pdf, f.blurb(), f.textWidth()))
	text := nameH + 4 + metaH + float64(lines)*descLead
	if len(f.Photo) > 0 {
		text = max(text, featPhotoH)
	}
	return featPad + featLabelH + text + featPad + rowGap + 4
}

// drawFeatured draws the box: a gold frame, the reason in small capitals,
// the photo on the left if there is one, and the beer set a size up.
func drawFeatured(pdf *fpdf.Fpdf, x, y float64, f *Featured) {
	h := featuredHeight(pdf, f) - rowGap - 4
	setDraw(pdf, printGold)
	pdf.SetLineWidth(featBorder)
	pdf.RoundedRect(x, y, colW, h, 3, "1234", "D")

	label := "FEATURED"
	if l := strings.TrimSpace(f.Label); l != "" {
		label += " · " + strings.ToUpper(l)
	}
	setText(pdf, printAmber)
	pdf.SetFont(printBold, "", 7.5)
	drawSpaced(pdf, x+featPad+11, y+featPad+6.5, label, 0.14, 7.5)
	drawStar(pdf, x+featPad+4, y+featPad+3.9, 4.2)

	top := y + featPad + featLabelH
	tx := x + featPad
	if len(f.Photo) > 0 {
		pdf.ImageOptions("menu-print-featured", tx, top, featPhotoW, featPhotoH, false,
			fpdf.ImageOptions{ImageType: f.PhotoKind}, 0, "")
		tx += featPhotoW + 10
	}
	right := x + colW - featPad
	b := f.Beer
	base := top + featNamePt*0.8

	pour, _ := b.Headline()
	setText(pdf, printNavy)
	pdf.SetFont(printMedium, "", featNamePt)
	price := shortPrice(pour.Price)
	rightText(pdf, right, base, price)
	priceW := pdf.GetStringWidth(price)

	name := strings.ToUpper(b.Name)
	size := featNamePt
	pdf.SetFont(printSemi, "", size)
	for size > nameMinPt && pdf.GetStringWidth(name) > right-priceW-8-tx {
		size -= 0.5
		pdf.SetFont(printSemi, "", size)
	}
	pdf.Text(tx, base, clipTo(pdf, name, right-priceW-8-tx))

	cursor := top + nameH + 4
	drawMeta(pdf, tx, cursor+metaPt*0.8, joinDot(tidyStyle(b.Style), pourSize(b)), tidyABV(b.ABV))
	cursor += metaH
	setText(pdf, printNavy)
	pdf.SetFont(printBody, "", descPt)
	for i, line := range wrapText(pdf, f.blurb(), f.textWidth()) {
		pdf.Text(tx, cursor+descPt*0.8+float64(i)*descLead, line)
	}
}

// drawStar is a small five-pointed star in gold, centred on cx, cy. Drawn
// rather than set as a glyph: the embedded faces have no star.
func drawStar(pdf *fpdf.Fpdf, cx, cy, r float64) {
	pts := make([]fpdf.PointType, 0, 10)
	for i := range 10 {
		rad := r
		if i%2 == 1 {
			rad = r * 0.42
		}
		a := -90.0 + float64(i)*36
		pts = append(pts, fpdf.PointType{
			X: cx + rad*cosDeg(a),
			Y: cy + rad*sinDeg(a),
		})
	}
	setFill(pdf, printGold)
	pdf.Polygon(pts, "F")
}

func cosDeg(d float64) float64 { return math.Cos(d * math.Pi / 180) }
func sinDeg(d float64) float64 { return math.Sin(d * math.Pi / 180) }
