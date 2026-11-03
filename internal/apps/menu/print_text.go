package menu

import (
	"strings"

	"github.com/go-pdf/fpdf"
)

// drawSpaced writes letterspaced text and returns the width it drew.
//
// The headings on this menu are widely tracked, and fpdf has no letterspacing:
// it draws a string at a position. So the string is drawn a glyph at a time
// with the tracking added between them. Tracking is given in ems, as the
// design tool records it, so it scales with the font size rather than needing
// a new number at every size.
func drawSpaced(pdf *fpdf.Fpdf, x, baseline float64, s string, em, size float64) float64 {
	gap := em * size
	cur := x
	for _, r := range s {
		g := string(r)
		pdf.Text(cur, baseline, g)
		cur += pdf.GetStringWidth(g) + gap
	}
	if len(s) > 0 {
		cur -= gap // no tracking after the last glyph
	}
	return cur - x
}

// spacedWidth measures what drawSpaced would draw, for centring and for
// right-aligning.
func spacedWidth(pdf *fpdf.Fpdf, s string, em, size float64) float64 {
	if s == "" {
		return 0
	}
	w := pdf.GetStringWidth(s)
	return w + em*size*float64(len([]rune(s))-1)
}

// drawSpacedCentre centres letterspaced text on x.
func drawSpacedCentre(pdf *fpdf.Fpdf, centre, baseline float64, s string, em, size float64) {
	drawSpaced(pdf, centre-spacedWidth(pdf, s, em, size)/2, baseline, s, em, size)
}

// drawSpacedRight ends letterspaced text at x.
func drawSpacedRight(pdf *fpdf.Fpdf, right, baseline float64, s string, em, size float64) {
	drawSpaced(pdf, right-spacedWidth(pdf, s, em, size), baseline, s, em, size)
}

// capHeight is Archivo's cap height as a fraction of the em (686/1000, from
// the font's OS/2 table). Used to line
// two sizes up by what the eye actually sees, which is the capitals, not the
// invisible box the glyphs sit in.
const capHeight = 0.686

// capCentredBaseline returns the baseline that puts a run's capitals in the
// vertical middle of a box.
//
// Text does not sit in the middle of its own line box -- the ascender and
// descender space is uneven, and the capitals ride high in it. Placing a
// heading by a fraction of the bar's height gets this wrong in a way that is
// obvious once seen: the old section bars were tuned to 0.72 and printed with
// two units of air above the letters and ten below.
func capCentredBaseline(top, height, fontSize float64) float64 {
	return top + (height+fontSize*capHeight)/2
}

// centreText centres plain text on x.
func centreText(pdf *fpdf.Fpdf, centre, baseline float64, s string) {
	pdf.Text(centre-pdf.GetStringWidth(s)/2, baseline, s)
}

// rightText ends plain text at x.
func rightText(pdf *fpdf.Fpdf, right, baseline float64, s string) {
	pdf.Text(right-pdf.GetStringWidth(s), baseline, s)
}

// wrapText breaks a string to a width, in the font currently set.
//
// A word longer than the whole measure is left to overhang rather than being
// broken mid-word: it is almost always a URL or a hop name, and a hyphen
// inserted into either is worse than a line that runs a little wide.
func wrapText(pdf *fpdf.Fpdf, s string, width float64) []string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return nil
	}
	var (
		lines []string
		cur   = words[0]
	)
	for _, w := range words[1:] {
		if pdf.GetStringWidth(cur+" "+w) <= width {
			cur += " " + w
			continue
		}
		lines = append(lines, cur)
		cur = w
	}
	return append(lines, cur)
}

// rgb is a colour as fpdf takes it.
type rgb [3]int

func setText(pdf *fpdf.Fpdf, c rgb) { pdf.SetTextColor(c[0], c[1], c[2]) }
func setFill(pdf *fpdf.Fpdf, c rgb) { pdf.SetFillColor(c[0], c[1], c[2]) }
func setDraw(pdf *fpdf.Fpdf, c rgb) { pdf.SetDrawColor(c[0], c[1], c[2]) }
