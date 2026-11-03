package menu

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

const wrapNote = "A beer with a description long enough to wrap onto a second line " +
	"of the column, which is what makes a row tall enough to force the issue."

func layoutBeer(name string) Beer {
	return Beer{Name: name, Style: "IPA - West Coast", ABV: "6.5%", Notes: wrapNote,
		Pours: []Pour{{Size: "16oz", Label: "16oz Draft", Price: "7"}}}
}

func layoutSections(n, per int) []PrintSection {
	var out []PrintSection
	for i := range n {
		sec := PrintSection{Name: fmt.Sprintf("Section %d", i)}
		for j := range per {
			// Distinct descriptions, or the grouping rule folds them.
			b := layoutBeer(fmt.Sprintf("Beer %d-%d", i, j))
			b.Notes += fmt.Sprintf(" (%d-%d)", i, j)
			sec.Rows = append(sec.Rows, b)
		}
		out = append(out, sec)
	}
	return out
}

// Nothing may be drawn below the column's end: a description running under
// the footer reads as a printer fault. Every placement's bottom is checked
// against the limit the plan was given.
func TestPlanNeverRunsIntoTheFooter(t *testing.T) {
	pdf := newMeasuringPDF(t)
	sections := layoutSections(7, 5)
	plan, pages := planLayout(pdf, sections)
	if pages < 2 {
		t.Fatalf("got %d pages; the fixture is meant to overflow", pages)
	}
	for _, pl := range plan {
		h := headingH
		if !pl.heading {
			h = unitHeight(pdf, pl.unit) - rowGap
		}
		if pl.y+h > bodyBottom {
			t.Errorf("page %d col %d: placement at %.0f runs to %.0f, past %.0f",
				pl.page, pl.col, pl.y, pl.y+h, bodyBottom)
		}
		top := bodyTopRest
		if pl.page == 0 {
			top = bodyTopFirst
		}
		if pl.y < top {
			t.Errorf("placement at %.0f starts above the body top %.0f", pl.y, top)
		}
	}
}

// Columns are filled up to the balanced line before the flow moves on: a
// column may only end where the next thing would not have fitted above it.
func TestColumnsFillToTheBalancedLine(t *testing.T) {
	pdf := newMeasuringPDF(t)
	sections := layoutSections(7, 5)
	plan, pages := planLayout(pdf, sections)
	bottom := balancedBottom(pdf, sections, 2*pages)
	for i := 1; i < len(plan); i++ {
		prev, cur := plan[i-1], plan[i]
		if prev.page == cur.page && prev.col == cur.col {
			continue
		}
		need := headingH
		if !cur.heading {
			need = unitHeight(pdf, cur.unit)
		} else {
			// A section opens only where its first two units fit.
			for j := i + 1; j < len(plan) && j <= i+2 && plan[j].sec == cur.sec && !plan[j].heading; j++ {
				need += unitHeight(pdf, plan[j].unit)
			}
		}
		end := prev.y + headingH
		if !prev.heading {
			end = prev.y + unitHeight(pdf, prev.unit)
		}
		if !cur.cont {
			end += sectionGap
		}
		limit := bottom
		if isLastOfSection(plan, i) || (cur.heading && !cur.cont && sectionLen(plan, i) <= 2) {
			limit = bodyBottom
		}
		if end+need <= limit {
			t.Errorf("moved to page %d col %d with %.0f free above the line and %.0f needed",
				cur.page, cur.col, bottom-end, need)
		}
	}
}

// isLastOfSection reports whether the unit opening a column at plan[i] (or the
// one after its heading) is its section's last.
func isLastOfSection(plan []placement, i int) bool {
	j := i
	if plan[j].heading && j+1 < len(plan) {
		j++
	}
	return j+1 >= len(plan) || plan[j+1].sec != plan[j].sec
}

// sectionLen counts the units of the section whose heading is plan[i].
func sectionLen(plan []placement, i int) int {
	n := 0
	for j := i + 1; j < len(plan) && plan[j].sec == plan[i].sec; j++ {
		if !plan[j].heading {
			n++
		}
	}
	return n
}

