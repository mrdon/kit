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

// Columns are filled before the flow moves on: a column may only end where
// the next thing genuinely would not have fitted.
func TestColumnsFillBeforeMovingOn(t *testing.T) {
	pdf := newMeasuringPDF(t)
	plan, _ := planLayout(pdf, layoutSections(7, 5))
	for i := 1; i < len(plan); i++ {
		prev, cur := plan[i-1], plan[i]
		if prev.page == cur.page && prev.col == cur.col {
			continue
		}
		// cur opened a new column. What it brought (with a heading, the unit
		// after it too) must not have fitted where prev ended.
		need := headingH
		if !cur.heading {
			need = unitHeight(pdf, cur.unit)
		} else if i+1 < len(plan) {
			need += unitHeight(pdf, plan[i+1].unit)
		}
		end := prev.y + headingH
		if !prev.heading {
			end = prev.y + unitHeight(pdf, prev.unit)
		}
		if !cur.cont {
			end += sectionGap
		}
		if end+need <= bodyBottom {
			t.Errorf("moved to page %d col %d with %.0f free and %.0f needed",
				cur.page, cur.col, bodyBottom-end, need)
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
