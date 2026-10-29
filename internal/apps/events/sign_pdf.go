package events

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/go-pdf/fpdf"
)

// Drawing the entrance sign: a portrait Letter page, in the topper's type and
// colours so the stand and the tables read as one set.
//
// The page is measured the topper's way. The heading and footer take what
// they need, the events share what is left, and text is fitted into each
// event's share. One event gets the page to itself with its poster large;
// two to four stack as rows with the poster beside the words.

const (
	signW      = 215.9
	signH      = 279.4
	signMargin = 16.0
	signAccent = 6.0
	signGap    = 6.0
	// signRowMaxH stops a two-event day stretching each row into a slab.
	signRowMaxH = 70.0
)

// RenderSignPDF writes the entrance sign.
func RenderSignPDF(s Sign, out io.Writer) error {
	pdf, err := newSignPDF()
	if err != nil {
		return err
	}
	for i, e := range s.Events {
		registerSignImage(pdf, signPosterName(i), e.Poster)
	}
	if len(s.Logo) > 0 {
		pdf.RegisterImageOptionsReader("sign-logo", fpdf.ImageOptions{ImageType: "PNG"}, bytes.NewReader(s.Logo))
	}
	clearImageError(pdf)

	pdf.SetFillColor(accentColor[0], accentColor[1], accentColor[2])
	pdf.Rect(0, 0, signW, signAccent, "F")
	pdf.Rect(0, signH-signAccent, signW, signAccent, "F")
	top := drawSignHead(pdf, s)
	bottom := drawSignFoot(pdf, s)
	drawSignEvents(pdf, s.Events, top, bottom)
	return writeSignPDF(pdf, out)
}

// RenderSignPosterPDF writes one poster as the whole sign, as large as the
// page allows with its shape kept.
func RenderSignPosterPDF(img *signImage, out io.Writer) error {
	pdf, err := newSignPDF()
	if err != nil {
		return err
	}
	registerSignImage(pdf, "sign-poster", img)
	if err := pdf.Error(); err != nil {
		return fmt.Errorf("loading poster: %w", err)
	}
	const m = 8.0
	x, y, w, h := img.fitBox(m, m, signW-2*m, signH-2*m)
	pdf.ImageOptions("sign-poster", x, y, w, h, false, fpdf.ImageOptions{ImageType: "JPG"}, 0, "")
	return writeSignPDF(pdf, out)
}

func newSignPDF() (*fpdf.Fpdf, error) {
	pdf := fpdf.NewCustom(&fpdf.InitType{UnitStr: "mm", Size: fpdf.SizeType{Wd: signW, Ht: signH}})
	if err := loadTopperFonts(pdf); err != nil {
		return nil, err
	}
	pdf.SetAutoPageBreak(false, 0)
	pdf.SetMargins(0, 0, 0)
	pdf.AddPage()
	return pdf, nil
}

func writeSignPDF(pdf *fpdf.Fpdf, out io.Writer) error {
	if err := pdf.Error(); err != nil {
		return fmt.Errorf("composing sign: %w", err)
	}
	if err := pdf.Output(out); err != nil {
		return fmt.Errorf("writing sign pdf: %w", err)
	}
	return nil
}

func registerSignImage(pdf *fpdf.Fpdf, name string, img *signImage) {
	if img != nil {
		pdf.RegisterImageOptionsReader(name, fpdf.ImageOptions{ImageType: "JPG"}, bytes.NewReader(img.Data))
	}
}

// clearImageError drops a bad picture rather than the sheet; the draw code
// skips anything that failed to register.
func clearImageError(pdf *fpdf.Fpdf) {
	if pdf.Error() != nil {
		pdf.ClearError()
	}
}

func signPosterName(i int) string { return fmt.Sprintf("sign-poster-%d", i) }

// signCentre draws s centred across the page.
func signCentre(pdf *fpdf.Fpdf, s string, baseline float64) {
	pdf.Text((signW-pdf.GetStringWidth(s))/2, baseline, s)
}

// drawSignHead draws the heading and returns where the events start. No date:
// the same lineup on another day reuses the sheet.
func drawSignHead(pdf *fpdf.Fpdf, s Sign) float64 {
	inner := signW - 2*signMargin
	pdf.SetTextColor(inkColor[0], inkColor[1], inkColor[2])
	head := strings.ToUpper(s.Heading)
	size := fitFontSize(pdf, fontDisplay, head, inner, 58)
	pdf.SetFont(fontDisplay, "", size)
	y := signAccent + 10 + ptToMM(size)*0.75
	signCentre(pdf, head, y)
	return y + 9
}

// drawSignFoot draws the logo, the site and the "Also today" line, and
// returns where the events must end.
func drawSignFoot(pdf *fpdf.Fpdf, s Sign) float64 {
	top := signH - signAccent - 22
	baseline := top + 14
	if len(s.Logo) > 0 {
		pdf.ImageOptions("sign-logo", signMargin, baseline-11, 14, 14, false, fpdf.ImageOptions{ImageType: "PNG"}, 0, "")
	}
	pdf.SetFont(fontText, "", 14)
	pdf.SetTextColor(mutedColor[0], mutedColor[1], mutedColor[2])
	if s.Site != "" {
		site := spaced(strings.ToUpper(s.Site))
		pdf.Text(signW-signMargin-pdf.GetStringWidth(site), baseline, site)
	}
	if len(s.Also) == 0 {
		return top
	}
	pdf.SetFont(fontText, "", 16)
	lines := wrapToWidth(pdf, "Also today: "+strings.Join(s.Also, "  ·  "), signW-2*signMargin)
	lineH := ptToMM(16) * 1.25
	y := top - 4 - lineH*float64(len(lines)-1)
	for _, line := range lines {
		signCentre(pdf, line, y)
		y += lineH
	}
	return top - 8 - lineH*float64(len(lines))
}

// drawSignEvents shares the space between the head and the foot.
func drawSignEvents(pdf *fpdf.Fpdf, evs []SignEvent, top, bottom float64) {
	switch len(evs) {
	case 0:
		pdf.SetTextColor(mutedColor[0], mutedColor[1], mutedColor[2])
		pdf.SetFont(fontText, "", 22)
		signCentre(pdf, "Nothing on the calendar today.", top+(bottom-top)/2)
	case 1:
		drawSignSolo(pdf, evs[0], top, bottom)
	default:
		n := float64(len(evs))
		h := min((bottom-top-signGap*(n-1))/n, signRowMaxH)
		y := top + (bottom-top-(h*n+signGap*(n-1)))/2
		for i, e := range evs {
			drawSignRow(pdf, e, i, y, h)
			y += h + signGap
		}
	}
}