// A section's last unit never sits alone at the top of a column when it would
// have fitted above the footer where its section was.
func TestLastUnitIsNotStranded(t *testing.T) {
	pdf := newMeasuringPDF(t)
	for _, n := range []int{3, 5, 7} {
		plan, _ := planLayout(pdf, layoutSections(n, 3))
		for i := 2; i < len(plan); i++ {
			cur, head, prev := plan[i], plan[i-1], plan[i-2]
			if !head.cont || cur.heading || !isLastOfSection(plan, i) {
				continue
			}
			end := prev.y + unitHeight(pdf, prev.unit)
			if end+unitHeight(pdf, cur.unit) <= bodyBottom {
				t.Errorf("%s: last row moved to a new column with room for it above the footer",
					cur.sec.Name)
			}
		}
	}
}

// A section never opens with its heading and one unit at a column's foot and
// carries on in the next column.
func TestFirstUnitIsNotStranded(t *testing.T) {
	pdf := newMeasuringPDF(t)
	for _, n := range []int{3, 5, 7} {
		plan, _ := planLayout(pdf, layoutSections(n, 3))
		for i := 0; i+3 < len(plan); i++ {
			head, first, next := plan[i], plan[i+1], plan[i+2]
			if !head.heading || head.cont || next.sec != head.sec {
				continue
			}
			if next.heading && (next.page != first.page || next.col != first.col) {
				t.Errorf("%s: one row at a column's foot, the rest in the next column", head.sec.Name)
			}
		}
	}
}

// Balancing evens the columns out; it never costs a page.
func TestBalancingNeverAddsAPage(t *testing.T) {
	pdf := newMeasuringPDF(t)
	for _, n := range []int{1, 3, 5, 7, 9} {
		sections := layoutSections(n, 4)
		_, packed := flowAt(pdf, sections, bodyBottom)
		_, pages := planLayout(pdf, sections)
		if pages != (packed+1)/2 {
			t.Errorf("%d sections: %d pages balanced, %d packed", n, pages, (packed+1)/2)
		}
	}
}

// Balanced columns end far closer together than packed ones. Not exactly
// level: breaks fall between rows, and a section's last row is never sent
// alone to the next column to even things out.
func TestColumnsComeOutLevel(t *testing.T) {
	pdf := newMeasuringPDF(t)
	for _, n := range []int{2, 5, 7} {
		sections := layoutSections(n, 4)
		packed, cols := flowAt(pdf, sections, bodyBottom)
		cols = 2 * ((cols + 1) / 2)
		plan, _ := planLayout(pdf, sections)
		plo, phi := columnEnds(pdf, packed, cols)
		lo, hi := columnEnds(pdf, plan, cols)
		if hi-lo > phi-plo {
			t.Errorf("%d sections: balanced spread %.0f is worse than packed %.0f", n, hi-lo, phi-plo)
		}
		if hi-lo > (bodyBottom-bodyTopRest)/2 {
			t.Errorf("%d sections: columns end %.0f apart, over half a column", n, hi-lo)
		}
	}
}

// A heading never sits alone at a column's foot, and a section carried into a
// new column repeats its heading marked as continued.
func TestHeadingsTravelWithTheirRows(t *testing.T) {
	pdf := newMeasuringPDF(t)
	plan, _ := planLayout(pdf, layoutSections(7, 5))
	for i, pl := range plan {
		if !pl.heading {
			continue
		}
		if i+1 >= len(plan) {
			t.Fatal("the plan ends on a heading")
		}
		next := plan[i+1]
		if next.heading || next.page != pl.page || next.col != pl.col {
			t.Errorf("%s heading was separated from its first row", pl.sec.Name)
		}
	}
	sawCont := false
	for i := 1; i < len(plan); i++ {
		cur, prev := plan[i], plan[i-1]
		if cur.heading || prev.heading || cur.sec != prev.sec {
			continue
		}
		if cur.page != prev.page || cur.col != prev.col {
			t.Errorf("%s continued into a new column with no heading", cur.sec.Name)
		}
	}
	for _, pl := range plan {
		sawCont = sawCont || pl.cont
	}
	if !sawCont {
		t.Error("the fixture never carried a section over; it should")
	}
}

