package menu

import (
	"strings"

	"github.com/go-pdf/fpdf"
)

// What goes in a column: headings, beers, families of beers, and sentences.
//
// Every unit is measured by the same arithmetic that draws it, so the plan
// and the page cannot disagree about where a column ends.

// Type sizes and the vertical rhythm, in points.
const (
	headPt     = 10.5 // section heading, bold tracked capitals
	headEm     = 0.12
	headRuleY  = 14.5 // heading top -> gold rule
	headRuleH  = 1.5
	headingH   = 22.0 // heading top -> first unit
	sectionGap = 6.0  // after a section's last unit, on top of its row gap

	namePt    = 12.5 // beer name, semibold capitals
	nameMinPt = 9.5  // a long name shrinks this far before it is clipped
	pricePt   = 12.0
	sizePt    = 7.5 // "9 oz" under a price
	metaPt    = 8.75
	descPt    = 9.25
	descLead  = 12.4
	variantPt = 9.75
	badgePt   = 6.5

	nameH    = 15.0 // name line
	metaH    = 11.5 // style and ABV line
	variantH = 14.0 // one row inside a family
	rowGap   = 8.0  // after every unit
	// A drink with nothing written about it -- a can, a soda -- is one line:
	// smaller name, style beside it, size beside the price.
	compactPt  = 10.5
	compactH   = 15.5
	compactGap = 3.0
)

// unitHeight is how far a unit advances the cursor.
func unitHeight(pdf *fpdf.Fpdf, u printUnit) float64 {
	switch {
	case u.Featured != nil:
		return featuredHeight(pdf, u.Featured)
	case u.Group != nil:
		g := u.Group
		h := float64(len(descLines(pdf, g.Notes)))*descLead + float64(len(g.Rows))*variantH
		if g.Name != "" {
			h += nameH + metaH
		}
		return h + rowGap
	case u.Beer != nil:
		return beerHeight(pdf, *u.Beer)
	default:
		return float64(len(descLines(pdf, u.Blurb)))*descLead + rowGap
	}
}

func beerHeight(pdf *fpdf.Fpdf, b Beer) float64 {
	if isCompact(b) {
		return compactH + compactGap
	}
	h := nameH
	if beerMeta(b) != "" || pourSize(b) != "" {
		h += metaH
	}
	return h + float64(len(descLines(pdf, b.Notes)))*descLead + rowGap
}

// descLines wraps prose at the column's width.
func descLines(pdf *fpdf.Fpdf, s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	pdf.SetFont(printBody, "", descPt)
	return wrapText(pdf, s, colW)
}

// drawHeading is the section's name in tracked capitals over a gold rule,
// with "cont." after it when the section carried over from another column.
func drawHeading(pdf *fpdf.Fpdf, x, y float64, name string, cont bool) {
	setText(pdf, printNavy)
	pdf.SetFont(printBold, "", headPt)
	base := y + headPt*0.82
	w := drawSpaced(pdf, x, base, strings.ToUpper(name), headEm, headPt)
	if cont {
		setText(pdf, printMuted)
		pdf.SetFont(printBold, "", headPt*0.75)
		drawSpaced(pdf, x+w+6, base, "CONT.", headEm, headPt*0.75)
	}
	setFill(pdf, printGold)
	pdf.Rect(x, y+headRuleY, colW, headRuleH, "F")
}

// drawBeer is one row: name and marks, price on the right with its size under
// it, the style and ABV, then the description.
func drawBeer(pdf *fpdf.Fpdf, x, y float64, b Beer) {
	if isCompact(b) {
		drawCompact(pdf, x, y, b)
		return
	}
	right := x + colW
	base := y + namePt*0.8
	// With no style or strength to print, the size moves into that line
	// rather than hanging under the price over an empty one.
	noMeta := beerMeta(b) == ""
	priceW := drawPrice(pdf, right, base, b, !noMeta)
	drawName(pdf, x, base, b.Name, right-priceW-10, beerBadges(b))

	cursor := y + nameH
	switch {
	case !noMeta:
		drawMeta(pdf, x, cursor+metaPt*0.8, tidyStyle(b.Style), tidyABV(b.ABV))
		cursor += metaH
	case pourSize(b) != "":
		drawMeta(pdf, x, cursor+metaPt*0.8, pourSize(b), "")
		cursor += metaH
	}
	drawDesc(pdf, x, cursor, b.Notes)
}

// isCompact is whether a row prints on one line. A row with a description is
// a beer someone will read about; one without is a list entry, and setting a
// can of soda at the size of a beer with a paragraph gave a page of cans the
// weight of a page of beers.
func isCompact(b Beer) bool { return strings.TrimSpace(b.Notes) == "" }

