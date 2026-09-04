package menu

import (
	"strings"

	"github.com/go-pdf/fpdf"
)

// The page furniture: the masthead, the running head, and the footer.
//
// Split from print_pdf.go because it is the half that changes for a reason
// unrelated to the tap list. Rows move when beers change; this moves when a
// venue rewrites its wifi password or puts a new photograph at the top, and
// keeping the two apart means neither edit has to read the other.

// drawMasthead is the first page's title block: the photograph, a wash of
// colour over it so white type stays readable whatever the picture is, the
// wordmark and the title.
//
// Title and subtitle share one baseline, the title at the left margin and the
// subtitle at the right. Stacking them put the subtitle alone in the corner
// with a wedge of empty colour above it; across one line the band holds the
// full width and needs a third less height.
func drawMasthead(pdf *fpdf.Fpdf, m PrintMenu) {
	x, w := d(marginX-4.9), d(contentW+12.3)
	if len(m.Hero) > 0 {
		pdf.ImageOptions("menu-print-hero", x, d(heroTop), w, d(heroH), false,
			fpdf.ImageOptions{ImageType: m.HeroKind}, 0, "")
		pdf.SetAlpha(0.66, "Normal")
	}
	r, g, b := hexRGB("#f58a22")
	pdf.SetFillColor(r, g, b)
	pdf.Rect(x, d(heroTop), w, d(heroH), "F")
	pdf.SetAlpha(1, "Normal")

	// The title clears the wordmark when there is one and moves over to the
	// margin when there is not -- an indent that exists to make room for a
	// logo reads as a mistake once the logo is gone.
	//
	// The logo is measured off the band rather than pinned to numbers of its
	// own, so shortening the band cannot leave it hanging over the edge.
	titleX := d(marginX + 16)
	if len(m.Logo) > 0 {
		logoH := heroH - 2*logoInset
		logoW := logoH * logoAspect
		pdf.ImageOptions("menu-print-logo", d(marginX+logoInset), d(heroTop+logoInset),
			d(logoW), d(logoH), false, fpdf.ImageOptions{ImageType: "PNG"}, 0, "")
		titleX = d(marginX + logoInset + logoW + 22)
	}

	pdf.SetTextColor(255, 255, 255)
	title := strings.ToUpper(m.Title)
	subRight := d(marginX + contentW - 16)

	// The two are set at opposite ends of one line, so a long title is the one
	// way they can collide. The title gives way rather than the subtitle: it
	// is the larger of the two and has the room to lose.
	pdf.SetFont(printBody, "", heroSubPt*designScale)
	subW := pdf.GetStringWidth(m.Subtitle)
	titlePt := fitHeroTitle(pdf, title, subRight-titleX-subW-d(24))

	pdf.SetFont(printBold, "", titlePt)
	baseline := d(capCentredBaseline(heroTop, heroH, heroTitlePt))
	pdf.Text(titleX, baseline, title)

	pdf.SetFont(printBody, "", heroSubPt*designScale)
	rightText(pdf, subRight, alignedBaseline(baseline, titlePt, heroSubPt*designScale), m.Subtitle)
}

// Logo geometry, as fractions of the band. The aspect is the original art's;
// the inset is the breathing room above and below it.
const (
	logoInset  = 12.0
	logoAspect = 197.06 / 165.53
)

// fitHeroTitle returns the largest size at or below the design size that fits
// the width, stopping well short of illegible -- a masthead that has shrunk to
// body copy is worse than one that is merely tight.
func fitHeroTitle(pdf *fpdf.Fpdf, title string, width float64) float64 {
	full := heroTitlePt * designScale
	if width <= 0 {
		return full
	}
	for pt := full; pt >= full*0.6; pt -= 1 {
		pdf.SetFont(printBold, "", pt)
		if pdf.GetStringWidth(title) <= width {
			return pt
		}
	}
	return full * 0.6
}

