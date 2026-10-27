package menu

import (
	"context"
	"errors"
	"html/template"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/events"
)

func TestExpandSpotlightShowsOnlyWhatIsPouring(t *testing.T) {
	taps := []Tap{
		{Name: "Wicked Nebula", Style: "IPA - Hazy New England", ABV: "6.5%"},
		{Name: "Little Dipper", Style: "Mild - Dark", ABV: "4%"},
	}
	panels := []Panel{
		{Kind: PanelPoster, Image: "asset:x"},
		{Kind: PanelSpotlight, Label: "On tap", Beers: []SpotlightBeer{
			{Name: "little dipper", Image: "asset:dipper", Blurb: "Dark and easy."},
			{Name: "Light Lift Lager", Image: "asset:lift", Blurb: "Crisp."},
			{Name: "Wicked Nebula", Image: "asset:nebula", Blurb: "Hazy."},
		}},
	}

	got := expandSpotlight(panels, taps)

	if len(got) != 3 {
		t.Fatalf("got %d panels, want the poster and two beers: %+v", len(got), got)
	}
	dipper := got[1]
	// The tap list's spelling, not the stored one, and its style and ABV.
	if dipper.Headline != "Little Dipper" || dipper.Detail != "Mild - Dark · 4%" || dipper.Image != "asset:dipper" {
		t.Errorf("dipper = %+v", dipper)
	}
	if got[1].Group == "" || got[1].Group != got[2].Group {
		t.Errorf("beers do not share a slot: %q, %q", got[1].Group, got[2].Group)
	}

	// Nothing on the rotation is pouring: the spotlight is gone.
	if got := expandSpotlight(panels, []Tap{{Name: "Regular"}}); len(got) != 1 {
		t.Errorf("empty spotlight left %d panels", len(got))
	}
}

func TestPanelSlotsCountAGroupOnce(t *testing.T) {
	panels := []Panel{
		{Label: "poster"},
		{Label: "give back", Group: "house"},
		{Label: "weekly"},
		{Label: "parties", Group: "house"},
		{Label: "dipper", Group: "spot"},
		{Label: "nebula", Group: "spot"},
	}
	got := panelSlots(panels)
	want := []int{0, 1, 2, 1, 3, 3}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("slots = %v, want %v", got, want)
		}
	}
}

func TestSpotlightValidation(t *testing.T) {
	beer := func(b SpotlightBeer) Panel { return Panel{Kind: PanelSpotlight, Beers: []SpotlightBeer{b}} }
	for _, tc := range []struct {
		name string
		p    Panel
		ok   bool
	}{
		{"ok", beer(SpotlightBeer{Name: "Wicked Nebula", Image: "asset:n", Blurb: "Hazy."}), true},
		{"no beers", Panel{Kind: PanelSpotlight}, false},
		{"no image", beer(SpotlightBeer{Name: "Wicked Nebula", Blurb: "Hazy."}), false},
		{"no blurb", beer(SpotlightBeer{Name: "Wicked Nebula", Image: "asset:n"}), false},
		{"long blurb", beer(SpotlightBeer{Name: "Wicked Nebula", Image: "asset:n", Blurb: strings.Repeat("x", spotlightBlurbMax+1)}), false},
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

func TestRenderSharesASlot(t *testing.T) {
	b := &Board{
		Taps: []Tap{{Name: "Wicked Nebula", Section: "IPA"}},
		Panels: []Panel{
			{Kind: PanelCTA, Label: "Give Back", Headline: "x", Group: "house"},
			{Kind: PanelCTA, Label: "Parties", Headline: "y", Group: "house"},
			{Kind: PanelSpotlight, Label: "On tap", Group: "spot", Image: "data:image/png;base64,AA==",
				Headline: "Wicked Nebula", Detail: "IPA · 6.5%", Body: "Hazy."},
		},
	}
	html, err := Render(b, nil, "v")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(html, `data-slot="0"`) != 2 || strings.Count(html, `data-slot="1"`) != 1 {
		t.Error("grouped panels did not share slot 0")
	}
	if !strings.Contains(html, `class="spot-name">Wicked Nebula`) || !strings.Contains(html, "IPA · 6.5%") {
		t.Error("spotlight did not render its beer")
	}
}

type stubFeed struct{ f *events.BoardFeed }

func (s stubFeed) BoardFeed(context.Context, uuid.UUID, events.BoardRequest) (*events.BoardFeed, error) {
	return s.f, nil
}

func (s stubFeed) BoardPoster(context.Context, uuid.UUID, uuid.UUID) template.URL { return "" }

// The page and the version it polls must stamp the same panels, or the wall
// reloads every poll: the spotlight once expanded on one path only.
func TestPageAndVersionStampAgree(t *testing.T) {
	orig := boardEvents
	t.Cleanup(func() { boardEvents = orig })
	poster := uuid.New()
	boardEvents = func() eventFeed {
		return stubFeed{&events.BoardFeed{Today: "2026-10-27", Upcoming: []events.BoardEvent{{Title: "Halloween", PosterID: &poster}}}}
	}
	board := func() *Board {
		return &Board{
			Taps: []Tap{{Name: "Wicked Nebula", Section: "IPA"}},
			Panels: []Panel{
				{Kind: PanelPoster, Source: SourceUpcoming},
				{Kind: PanelSpotlight, Beers: []SpotlightBeer{{Name: "Wicked Nebula", Image: "asset:n", Blurb: "Hazy."}}},
			},
		}
	}
	a := &App{}
	page := a.expandPanels(context.Background(), uuid.New(), board(), map[string]string{})
	version := a.expandPanels(context.Background(), uuid.New(), board(), nil)
	if page == "" || page != version {
		t.Errorf("page stamp %q, version stamp %q", page, version)
	}
}
