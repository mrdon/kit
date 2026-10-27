package events

import (
	"strings"

	"github.com/go-pdf/fpdf"
)

// The sign's event blocks: one event alone on the page, or one row of a
// busier day.

// signTimeColor is the rust band from the topper: the time is the thing a
// reader at the door looks for second, after the name.
var signTimeColor = topperBands[3]

// drawSignSolo gives one event the page: the poster large and centred, then
// the name, the time and its lines.
func drawSignSolo(pdf *fpdf.Fpdf, e SignEvent, top, bottom float64) {
	inner := signW - 2*signMargin
	y := top
	if e.Poster != nil {
		boxH := min((bottom-top)*0.68, 165)
		x, py, w, h := e.Poster.fitBox(signMargin, y, inner, boxH)
		pdf.ImageOptions(signPosterName(0), x, py, w, h, false, fpdf.ImageOptions{ImageType: "JPG"}, 0, "")
		y += boxH + 6
	}
	title := strings.ToUpper(e.Title)
	size := fitFontSize(pdf, fontDisplay, title, inner, 48)
	pdf.SetFont(fontDisplay, "", size)
	pdf.SetTextColor(inkColor[0], inkColor[1], inkColor[2])
	y += ptToMM(size) * 0.8
	signCentre(pdf, title, y)
	if e.Time != "" {
		pdf.SetFont(fontText, "", 26)
		pdf.SetTextColor(signTimeColor[0], signTimeColor[1], signTimeColor[2])
		y += ptToMM(26) * 1.2
		signCentre(pdf, spaced(strings.ToUpper(e.Time)), y)
	}
	pdf.SetFont(fontText, "", 20)
	pdf.SetTextColor(inkColor[0], inkColor[1], inkColor[2])
	lineH := ptToMM(20) * 1.3
	y += 4
	for _, line := range signLines(pdf, e.Lines, inner, int((bottom-y)/lineH)) {
		y += lineH
		signCentre(pdf, line, y)
	}
}

// drawSignRow draws one event of several: a colour rule, the poster, then
// the words beside it.
func drawSignRow(pdf *fpdf.Fpdf, e SignEvent, i int, y, h float64) {
	c := topperBands[i%len(topperBands)]
	pdf.SetFillColor(c[0], c[1], c[2])
	pdf.Rect(signMargin, y, 3, h, "F")

	textX := signMargin + 9
	if e.Poster != nil {
		boxW := min(h, 60.0)
		x, py, w, ph := e.Poster.fitBox(textX, y, boxW, h)
		pdf.ImageOptions(signPosterName(i), x, py, w, ph, false, fpdf.ImageOptions{ImageType: "JPG"}, 0, "")
		textX += boxW + 7
	}
	width := signW - signMargin - textX

	title := strings.ToUpper(e.Title)
	size := fitFontSize(pdf, fontDisplay, title, width, min(34, h*0.9))
	pdf.SetFont(fontDisplay, "", size)
	pdf.SetTextColor(inkColor[0], inkColor[1], inkColor[2])
	ty := y + ptToMM(size)*0.85
	pdf.Text(textX, ty, title)
	if e.Time != "" {
		pdf.SetFont(fontText, "", 20)
		pdf.SetTextColor(signTimeColor[0], signTimeColor[1], signTimeColor[2])
		ty += ptToMM(20) * 1.2
		pdf.Text(textX, ty, spaced(strings.ToUpper(e.Time)))
	}
	pdf.SetFont(fontText, "", 16)
	pdf.SetTextColor(inkColor[0], inkColor[1], inkColor[2])
	lineH := ptToMM(16) * 1.3
	for _, line := range signLines(pdf, e.Lines, width, int((y+h-ty)/lineH)) {
		ty += lineH
		pdf.Text(textX, ty, line)
	}
}

// signLines wraps an event's lines at the current font and keeps what fits
// in room lines, marking the last one when something was cut.
func signLines(pdf *fpdf.Fpdf, lines []string, width float64, room int) []string {
	var out []string
	for _, l := range lines {
		out = append(out, wrapToWidth(pdf, l, width)...)
	}
	if room <= 0 {
		return nil
	}
	if len(out) > room {
		out = out[:room]
		out[room-1] = clipWordsToWidth(pdf, out[room-1], width)
	}
	return out
}