// drawCompact is a one-line row: name and marks, then the style and ABV in
// grey, and the size and price at the right.
func drawCompact(pdf *fpdf.Fpdf, x, y float64, b Beer) {
	right := x + colW
	base := y + compactPt*0.95
	pour, ok := b.Headline()
	price := "-"
	if ok && pour.Price != "" {
		price = shortPrice(pour.Price)
	}
	setText(pdf, printNavy)
	pdf.SetFont(printMedium, "", pricePt*0.9)
	priceW := pdf.GetStringWidth(price)
	rightText(pdf, right, base, price)
	edge := right - priceW - 8
	if size := pourSize(b); size != "" {
		setText(pdf, printMuted)
		pdf.SetFont(printBody, "", sizePt)
		edge -= pdf.GetStringWidth(size)
		pdf.Text(edge, base, size)
		edge -= 8
	}

	name := strings.ToUpper(b.Name)
	setText(pdf, printNavy)
	pdf.SetFont(printSemi, "", compactPt)
	name = clipTo(pdf, name, edge-x)
	pdf.Text(x, base, name)
	cur := x + pdf.GetStringWidth(name)
	for _, bd := range beerBadges(b) {
		cur += 4 + drawBadge(pdf, cur+4, base, bd, compactPt)
	}
	if meta := beerMeta(b); meta != "" && cur+8 < edge {
		setText(pdf, printMuted)
		pdf.SetFont(printBody, "", metaPt)
		pdf.Text(cur+7, base, clipTo(pdf, meta, edge-cur-7))
	}
}

// drawGroup is a family: its shared name and style, the description once, then
// a line per member with its own marks, strength and price.
func drawGroup(pdf *fpdf.Fpdf, x, y float64, g *beerGroup) {
	cursor := y
	if g.Name != "" {
		drawName(pdf, x, cursor+namePt*0.8, g.Name, x+colW, nil)
		cursor += nameH
		style := g.Style
		if g.Size != "" && g.Size != DefaultPour {
			style = joinDot(style, strings.Replace(g.Size, "oz", " oz", 1)+" pours")
		}
		drawMeta(pdf, x, cursor+metaPt*0.8, style, "")
		cursor += metaH
	}
	cursor = drawDesc(pdf, x, cursor, g.Notes)
	for _, v := range g.Rows {
		drawVariant(pdf, x, cursor, v, g.Size == "")
		cursor += variantH
	}
}

// drawVariant is one member of a family on a single line.
func drawVariant(pdf *fpdf.Fpdf, x, top float64, v groupVariant, ownSize bool) {
	right := x + colW
	base := top + variantPt*0.95
	pour, _ := v.Beer.Headline()

	setText(pdf, printNavy)
	pdf.SetFont(printMedium, "", pricePt*0.875)
	rightText(pdf, right, base, shortPrice(pour.Price))
	if abv := tidyABV(v.Beer.ABV); abv != "" {
		setText(pdf, printAmber)
		pdf.SetFont(printSemi, "", metaPt)
		rightText(pdf, right-30, base, abv)
	}

	label := v.Label
	if ownSize && pour.Size != "" && pour.Size != DefaultPour {
		label += " (" + strings.Replace(pour.Size, "oz", " oz", 1) + ")"
	}
	setText(pdf, printNavy)
	pdf.SetFont(printBody, "", variantPt)
	label = clipTo(pdf, label, colW-110)
	pdf.Text(x+8, base, label)
	cur := x + 8 + pdf.GetStringWidth(label)
	for _, bd := range beerBadges(v.Beer) {
		cur += 4 + drawBadge(pdf, cur+4, base, bd, variantPt)
	}
	if v.Style != "" {
		setText(pdf, printMuted)
		pdf.SetFont(printBody, "", metaPt*0.95)
		pdf.Text(cur+6, base, clipTo(pdf, v.Style, right-70-cur-6))
	}
}

// drawBlurb is a section's sentence, set as a description.
func drawBlurb(pdf *fpdf.Fpdf, x, y float64, text string) {
	drawDesc(pdf, x, y, text)
}

// drawDesc sets prose from top and returns where it ended.
func drawDesc(pdf *fpdf.Fpdf, x, top float64, text string) float64 {
	lines := descLines(pdf, text)
	setText(pdf, printNavy)
	pdf.SetFont(printBody, "", descPt)
	for i, line := range lines {
		pdf.Text(x, top+descPt*0.8+float64(i)*descLead, line)
	}
	return top + float64(len(lines))*descLead
}

// drawMeta is "Mexican Lager · 5.1%": the style in grey, the ABV in gold.
func drawMeta(pdf *fpdf.Fpdf, x, base float64, style, abv string) {
	cur := x
	if style != "" {
		setText(pdf, printMuted)
		pdf.SetFont(printBody, "", metaPt)
		pdf.Text(cur, base, clipTo(pdf, style, colW-40))
		cur += pdf.GetStringWidth(clipTo(pdf, style, colW-40))
		if abv != "" {
			pdf.Text(cur, base, " · ")
			cur += pdf.GetStringWidth(" · ")
		}
	}
	if abv != "" {
		setText(pdf, printAmber)
		pdf.SetFont(printSemi, "", metaPt)
		pdf.Text(cur, base, abv)
	}
}

