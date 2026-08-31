package events

import (
	"strings"

	"github.com/go-pdf/fpdf"
)

// Text fitting helpers.
//
// A poster has fixed boxes and variable copy, so every string on the card goes
// through one of these: shrink it, clip it, or centre it. None of them is
// clever -- the point is that the failure mode is always "slightly smaller
// type" rather than "title runs off the card".

// minFontPt is the floor. Below this the line is not readable across a table,
// so it is better to clip the string than to keep shrinking it.
const minFontPt = 6.0

// Bullet type has a higher floor than the rest of the card and a hard line
// budget, because the two failure modes are not symmetric. Detail set at 6pt
// is present on the page but unreadable from a chair, which is the same as
// absent -- except that it also crowds the title it sits under. So the detail
// keeps its size and loses lines instead: a band shows the first few lines at
// a size that carries across a table, ellipsised so the cut reads as a
// decision, and the website carries the rest.
const (
	minBulletPt = 8.0
	// maxBulletLines is the ceiling for one band regardless of how much room
	// the band happens to have. Without it a light week -- tall bands, few of
	// them -- prints a paragraph under every title, which is a flyer, not a
	// table topper.
	maxBulletLines = 3
)

// maxBulletSize is the largest detail type a band of this height may use.
// Proportional so a busy week scales down together, with a floor that keeps a
// seven-day week legible rather than merely fitted.
func maxBulletSize(bandH float64) float64 { return max(bandH*0.46, 9) }

// ptToMM converts a font size to page units. fpdf takes sizes in points
// regardless of the document's unit, so any vertical maths against a font size
// has to cross this boundary.
func ptToMM(pt float64) float64 { return pt * 25.4 / 72 }

// fitFontSize returns the largest size at or below want that fits s into
// width. Steps down by a point at a time: exact enough for type this size, and
// it keeps sibling bands at the same size instead of each landing on its own
// fractional value.
func fitFontSize(pdf *fpdf.Fpdf, family, s string, width, want float64) float64 {
	for size := want; size > minFontPt; size-- {
		pdf.SetFont(family, "", size)
		if pdf.GetStringWidth(s) <= width {
			return size
		}
	}
	return minFontPt
}

// clipToWidth trims a string to fit, ending in an ellipsis. Used where
// shrinking is not an option because the line shares its size with siblings.
func clipToWidth(pdf *fpdf.Fpdf, s string, width float64) string {
	if pdf.GetStringWidth(s) <= width {
		return s
	}
	r := []rune(s)
	for len(r) > 1 {
		r = r[:len(r)-1]
		if pdf.GetStringWidth(string(r)+"…") <= width {
			return strings.TrimRight(string(r), " ,;:") + "…"
		}
	}
	return ""
}

// centreText draws s centred in the panel starting at x0.
func centreText(pdf *fpdf.Fpdf, x0 float64, s string, baseline float64) {
	pdf.Text(x0+(panelW-pdf.GetStringWidth(s))/2, baseline, s)
}

// spaced letterspaces a string the poster way, by inserting real spaces. PDF
// text can carry a character-spacing parameter, but fpdf does not expose it,
// and for a line of caps this is indistinguishable in print.
func spaced(s string) string {
	var b strings.Builder
	for _, r := range s {
		b.WriteRune(r)
		b.WriteRune(' ')
	}
	return strings.TrimRight(b.String(), " ")
}

// bandLine is one rendered line of bullet text. Continuation lines of a
// wrapped bullet carry an indent so they hang under the text rather than under
// the dot.
type bandLine struct {
	text   string
	indent float64
}

// fitBullets wraps the bullets and picks the largest size whose wrapped form
// fits in avail. Returns the size to set and the lines to draw.
//
// Wrapping and sizing cannot be separated: a smaller size fits more characters
// per line, so it may need fewer lines, so it may fit where the larger size
// needed one line too many. Hence the re-wrap inside the loop.
func fitBullets(pdf *fpdf.Fpdf, bullets []string, w, avail, bandH float64) (float64, []bandLine) {
	if len(bullets) == 0 || avail <= 0 {
		return minBulletPt, nil
	}
	var lines []bandLine
	size := maxBulletSize(bandH)
	for ; size > minBulletPt; size-- {
		pdf.SetFont(fontText, "", size)
		lines = clampBullets(pdf, bulletLines(pdf, bullets, w), w, maxBulletLines)
		if float64(len(lines))*ptToMM(size)*1.22 <= avail {
			return size, lines
		}
	}
	// At the floor, drop whole lines rather than shrinking into illegibility.
	pdf.SetFont(fontText, "", minBulletPt)
	lines = bulletLines(pdf, bullets, w)
	return minBulletPt, clampBullets(pdf, lines, w, int(avail/(ptToMM(minBulletPt)*1.22)))
}

// clampBullets trims the wrapped detail to whatever the band will actually
// print -- the smaller of the caller's room and the hard line budget -- and
// ellipsises what survives so a truncated band reads as edited rather than
// broken off.
func clampBullets(pdf *fpdf.Fpdf, lines []bandLine, w float64, room int) []bandLine {
	room = max(min(room, maxBulletLines), 0)
	if len(lines) <= room {
		return lines
	}
	if room == 0 {
		return nil
	}
	lines = lines[:room]
	last := &lines[len(lines)-1]
	last.text = clipToWidth(pdf, last.text+" \u2026", w-last.indent)
	return lines
}

// bulletLines renders the bullets at the current font size as drawable lines.
func bulletLines(pdf *fpdf.Fpdf, bullets []string, w float64) []bandLine {
	const dot = "• "
	indent := pdf.GetStringWidth(dot)
	var out []bandLine
	for _, b := range bullets {
		wrapped := wrapToWidth(pdf, strings.ToUpper(b), w-indent)
		for i, line := range wrapped {
			if i == 0 {
				out = append(out, bandLine{text: dot + line})
				continue
			}
			out = append(out, bandLine{text: line, indent: indent})
		}
	}
	return out
}

// wrapToWidth is a greedy word wrap at the current font size. A single word
// too long for the line is clipped rather than broken: hyphenating a brewery's
// name mid-word looks like a bug, and it only happens to URLs in practice.
func wrapToWidth(pdf *fpdf.Fpdf, s string, w float64) []string {
	var out []string
	line := ""
	for word := range strings.FieldsSeq(s) {
		candidate := word
		if line != "" {
			candidate = line + " " + word
		}
		if pdf.GetStringWidth(candidate) <= w {
			line = candidate
			continue
		}
		if line != "" {
			out = append(out, line)
		}
		line = clipToWidth(pdf, word, w)
	}
	if line != "" {
		out = append(out, line)
	}
	return out
}
