package menu

import (
	"fmt"
	"strings"

	"github.com/go-pdf/fpdf"
)

// The page furniture: the masthead, the running head, and the footer.
//
// Split from the rows because it changes for reasons unrelated to the tap
// list. Rows move when beers change; this moves when a venue rewrites its wifi
// password or swaps its logo, and keeping the two apart means neither edit has
// to read the other.

// Masthead geometry and type, in points.
const (
	mastPad      = 10.0 // band edge -> logo, and badge -> band edge
	mastLogoH    = 36.0
	brandPt      = 17.0
	brandMinPt   = 11.0
	brandEm      = 0.06
	mastSubPt    = 9.0
	mastSubEm    = 0.12
	badgeTitle   = "TRY A FLIGHT"
	badgeTitlePt = 10.5
	badgeLinePt  = 8.5
	badgeMaxW    = 170.0

	runLogoH = 18.0
	runBase  = 47.0
	runRuleY = 54.0

	footDefsPt = 8.0
	footDefsY  = 748.0
	footRuleY  = 756.0
	footLinePt = 7.5
	footLineY  = 770.0
)

// drawMasthead is page one's band: the venue's mark and name, the menu's own
// title beside them, and the flight badge on the right. It is the board's
// header, set on paper.
func (sh *sheet) drawMasthead() {
	pdf := sh.pdf
	if len(sh.m.Hero) > 0 {
		pdf.ImageOptions("menu-print-hero", marginX, mastTop, contentW, mastH, false,
			fpdf.ImageOptions{ImageType: sh.m.HeroKind}, 0, "")
		pdf.SetAlpha(0.8, "Normal")
	}
	setFill(pdf, printNavy)
	pdf.Rect(marginX, mastTop, contentW, mastH, "F")
	pdf.SetAlpha(1, "Normal")
	setFill(pdf, printGold)
	pdf.Rect(marginX, mastTop+mastH, contentW, mastRuleH, "F")

	x := marginX + mastPad
	if sh.logoAspect > 0 {
		w := mastLogoH * sh.logoAspect
		pdf.ImageOptions("menu-print-logo", x, mastTop+(mastH-mastLogoH)/2, w, mastLogoH,
			false, fpdf.ImageOptions{ImageType: "PNG"}, 0, "")
		x += w + 10
	}
	badgeX := sh.drawFlightBadge()
	sh.drawMastText(x, badgeX-12)
}

// drawMastText sets the name and the title line between the logo and the
// badge. The name gives way first, shrinking to a floor; the title line is
// dropped before anything overlaps.
func (sh *sheet) drawMastText(x, limit float64) {
	pdf := sh.pdf
	name, sub := mastWords(sh.m)
	base := capCentredBaseline(mastTop, mastH, brandPt)

	pdf.SetFont(printBody, "", mastSubPt)
	subW := spacedWidth(pdf, sub, mastSubEm, mastSubPt)
	size := brandPt
	for ; size > brandMinPt; size -= 0.5 {
		pdf.SetFont(printBold, "", size)
		if x+spacedWidth(pdf, name, brandEm, size)+12+subW <= limit {
			break
		}
	}
	setText(pdf, printWhite)
	pdf.SetFont(printBold, "", size)
	w := drawSpaced(pdf, x, base, name, brandEm, size)

	if sub == "" || x+w+12+subW > limit {
		return
	}
	setText(pdf, printPale)
	pdf.SetFont(printBody, "", mastSubPt)
	drawSpaced(pdf, x+w+12, base, sub, mastSubEm, mastSubPt)
}

// mastWords is what the band says: the venue's name large and the menu's
// title small beside it ("GRAVITY BREWING  BEERS & MORE"). With no name, the
// title takes the large slot and the subtitle the small one.
func mastWords(m PrintMenu) (string, string) {
	title := strings.TrimSpace(m.Title)
	sub := strings.TrimSpace(m.Subtitle)
	if brand := strings.TrimSpace(m.Brand); brand != "" {
		return strings.ToUpper(brand), strings.ToUpper(strings.TrimSpace(title + " " + sub))
	}
	return strings.ToUpper(title), strings.ToUpper(sub)
}

// drawFlightBadge is the gold badge at the right of the band, and returns its
// left edge so the name can stop short of it. The venue's own line goes under
// a fixed "Try a flight", shrunk to fit the badge's width.
func (sh *sheet) drawFlightBadge() float64 {
	pdf := sh.pdf
	line := strings.TrimSpace(sh.m.Flight)
	pdf.SetFont(printBold, "", badgeTitlePt)
	titleW := spacedWidth(pdf, badgeTitle, 0.08, badgeTitlePt)
	lineSize := badgeLinePt
	pdf.SetFont(printBody, "", lineSize)
	for lineSize > 6.5 && pdf.GetStringWidth(line) > badgeMaxW-20 {
		lineSize -= 0.5
		pdf.SetFont(printBody, "", lineSize)
	}
	w := max(titleW, pdf.GetStringWidth(line)) + 20
	h := mastLogoH
	x := marginX + contentW - mastPad - w
	y := mastTop + (mastH-h)/2

	setFill(pdf, printHot)
	pdf.RoundedRect(x, y, w, h, 3, "1234", "F")
	setText(pdf, printNavy)
	pdf.SetFont(printBold, "", badgeTitlePt)
	if line == "" {
		drawSpacedCentre(pdf, x+w/2, capCentredBaseline(y, h, badgeTitlePt), badgeTitle, 0.08, badgeTitlePt)
		return x
	}
	drawSpacedCentre(pdf, x+w/2, y+15, badgeTitle, 0.08, badgeTitlePt)
	pdf.SetFont(printBody, "", lineSize)
	centreText(pdf, x+w/2, y+28, clipTo(pdf, line, badgeMaxW-20))
	return x
}

