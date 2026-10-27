package menu

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/events"
)

func TestExpandEventPanels(t *testing.T) {
	poster, logo, crib := uuid.New(), uuid.New(), uuid.New()
	f := &events.BoardFeed{
		Today:    "2026-10-26",
		Upcoming: []events.BoardEvent{{Title: "Halloween Party", When: "Saturday", PosterID: &poster}},
		Weekly:   []events.BoardWeekly{{When: "Every Thu", Time: "6:30 to 8pm", Title: "Guesstronauts Trivia"}},
		ByLabel:  map[string]events.BoardEvent{"giveback": {Title: "SPAN", PosterID: &logo}},
		Featured: &events.BoardEvent{
			Title: "Cribbage Night", When: "Tonight", Time: "6 to 8pm",
			Summary: "Beginners welcome.", PosterID: &crib,
		},
	}
	panels := []Panel{
		{Kind: PanelPoster, Source: SourceUpcoming},
		{Kind: PanelPoster, Label: "Don't miss", Source: SourceFeatured},
		{Kind: PanelAgenda, Label: "Every week", Source: SourceWeekly, Events: []Event{{Title: "Guesstronaut Trivia"}}},
		{Kind: PanelCTA, Label: "Give Back", Headline: "$2 a pint.", Image: "asset:give-back", Source: SourceLabel + "giveback"},
		{Kind: PanelCTA, Label: "Holiday parties", Headline: "Book here."},
	}

	got := expandEventPanels(panels, f)

	var labels []string
	for _, p := range got {
		labels = append(labels, p.Label)
	}
	want := "Saturday · Halloween Party|Don't miss|Every week|Give Back|Holiday parties"
	if strings.Join(labels, "|") != want {
		t.Fatalf("panels = %s, want %s", strings.Join(labels, "|"), want)
	}
	if got[0].Image != AssetRef+posterAsset+poster.String() {
		t.Errorf("poster image = %q", got[0].Image)
	}
	if !got[1].Feature || got[1].Image != AssetRef+posterAsset+crib.String() {
		t.Errorf("featured = %+v", got[1])
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

	// Nothing upcoming or featured: neither panel at all, and the Give Back
	// panel keeps its stored image.
	empty := expandEventPanels(panels, &events.BoardFeed{})
	if empty[0].Label != "Every week" || empty[1].Image != "asset:give-back" {
		t.Errorf("empty feed expanded to %+v", empty)
	}
}

func TestTonightPanelsShareASlot(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	f := &events.BoardFeed{Upcoming: []events.BoardEvent{
		{Title: "Cribbage Night", When: "Tonight", PosterID: &a},
		{Title: "Lunch Thing", When: "Today", PosterID: &b},
		{Title: "Broncos", When: "Sunday", PosterID: &c},
	}}
	got := expandEventPanels([]Panel{{Kind: PanelPoster, Source: SourceTonight}}, f)
	if len(got) != 2 || got[0].Label != "Tonight · Cribbage Night" || got[1].Label != "Today · Lunch Thing" {
		t.Fatalf("tonight = %+v", got)
	}
	if got[0].Group == "" || got[0].Group != got[1].Group {
		t.Errorf("tonight posters do not share a slot: %q, %q", got[0].Group, got[1].Group)
	}
	if none := expandEventPanels([]Panel{{Kind: PanelPoster, Source: SourceTonight}}, &events.BoardFeed{}); len(none) != 0 {
		t.Errorf("a night with nothing on left %+v", none)
	}

	// No one-off left today: the BOGO deal gets the slot instead.
	f.Upcoming = f.Upcoming[2:]
	f.Background = []events.BoardEvent{{Title: "BOGO Pizza", When: "Tonight", PosterID: &a}}
	got = expandEventPanels([]Panel{{Kind: PanelPoster, Source: SourceTonight}}, f)
	if len(got) != 1 || got[0].Label != "Tonight · BOGO Pizza" {
		t.Errorf("fallback = %+v", got)
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
		{"featured poster needs no image", Panel{Kind: PanelPoster, Source: SourceFeatured}, true},
		{"featured on a cta", Panel{Kind: PanelCTA, Headline: "x", Source: SourceFeatured}, false},
		{"source on the wrong kind", Panel{Kind: PanelCTA, Headline: "x", Source: SourceUpcoming}, false},
		{"unknown source", Panel{Kind: PanelPoster, Image: "asset:x", Source: "events:soon"}, false},
		{"empty label", Panel{Kind: PanelCTA, Headline: "x", Source: SourceLabel}, false},
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