// drawPrice sets the headline price right-aligned, with its size under it when
// that is not the house pour and withSize is set, and returns the width the
// price took.
func drawPrice(pdf *fpdf.Fpdf, right, base float64, b Beer, withSize bool) float64 {
	pour, ok := b.Headline()
	price := "-"
	if ok && pour.Price != "" {
		price = shortPrice(pour.Price)
	}
	setText(pdf, printNavy)
	pdf.SetFont(printMedium, "", pricePt)
	w := pdf.GetStringWidth(price)
	rightText(pdf, right, base, price)
	if size := pourSize(b); size != "" && withSize {
		setText(pdf, printMuted)
		pdf.SetFont(printBody, "", sizePt)
		rightText(pdf, right, base+sizePt+3, size)
	}
	return w
}

// drawName sets a name in capitals, shrinking it to fit beside its badges
// before clipping it, then draws the badges after it.
func drawName(pdf *fpdf.Fpdf, x, base float64, name string, limit float64, badges []string) {
	name = strings.ToUpper(name)
	room := limit - x
	for _, bd := range badges {
		room -= 4 + badgeWidth(pdf, bd)
	}
	size := namePt
	pdf.SetFont(printSemi, "", size)
	for size > nameMinPt && pdf.GetStringWidth(name) > room {
		size -= 0.5
		pdf.SetFont(printSemi, "", size)
	}
	name = clipTo(pdf, name, room)
	setText(pdf, printNavy)
	pdf.Text(x, base, name)
	cur := x + pdf.GetStringWidth(name)
	for _, bd := range badges {
		cur += 4 + drawBadge(pdf, cur+4, base, bd, size)
	}
}

// beerMeta is whether a row has a style or ABV line at all. Sodas often have
// neither, and an empty line under them would read as a gap.
func beerMeta(b Beer) string {
	return joinDot(tidyStyle(b.Style), tidyABV(b.ABV))
}

// pourSize is the headline pour's size as the board writes it ("9 oz"), or
// empty for the house pint.
func pourSize(b Beer) string {
	p, ok := b.Headline()
	if !ok || p.Size == "" || p.Size == DefaultPour {
		return ""
	}
	return strings.Replace(p.Size, "oz", " oz", 1)
}

// The two marks a row can wear, as the board draws them.
const (
	badgeGR   = "GR"
	badgeNew  = "NEW"
	badgeStar = "★"
)

func beerBadges(b Beer) []string {
	var out []string
	if b.GlutenReduced {
		out = append(out, badgeGR)
	}
	if isNewAt(b.AddedAt, timeNow()) {
		out = append(out, badgeNew)
	}
	if b.Featured {
		out = append(out, badgeStar)
	}
	return out
}

// drawBadge draws a mark beside text of the given size, centred on that text's
// capitals, and returns its width: GR outlined in navy, NEW filled gold, both
// in small tracked capitals.
func drawBadge(pdf *fpdf.Fpdf, x, base float64, text string, lineSize float64) float64 {
	if text == badgeStar {
		drawStar(pdf, x+5, base-lineSize*capHeight/2, 5)
		return 10
	}
	w := badgeWidth(pdf, text)
	h := badgePt * 1.55
	top := base - lineSize*capHeight/2 - h/2
	if text == badgeNew {
		setFill(pdf, printHot)
		pdf.RoundedRect(x, top, w, h, 1.5, "1234", "F")
	} else {
		setDraw(pdf, printNavy)
		pdf.SetLineWidth(0.8)
		pdf.RoundedRect(x, top, w, h, 1.5, "1234", "D")
	}
	setText(pdf, printNavy)
	pdf.SetFont(printBold, "", badgePt)
	drawSpaced(pdf, x+2.5, top+h/2+badgePt*capHeight/2, text, 0.1, badgePt)
	return w
}

func badgeWidth(pdf *fpdf.Fpdf, text string) float64 {
	if text == badgeStar {
		return 10
	}
	pdf.SetFont(printBold, "", badgePt)
	return spacedWidth(pdf, text, 0.1, badgePt) + 5
}

// joinDot joins the non-empty parts with a centred dot.
func joinDot(parts ...string) string {
	var keep []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			keep = append(keep, p)
		}
	}
	return strings.Join(keep, " · ")
}

// clipTo shortens a string to fit, with an ellipsis.
func clipTo(pdf *fpdf.Fpdf, s string, width float64) string {
	if width <= 0 || pdf.GetStringWidth(s) <= width {
		return s
	}
	runes := []rune(s)
	for len(runes) > 1 {
		runes = runes[:len(runes)-1]
		if pdf.GetStringWidth(string(runes)+"…") <= width {
			return string(runes) + "…"
		}
	}
	return ""
}