// drawRunningHead is the one line at the top of pages two and up: the small
// mark and the name, and the flight on the right, over a gold rule. The band
// is a first-page event; repeating it would spend a sixth of every sheet on a
// reader who already knows what they are holding.
func (sh *sheet) drawRunningHead() {
	pdf := sh.pdf
	x := marginX
	if sh.logoAspect > 0 {
		w := runLogoH * sh.logoAspect
		pdf.ImageOptions("menu-print-logo", x, runBase-runLogoH+4, w, runLogoH,
			false, fpdf.ImageOptions{ImageType: "PNG"}, 0, "")
		x += w + 8
	}
	name, sub := mastWords(sh.m)
	setText(pdf, printNavy)
	pdf.SetFont(printBold, "", 10)
	w := drawSpaced(pdf, x, runBase, name, brandEm, 10)
	if sub != "" {
		setText(pdf, printMuted)
		pdf.SetFont(printBody, "", 7.5)
		drawSpaced(pdf, x+w+10, runBase, sub, mastSubEm, 7.5)
	}
	if line := strings.TrimSpace(sh.m.Flight); line != "" {
		setText(pdf, printMuted)
		pdf.SetFont(printBody, "", 7.5)
		drawSpacedRight(pdf, marginX+contentW, runBase,
			strings.ToUpper("Try a flight: "+line), 0.08, 7.5)
	}
	setFill(pdf, printGold)
	pdf.Rect(marginX, runRuleY, contentW, headRuleH, "F")
}

// drawFooter is the key line, a rule, and the house lines with the page count
// between them. The key carries the pour-size note, and the GR caveat when a
// GR badge is on the menu: every page gets it, because a sheet separated from
// its partner still has to explain its own marks.
func (sh *sheet) drawFooter(page, pages int) {
	pdf := sh.pdf
	sh.drawFooterKey()

	setDraw(pdf, printNavy)
	pdf.SetLineWidth(0.75)
	pdf.Line(marginX, footRuleY, marginX+contentW, footRuleY)

	setText(pdf, printMuted)
	pdf.SetFont(printBody, "", footLinePt)
	if s := strings.TrimSpace(sh.m.FootLeft); s != "" {
		drawSpaced(pdf, marginX, footLineY, s, 0.1, footLinePt)
	}
	if pages > 1 {
		label := fmt.Sprintf("PAGE %d OF %d", page, pages)
		drawSpacedCentre(pdf, pageW/2, footLineY, label, 0.1, footLinePt)
	}
	if s := strings.TrimSpace(sh.m.FootRight); s != "" {
		drawSpacedRight(pdf, marginX+contentW, footLineY, s, 0.1, footLinePt)
	}
}

// drawFooterKey centres "16oz unless marked · [GR] the caveat" on one line,
// shrinking it before it would run past the margins.
func (sh *sheet) drawFooterKey() {
	pdf := sh.pdf
	sizes := strings.TrimSpace(sh.m.Sizes)
	if sizes == "" && !sh.anyGR {
		return
	}
	size := footDefsPt
	var width float64
	for ; size >= 6; size -= 0.25 {
		width = sh.footerKeyWidth(sizes, size)
		if width <= contentW {
			break
		}
	}
	x := pageW/2 - width/2
	setText(pdf, printNavy)
	pdf.SetFont(printBody, "", size)
	if sizes != "" {
		pdf.Text(x, footDefsY, sizes)
		x += pdf.GetStringWidth(sizes)
		if sh.anyGR {
			pdf.Text(x, footDefsY, "  ·  ")
			x += pdf.GetStringWidth("  ·  ")
		}
	}
	if sh.anyGR {
		x += drawBadge(pdf, x, footDefsY, badgeGR, size) + 4
		setText(pdf, printNavy)
		pdf.SetFont(printBody, "", size)
		pdf.Text(x, footDefsY, GlutenReducedNote)
	}
}

func (sh *sheet) footerKeyWidth(sizes string, size float64) float64 {
	pdf := sh.pdf
	pdf.SetFont(printBody, "", size)
	w := pdf.GetStringWidth(sizes)
	if sh.anyGR {
		if sizes != "" {
			w += pdf.GetStringWidth("  ·  ")
		}
		w += pdf.GetStringWidth(GlutenReducedNote) + 4
		w += badgeWidth(pdf, badgeGR)
	}
	return w
}