// The renderer and the plan agree: the PDF has as many pages as the plan, and
// the footer numbers them.
func TestRenderMatchesThePlan(t *testing.T) {
	m := PrintMenu{Brand: "Gravity Brewing", Title: "Beers", Subtitle: "& More",
		Flight: "Any four 4oz pours", Sizes: defaultSizes, FootLeft: "WIFI: Gravity",
		FootRight: "@thegravitybrewing", Sections: layoutSections(7, 5)}
	m.Sections[0].Rows[0].GlutenReduced = true
	var buf bytes.Buffer
	if err := RenderPrintPDF(m, &buf); err != nil {
		t.Fatalf("rendering: %v", err)
	}
	_, pages := planLayout(newMeasuringPDF(t), m.Sections)
	if n := strings.Count(buf.String(), "/Type /Page\n"); n != pages {
		t.Errorf("PDF has %d pages, plan has %d", n, pages)
	}
}

// A section that is only a sentence takes its heading and the sentence, and
// nothing more.
func TestBlurbOnlySectionRenders(t *testing.T) {
	snacks := PrintSection{Name: "Snacks", Blurb: "Pretzels, chips and popcorn — ask at the bar."}
	pdf := newMeasuringPDF(t)
	units := sectionUnits(snacks)
	if len(units) != 1 || units[0].Blurb == "" {
		t.Fatalf("got %+v, want one blurb unit", units)
	}
	if h := unitHeight(pdf, units[0]); h < descLead || h > 3*descLead+rowGap {
		t.Errorf("blurb height %.1f, want a line or two", h)
	}
	var buf bytes.Buffer
	if err := RenderPrintPDF(PrintMenu{Title: "Beers", Sections: []PrintSection{snacks}}, &buf); err != nil {
		t.Fatalf("rendering: %v", err)
	}
}

// The masthead's name shrinks before it reaches the flight badge, and drops
// the title line rather than overlapping it.
func TestMastheadFitsBesideTheBadge(t *testing.T) {
	for _, brand := range []string{"Gravity Brewing", "The Exceedingly Long Taproom And Brewing Company"} {
		m := PrintMenu{Brand: brand, Title: "Beers", Subtitle: "& More", Flight: defaultFlight}
		var buf bytes.Buffer
		if err := RenderPrintPDF(m, &buf); err != nil {
			t.Fatalf("rendering %q: %v", brand, err)
		}
	}
	// The band's capitals sit inside the band.
	base := capCentredBaseline(mastTop, mastH, brandPt)
	if top := base - brandPt*capHeight; top < mastTop || base > mastTop+mastH {
		t.Errorf("name spans %.1f-%.1f, outside the band %.1f-%.1f", top, base, mastTop, mastTop+mastH)
	}
	if bodyTopFirst < mastTop+mastH+mastRuleH {
		t.Error("the body starts inside the masthead")
	}
}

func TestMastWords(t *testing.T) {
	name, sub := mastWords(PrintMenu{Brand: "Gravity Brewing", Title: "Beers", Subtitle: "& More"})
	if name != "GRAVITY BREWING" || sub != "BEERS & MORE" {
		t.Errorf("with a brand: %q / %q", name, sub)
	}
	name, sub = mastWords(PrintMenu{Title: "Beers", Subtitle: "& More"})
	if name != "BEERS" || sub != "& MORE" {
		t.Errorf("without a brand: %q / %q", name, sub)
	}
}

// The masthead logo is found by convention, so its key has to be one an admin
// can actually store with set_menu_asset. The first one did not.
func TestPrintLogoKeyCanBeSet(t *testing.T) {
	if !keyPattern.MatchString(printLogoKeys[0]) {
		t.Errorf("logo key %q is refused by set_menu_asset", printLogoKeys[0])
	}
}
