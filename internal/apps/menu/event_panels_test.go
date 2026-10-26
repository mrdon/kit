package menu

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/events"
)

func TestExpandEventPanels(t *testing.T) {
	poster, logo := uuid.New(), uuid.New()
	over, live := uuid.New(), uuid.New()
	f := &events.BoardFeed{
		Today:    "2026-10-26",
		Upcoming: []events.BoardEvent{{Title: "Halloween Party", When: "Saturday", PosterID: &poster}},
		Weekly:   []events.BoardWeekly{{When: "Every Thu", Time: "6:30 to 8pm", Title: "Guesstronauts Trivia"}},
		ByLabel:  map[string]events.BoardEvent{"giveback": {Title: "SPAN", PosterID: &logo}},
		Live:     map[uuid.UUID]bool{live: true},
	}
	panels := []Panel{
		{Kind: PanelPoster, Source: SourceUpcoming},
		{Kind: PanelCTA, Label: "Cribbage", Headline: "Cribbage night.", Event: live.String()},
		{Kind: PanelCTA, Label: "Tsar Bomba Day", Headline: "Tsar Bomba.", Event: over.String()},
		{Kind: PanelAgenda, Label: "Every week", Source: SourceWeekly, Events: []Event{{Title: "Guesstronaut Trivia"}}},
		{Kind: PanelCTA, Label: "Give Back", Headline: "$2 a pint.", Image: "asset:give-back", Source: SourceLabel + "giveback"},
		{Kind: PanelCTA, Label: "Holiday parties", Headline: "Book here."},
	}

	got := expandEventPanels(panels, f)

	var labels []string
	for _, p := range got {
		labels = append(labels, p.Label)
	}
	// Tsar Bomba Day is pinned to an event that is over, so it is gone.
	want := "Saturday · Halloween Party|Cribbage|Every week|Give Back|Holiday parties"
	if strings.Join(labels, "|") != want {
		t.Fatalf("panels = %s, want %s", strings.Join(labels, "|"), want)
	}
	if got[0].Image != AssetRef+posterAsset+poster.String() {
		t.Errorf("poster image = %q", got[0].Image)
	}
	if got[2].Events[0].Title != "Guesstronauts Trivia" {
		t.Errorf("weekly kept the stored name: %q", got[2].Events[0].Title)
	}
	if got[3].Image != AssetRef+posterAsset+logo.String() {
		t.Errorf("give back image = %q", got[3].Image)
	}
	if got[3].Headline != "$2 a pint." {
		t.Error("give back copy was touched")
	}

	// Nothing upcoming: no poster panel at all, and the Give Back panel keeps
	// its stored image.
	empty := expandEventPanels(panels, &events.BoardFeed{Live: map[uuid.UUID]bool{}})
	if empty[0].Label != "Every week" || empty[1].Image != "asset:give-back" {
		t.Errorf("empty feed expanded to %+v", empty)
	}
}

func TestEventPanelValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    Panel
		ok   bool
	}{
		{"upcoming poster needs no image", Panel{Kind: PanelPoster, Source: SourceUpcoming}, true},
		{"weekly agenda needs no events", Panel{Kind: PanelAgenda, Source: SourceWeekly}, true},
		{"label cta", Panel{Kind: PanelCTA, Headline: "x", Source: SourceLabel + "giveback"}, true},
		{"pinned cta", Panel{Kind: PanelCTA, Headline: "x", Event: uuid.NewString()}, true},
		{"source on the wrong kind", Panel{Kind: PanelCTA, Headline: "x", Source: SourceUpcoming}, false},
		{"unknown source", Panel{Kind: PanelPoster, Image: "asset:x", Source: "events:soon"}, false},
		{"empty label", Panel{Kind: PanelCTA, Headline: "x", Source: SourceLabel}, false},
		{"bad event id", Panel{Kind: PanelCTA, Headline: "x", Event: "cribbage"}, false},
	} {
		err := tc.p.validate(0)
		if (err == nil) != tc.ok {
			t.Errorf("%s: err = %v", tc.name, err)
		}
		if err != nil && !errors.Is(err, ErrPayloadInvalid) {
			t.Errorf("%s: error is not ErrPayloadInvalid: %v", tc.name, err)
		}
	}
}
