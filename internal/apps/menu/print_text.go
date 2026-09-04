package menu

import (
	"strconv"
	"strings"

	"github.com/go-pdf/fpdf"
)

// Measurements are written in the design's own units and converted here.
//
// The layout was lifted from a 816x1056 artboard -- US Letter at 96dpi -- and
// every number below reads the same in the code as it does in the design file.
// Converting at the point of use, rather than re-expressing 40 constants in
// points, means a measurement taken off the original can be pasted straight
// in and checked by eye.
const designScale = 0.75 // 96dpi design units -> PDF points

// d converts a design unit to points.
func d(v float64) float64 { return v * designScale }

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

// capHeight is Montserrat's cap height as a fraction of the em. Used to line
// two sizes up by what the eye actually sees, which is the capitals, not the
// invisible box the glyphs sit in.
const capHeight = 0.70

// alignedBaseline returns the baseline a smaller run needs so its capitals are
// centred against a larger run's, rather than sitting on the same line.
//
// Two sizes sharing a baseline look bottom-aligned, because the smaller text's
// optical centre falls below the larger's by half the difference in cap height.
// It is a few points, and it is the difference between a lockup that looks
// deliberate and one that looks like it slipped.
func alignedBaseline(bigBaseline, bigSize, smallSize float64) float64 {
	return bigBaseline - (bigSize-smallSize)*capHeight/2
}

// capCentredBaseline returns the baseline that puts a run's capitals in the
// vertical middle of a box.
//
// Text does not sit in the middle of its own line box -- the ascender and
// descender space is uneven, and the capitals ride high in it. Placing a
// heading by a fraction of the bar's height gets this wrong in a way that is
// obvious once seen: the section bars were tuned to 0.72 and printed with two
// units of air above the letters and ten below.
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

// hexRGB reads a #rrggbb colour. An unparseable value falls back to black
// rather than failing the document: a wrong colour is a visible mistake
// somebody can fix, and a menu that will not print is not.
func hexRGB(s string) (int, int, int) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 6 {
		return 0, 0, 0
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, 0, 0
	}
	return int(v>>16) & 0xff, int(v>>8) & 0xff, int(v) & 0xff
}
