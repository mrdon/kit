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

// bulletOverhang is how far past its bottom margin a line of detail may sit,
// as a fraction of a line.
//
// The margin is comfort space rather than the edge of the band, and a line's
// height is mostly leading -- 22% of it is air below the letters, not ink. So
// a line that misses the budget by a fraction still prints clear of the band
// edge, while refusing it costs the band a whole line.
//
// That cost is not abstract: a band missing by six hundredths of a millimetre
// dropped the line naming the other event on that day, and printed a Monday
// with nothing on it but a pizza offer.
const bulletOverhang = 0.25

// bulletRoom is how many lines of detail fit in avail at this line height.
func bulletRoom(avail, lineH float64) int {
	if lineH <= 0 {
		return 0
	}
	return max(int((avail+lineH*bulletOverhang)/lineH), 0)
}

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

// clipWordsToWidth trims a line to fit by dropping whole words, ending in an
// ellipsis.
//
// Words rather than characters because this runs on the last line a band
// prints, where the cut is what a customer reads: "ON ANY SOURDOU…" looks like
// a printer that gave up, while "ON ANY …" looks like a line that was edited.
// The marker is measured against the untrimmed line first, so a cut costs at
// most the one word that would not fit -- never a whole clause.
//
// A line with nothing left to drop is returned unmarked: it already fits, and
// an unmarked line beats a bullet sitting alone next to an ellipsis.
func clipWordsToWidth(pdf *fpdf.Fpdf, s string, width float64) string {
	const marker = " …"
	if pdf.GetStringWidth(s+marker) <= width {
		return s + marker
	}
	words := strings.Fields(s)
	for len(words) > 2 || (len(words) == 2 && words[0] != bulletDot) {
		words = words[:len(words)-1]
		line := strings.TrimRight(strings.Join(words, " "), " ,;:-")
		if pdf.GetStringWidth(line+marker) <= width {
			return line + marker
		}
	}
	return s
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
	// pinned marks a line that names another event on the same day. When the
	// band cannot print everything it has, these are what it keeps: a second
	// thing on tonight changes whether someone comes in, and a third adjective
	// about the first thing does not.
	pinned bool
}

// fitBullets wraps the bullets and picks the largest size whose wrapped form
// fits in avail. Returns the size to set and the lines to draw.
//
// Wrapping and sizing cannot be separated: a smaller size fits more characters
// per line, so it may need fewer lines, so it may fit where the larger size
// needed one line too many. Hence the re-wrap inside the loop.
func fitBullets(pdf *fpdf.Fpdf, bullets []string, supports int, w, avail, bandH float64) (float64, []bandLine) {
	if len(bullets) == 0 || avail <= 0 {
		return minBulletPt, nil
	}
	var lines []bandLine
	size := maxBulletSize(bandH)
	for ; size > minBulletPt; size-- {
		pdf.SetFont(fontText, "", size)
		lineH := ptToMM(size) * 1.22
		lines = clampBullets(pdf, bulletLines(pdf, bullets, supports, w), w, maxBulletLines)
		if len(lines) <= bulletRoom(avail, lineH) {
			return size, lines
		}
	}
	// At the floor, drop whole lines rather than shrinking into illegibility.
	pdf.SetFont(fontText, "", minBulletPt)
	lines = bulletLines(pdf, bullets, supports, w)
	return minBulletPt, clampBullets(pdf, lines, w, bulletRoom(avail, ptToMM(minBulletPt)*1.22))
}

// clampBullets trims the wrapped detail to whatever the band will actually
// print -- the smaller of the caller's room and the hard line budget -- and
// ellipsises what survives when the cut lands mid-sentence, so a band broken
// off in the middle of a clause reads as edited rather than as a fault.
//
// The cut comes out of the headliner's own detail. Lines naming another event
// on the same day are kept: bandBullets already decided they were worth a slot
// on the reasoning that a second thing on tonight is news, and it budgeted in
// bullets while this budgets in wrapped lines. A two-line headliner bullet
// used to silently overrun that and print a quiet Friday that actually had a
// beer launch on it.
func clampBullets(pdf *fpdf.Fpdf, lines []bandLine, w float64, room int) []bandLine {
	room = max(min(room, maxBulletLines), 0)
	if len(lines) <= room {
		return lines
	}
	if room == 0 {
		return nil
	}
	own, pinned := splitPinned(lines)
	// Never below the headliner's opening line: a title with nothing under it
	// but another event's name reads as the wrong event on the band.
	if len(pinned) > room-1 {
		pinned = pinned[:room-1]
	}
	keep := max(room-len(pinned), 1)
	if keep >= len(own) {
		return append(own, pinned...)
	}
	kept := own[:keep]
	last := &kept[len(kept)-1]
	if cutMidSentence(*last, own[keep]) {
		last.text = clipWordsToWidth(pdf, last.text, w-last.indent)
	}
	return append(kept, pinned...)
}

// splitPinned divides the lines at the start of the pinned tail. Pinned lines
// are always last because bandBullets appends the support acts after the
// headliner's own detail.
func splitPinned(lines []bandLine) (own, pinned []bandLine) {
	i := len(lines)
	for i > 0 && lines[i-1].pinned {
		i--
	}
	return lines[:i], lines[i:]
}

// cutMidSentence reports whether dropping next leaves the last printed line
// hanging, which is the only case the ellipsis is for.
//
// Two cuts are clean. One is a line that ends a sentence: a full stop followed
// by "…" reads as a printing fault, not as an edit, and the reader has already
// been told the thought finished. The other is a line that ends its bullet --
// what follows opens with its own dot, so the break is visible in the layout
// and a marker only says again what the missing dot already says.
//
// What is left is a line cut in the middle of a clause, where nothing on the
// card would otherwise show that words were taken out.
func cutMidSentence(last, next bandLine) bool {
	if next.indent == 0 {
		// The next line opens a new bullet, so the kept text is whole.
		return false
	}
	return !endsClosed(last.text)
}

// endsClosed reports whether a line comes to a stop of its own -- a sentence
// end, or an ellipsis a narrower cut already put there. Trailing quotes and
// brackets are stepped over so ("like this.") still counts. A colon does not:
// a line ending in one is promising more, which is precisely when the reader
// needs telling that the rest is elsewhere.
func endsClosed(s string) bool {
	s = strings.TrimRight(strings.TrimSpace(s), `"')]`)
	if s == "" {
		return false
	}
	switch s[len(s)-1] {
	case '.', '!', '?':
		return true
	}
	return strings.HasSuffix(s, "\u2026")
}

// bulletDot opens a bullet's first line. Named because clipWordsToWidth has to
// recognise it: a line trimmed back to the dot alone prints a band that looks
// like it lost its text, rather than one that was cut short.
const bulletDot = "•"

// bulletLines renders the bullets at the current font size as drawable lines.
// The last supports of them name other events on the day, and their lines are
// marked so the clamp knows not to cut them first.
func bulletLines(pdf *fpdf.Fpdf, bullets []string, supports int, w float64) []bandLine {
	dot := bulletDot + " "
	indent := pdf.GetStringWidth(dot)
	pinnedFrom := len(bullets) - max(supports, 0)
	var out []bandLine
	for n, b := range bullets {
		wrapped := wrapToWidth(pdf, strings.ToUpper(b), w-indent)
		for i, line := range wrapped {
			l := bandLine{text: line, indent: indent, pinned: n >= pinnedFrom}
			if i == 0 {
				l.text, l.indent = dot+line, 0
			}
			out = append(out, l)
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
