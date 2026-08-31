package events

import (
	"strings"

	"github.com/go-pdf/fpdf"
)

// The bands: one coloured row per occurrence, and the text fitting that keeps
// them readable whatever anyone typed into the event form.
//
// The sizing rule is that the WEEK decides the type size, not the copy. Five
// events give tall bands and big titles; seven give short ones. Text is then
// fitted into whatever the band turned out to be, because the alternative --
// letting a long title set the layout -- produces a different-looking card
// every week.

// Band metrics as fractions of band height, so every size scales together when
// the week is busier.
const (
	bandGap  = 3.0
	bandMaxH = 25.0
	dayColW  = 23.0
	// Two vertical placements for the day block, with and without a time
	// underneath it.
	dayBaselineTimed = 0.60
	dayRuleTimed     = 0.71
	dayBaselinePlain = 0.70
	dayRulePlain     = 0.81
	timeBaseline     = 0.93
)

// drawBands divides the space between the header and the footer.
func drawBands(pdf *fpdf.Fpdf, rows []TopperRow, x0, top, height float64) {
	if len(rows) == 0 {
		drawQuietWeek(pdf, x0, top, height)
		return
	}
	n := float64(len(rows))
	h := (height - bandGap*(n-1)) / n
	if h > bandMaxH {
		h = bandMaxH
	}
	x, w := x0+margin, panelW-2*margin
	// One bullet size for the whole panel, taken from the band that can afford
	// the least. Sizing each band independently makes the card look like five
	// posters stacked up rather than one; a reader's eye reads the variation as
	// emphasis that is not there.
	bulletPt := uniformBulletSize(pdf, rows, x, w, h)

	// Centre the stack when a light week leaves slack, rather than letting the
	// bands hang off the header with a gap above the footer.
	y := top + (height-(h*n+bandGap*(n-1)))/2
	for i, row := range rows {
		drawBand(pdf, row, i, x, y, w, h, bulletPt)
		y += h + bandGap
	}
}

// uniformBulletSize is the largest size every band can carry.
func uniformBulletSize(pdf *fpdf.Fpdf, rows []TopperRow, x, w, h float64) float64 {
	smallest := max(h*0.40, 7)
	for _, row := range rows {
		if len(row.Bullets) == 0 {
			continue
		}
		textX, textRight := bandTextSpan(x, w, h, row.Poster != nil)
		_, avail := bandTextTop(pdf, row, textRight-textX, h)
		size, _ := fitBullets(pdf, row.Bullets, textRight-textX, avail, h)
		smallest = min(smallest, size)
	}
	return smallest
}

// drawQuietWeek is what an empty week prints. It says so plainly: a blank card
// looks like the printer failed, and someone would go looking for the bug.
func drawQuietWeek(pdf *fpdf.Fpdf, x0, top, height float64) {
	pdf.SetTextColor(mutedColor[0], mutedColor[1], mutedColor[2])
	pdf.SetFont(fontText, "", 12)
	centreText(pdf, x0, "Nothing on the calendar this week.", top+height/2)
}

// drawBand draws one row: the day block, the title and bullets, and the
// event's own poster in a circle on the right.
func drawBand(pdf *fpdf.Fpdf, row TopperRow, i int, x, y, w, h, bulletPt float64) {
	c := topperBands[i%len(topperBands)]
	pdf.SetFillColor(c[0], c[1], c[2])
	pdf.Rect(x, y, w, h, "F")
	pdf.SetTextColor(255, 255, 255)

	if row.Poster != nil {
		d := h - 4.4
		drawPosterCircle(pdf, posterName(i), x+w-3-d, y+2.2, d)
	}
	textX, textRight := bandTextSpan(x, w, h, row.Poster != nil)
	drawDayBlock(pdf, row, x+3.6, y, h)
	drawBandText(pdf, row, textX, textRight, y, h, bulletPt)
}

// bandTextSpan is the horizontal room left for the title and bullets once the
// day block and, if there is one, the poster coin have taken theirs. One
// function so the measuring pass and the drawing pass cannot disagree.
func bandTextSpan(x, w, h float64, hasPoster bool) (float64, float64) {
	right := x + w - 3
	if hasPoster {
		right -= (h - 4.4) + 3
	}
	return x + dayColW, right
}

