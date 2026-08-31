package events

import (
	"strings"
	"testing"

	"github.com/go-pdf/fpdf"
)

// bulletFixture is a document with the topper fonts loaded, plus the text
// width one band actually has. Measuring against anything narrower would let a
// test pass on type that overflows in print.
func bulletFixture(t *testing.T) (*fpdf.Fpdf, float64) {
	t.Helper()
	pdf := fpdf.NewCustom(&fpdf.InitType{
		UnitStr: "mm",
		Size:    fpdf.SizeType{Wd: sheetW, Ht: sheetH},
	})
	if err := loadTopperFonts(pdf); err != nil {
		t.Fatalf("loading fonts: %v", err)
	}
	x, right := bandTextSpan(margin, panelW-2*margin, bandMaxH, false)
	return pdf, right - x
}

// Detail is set to be read from a chair, not merely to fit. A band with more
// copy than room loses lines; it does not shrink into type nobody can read.
func TestBulletsKeepTheirSizeAndLoseLines(t *testing.T) {
	pdf, w := bulletFixture(t)
	long := []string{
		"Join the crew from Pints in the Park at Gravity Brewing for special games, giveaways, and offers",
		"Members only, and you must register in advance to take part",
		"Dine in only, while supplies last",
	}
	size, lines := fitBullets(pdf, long, w, 8, bandMaxH)
	if size < minBulletPt {
		t.Fatalf("size = %v, want no smaller than the %v floor", size, minBulletPt)
	}
	if len(lines) > maxBulletLines {
		t.Fatalf("lines = %d, want at most %d", len(lines), maxBulletLines)
	}
	if len(lines) == 0 {
		t.Fatal("no lines drawn")
	}
	if last := lines[len(lines)-1].text; !strings.HasSuffix(last, "…") {
		t.Fatalf("last line = %q, want an ellipsis marking the cut", last)
	}
}

// A cut lands between words. Half a word on a card handed to a customer reads
// as a printer that gave up rather than as a line that was edited.
func TestBulletsCutBetweenWords(t *testing.T) {
	pdf, w := bulletFixture(t)
	source := "Buy one get one on any sourdough pizza from Double D's every Monday"
	whole := map[string]bool{bulletDot: true}
	for word := range strings.FieldsSeq(strings.ToUpper(source)) {
		whole[word] = true
	}
	// Sweep the widths a real band can hand the clamp, so the assertion does
	// not depend on the cut happening to land in a friendly place.
	pdf.SetFont(fontText, "", minBulletPt)
	for avail := 3.0; avail <= w; avail += 1.5 {
		line := clipWordsToWidth(pdf, bulletLines(pdf, []string{source}, w)[0].text, avail)
		for word := range strings.FieldsSeq(strings.TrimSuffix(line, "…")) {
			if !whole[strings.TrimRight(word, " ,;:-")] {
				t.Fatalf("width %.1f: line %q cuts %q mid-word", avail, line, word)
			}
		}
	}
}

// Copy that fits is left alone -- no phantom ellipsis on a band that said
// everything it had to say.
func TestBulletsThatFitAreNotMarkedTruncated(t *testing.T) {
	pdf, w := bulletFixture(t)
	size, lines := fitBullets(pdf, []string{"Free to play"}, w, bandMaxH, bandMaxH)
	if len(lines) != 1 {
		t.Fatalf("lines = %d, want 1", len(lines))
	}
	if strings.HasSuffix(lines[0].text, "…") {
		t.Fatalf("line = %q, want no ellipsis", lines[0].text)
	}
	if size < minBulletPt {
		t.Fatalf("size = %v, want no smaller than the %v floor", size, minBulletPt)
	}
}

// The clamp is the last thing between a busy band and copy spilling over the
// footer, so it has to hold when there is no room at all.
func TestClampBulletsWithNoRoom(t *testing.T) {
	pdf, w := bulletFixture(t)
	pdf.SetFont(fontText, "", minBulletPt)
	lines := bulletLines(pdf, []string{"Trivia at seven"}, w)
	if got := clampBullets(pdf, lines, w, 0); got != nil {
		t.Fatalf("clamped = %+v, want nothing drawn", got)
	}
	if got := clampBullets(pdf, lines, w, -1); got != nil {
		t.Fatalf("negative room = %+v, want nothing drawn", got)
	}
}
