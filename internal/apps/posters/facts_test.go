package posters

import (
	"strings"
	"testing"
	"time"

	"github.com/mrdon/kit/internal/apps/events"
)

func sampleEvent() *events.Event {
	start := time.Now().AddDate(0, 0, 7).Truncate(time.Hour)
	end := start.Add(3 * time.Hour)
	price := int64(500)
	return &events.Event{
		Title: "Vinyl night", Summary: "Bring a record.", Timezone: "America/Denver",
		StartsAt: start, EndsAt: &end, PriceCents: &price, Currency: "USD",
		PrepNotes: "the host is late, stall them with free pretzels", Labels: []string{"music"},
	}
}

func TestFacts_ExcludePrepNotesAndHashChanges(t *testing.T) {
	e := sampleEvent()
	f := factsFor(e)
	text := factsText(f)
	if strings.Contains(text, "pretzels") {
		t.Fatalf("prep notes leaked into facts: %s", text)
	}
	if !strings.Contains(text, "price: $5") {
		t.Errorf("price missing or in the wrong style: %s", text)
	}
	h1 := factsHash(f)
	e.Title = "Vinyl night, moved"
	if factsHash(factsFor(e)) == h1 {
		t.Error("hash did not change with the title")
	}
	e.Title = "Vinyl night"
	e.PrepNotes = "different notes"
	if factsHash(factsFor(e)) != h1 {
		t.Error("hash changed on a prep_notes edit; staff notes must not flag a poster")
	}
}

func TestFormatMoney(t *testing.T) {
	cases := map[int64]string{500: "$5", 550: "$5.50", 1200: "$12"}
	for cents, want := range cases {
		if got := formatMoney(cents, "USD"); got != want {
			t.Errorf("formatMoney(%d) = %q, want %q", cents, got, want)
		}
	}
}