// drawDayBlock is the day, a rule, and the door time -- the part read from
// furthest away, so it gets the largest type on the card.
//
// The block centres itself: with a door time it sits high enough to leave the
// time room underneath, without one it drops to the middle of the band rather
// than leaving a hole where the time would have been.
func drawDayBlock(pdf *fpdf.Fpdf, row TopperRow, x, y, h float64) {
	baseline, rule := h*dayBaselineTimed, h*dayRuleTimed
	if row.Time == "" {
		baseline, rule = h*dayBaselinePlain, h*dayRulePlain
	}
	size := fitFontSize(pdf, fontDisplay, row.Day, dayColW-6, h*1.9)
	pdf.SetFont(fontDisplay, "", size)
	pdf.Text(x, y+baseline, row.Day)
	width := pdf.GetStringWidth(row.Day)

	pdf.SetDrawColor(255, 255, 255)
	pdf.SetLineWidth(h * 0.035)
	pdf.Line(x, y+rule, x+width, y+rule)

	if row.Time != "" {
		pdf.SetFont(fontText, "", max(h*0.42, 7))
		pdf.Text(x, y+h*timeBaseline, strings.ToUpper(row.Time))
	}
}

// drawBandText fits the title and bullets into whatever width is left after
// the day block and the poster have taken theirs.
//
// The title is shrunk to fit its own band; the bullets are wrapped and drawn
// at the size the whole panel agreed on. Wrapping rather than clipping matters
// because a clipped bullet drops information the person writing the event
// thought was worth saying.
func drawBandText(pdf *fpdf.Fpdf, row TopperRow, x, right, y, h, bulletPt float64) {
	w := right - x
	if w < 15 {
		return
	}
	drawBandTitle(pdf, row.Title, x, y, w, h)
	top, avail := bandTextTop(pdf, row, w, h)

	pdf.SetFont(fontText, "", bulletPt)
	lines := bulletLines(pdf, row.Bullets, w)
	lineH := ptToMM(bulletPt) * 1.22
	if n := int(avail / lineH); n < len(lines) {
		lines = lines[:max(n, 0)]
	}
	baseline := y + top + lineH
	for _, line := range lines {
		pdf.Text(x+line.indent, baseline, line.text)
		baseline += lineH
	}
}

// drawBandTitle draws the event name at the largest size its band can hold.
func drawBandTitle(pdf *fpdf.Fpdf, title string, x, y, w, h float64) {
	t := strings.ToUpper(title)
	pdf.SetFont(fontDisplay, "", fitFontSize(pdf, fontDisplay, t, w, h*0.95))
	pdf.Text(x, y+h*0.42, t)
}

// bandTextTop reports where the bullets start (relative to the band top) and
// how much vertical room they have. It re-fits the title rather than taking it
// as an argument so the measuring pass can call it before anything is drawn.
func bandTextTop(pdf *fpdf.Fpdf, row TopperRow, w, h float64) (float64, float64) {
	titleSize := fitFontSize(pdf, fontDisplay, strings.ToUpper(row.Title), w, h*0.95)
	top := h*0.42 + ptToMM(titleSize)*0.28
	return top, h - h*0.08 - top
}

// drawPosterCircle masks the event's artwork into a coin on the band.
//
// The thumbnail is already a square (see topper_image.go), so the mask crops
// symmetrically and nothing has to be scaled by aspect here.
func drawPosterCircle(pdf *fpdf.Fpdf, name string, x, y, d float64) {
	// White underneath: a JPEG that fails to draw leaves a coin rather than a
	// hole in the band, which reads as intentional.
	pdf.SetFillColor(255, 255, 255)
	pdf.Circle(x+d/2, y+d/2, d/2, "F")

	pdf.ClipCircle(x+d/2, y+d/2, d/2, false)
	pdf.ImageOptions(name, x, y, d, d, false, fpdf.ImageOptions{ImageType: "JPG"}, 0, "")
	pdf.ClipEnd()
}