// drawContinuedHead is the plain wordmark on pages two and up. The photograph
// is a first-page event; repeating it would cost a third of every sheet for a
// reader who already knows what they are holding.
func drawContinuedHead(pdf *fpdf.Fpdf, m PrintMenu) {
	pdf.SetTextColor(printInk[0], printInk[1], printInk[2])
	pdf.SetFont(printBold, "", contTitlePt*designScale)
	title := "MORE " + strings.ToUpper(m.Title)
	baseline := d(contHeadY)
	pdf.Text(d(marginX), baseline, title)
	w := pdf.GetStringWidth(title)
	// The subtitle is a third smaller, so sharing the baseline would hang it
	// low. Centre its capitals against the title's instead.
	pdf.SetFont(printBody, "", contSubPt*designScale)
	pdf.Text(d(marginX)+w+d(10),
		alignedBaseline(baseline, d(contTitlePt), d(contSubPt)), m.Subtitle)
}

// drawPrintFooter is the strap and the house lines, ruled off from the menu.
func drawPrintFooter(pdf *fpdf.Fpdf, m PrintMenu) {
	pdf.SetDrawColor(printInk[0], printInk[1], printInk[2])
	pdf.SetLineWidth(d(1.88))
	pdf.Line(d(marginX-5), d(footRuleTop), d(marginX+contentW+7), d(footRuleTop))
	pdf.Line(d(marginX-5), d(footRule2Y), d(marginX+contentW+7), d(footRule2Y))

	pdf.SetTextColor(printInk[0], printInk[1], printInk[2])
	if strap := strings.TrimSpace(m.Flight); strap != "" {
		pdf.SetFont(printBold, "", footPt*designScale)
		drawSpacedCentre(pdf, d(pageW/2), d(footStrapY), strings.ToUpper(strap),
			footStrapEm, footPt*designScale)
	}
	// The pour sizes live here rather than in a column of their own. Every
	// beer has a 9oz and a 4oz price, and printing all three made a wall of
	// numbers where the useful fact -- one price, and the sizes exist -- fits
	// on a line. The exceptions still carry their size beside the price.
	if sizes := strings.TrimSpace(m.Sizes); sizes != "" {
		pdf.SetFont(printBody, "", footSizesPt*designScale)
		drawSpacedCentre(pdf, d(pageW/2), d(footSizesY), sizes,
			footSizesEm, footSizesPt*designScale)
	}
	pdf.SetFont(printBody, "", footPt*designScale)
	if m.FootLeft != "" {
		drawSpaced(pdf, d(marginX-5), d(footLineY+12), m.FootLeft,
			footLineEm, footPt*designScale)
	}
	if m.FootRight != "" {
		drawSpacedRight(pdf, d(marginX+contentW+7), d(footLineY+12), m.FootRight,
			footLineEm, footPt*designScale)
	}
}

// drawLargePour writes the headline price, right-aligned, carrying its size
// when that size is not the house pour.
//
// The size rides along in smaller type rather than at full weight: it is a
// caveat on the number, not part of it, and setting it the same size as the
// price made a 12oz beer shout louder than the pints beside it.
func drawLargePour(pdf *fpdf.Fpdf, baseline float64, r Beer) {
	pour, _ := r.Headline()
	price, size := pour.Price, pour.Size
	if price == "" {
		pdf.SetFont(printBody, "", rowMetaPt*designScale)
		rightText(pdf, d(colLargeRight), baseline, "-")
		return
	}
	price = money(price)
	if size == "" || size == DefaultPour {
		pdf.SetFont(printBody, "", rowMetaPt*designScale)
		rightText(pdf, d(colLargeRight), baseline, price)
		return
	}
	note := " (" + size + ")"
	pdf.SetFont(printBody, "", rowSizePt*designScale)
	noteW := pdf.GetStringWidth(note)
	pdf.SetFont(printBody, "", rowMetaPt*designScale)
	priceW := pdf.GetStringWidth(price)
	x := d(colLargeRight) - priceW - noteW
	pdf.Text(x, baseline, price)
	pdf.SetFont(printBody, "", rowSizePt*designScale)
	pdf.Text(x+priceW, baseline, note)
}

// fitStyleSize returns the largest size at or below the body size that fits,
// stopping at rowMetaMin so a style never shrinks to unreadable.
func fitStyleSize(pdf *fpdf.Fpdf, s string, width float64) float64 {
	for pt := rowMetaPt; pt >= rowMetaMin; pt -= 0.5 {
		pdf.SetFont(printBody, "", pt*designScale)
		if pdf.GetStringWidth(s) <= width {
			return pt * designScale
		}
	}
	return rowMetaMin * designScale
}
