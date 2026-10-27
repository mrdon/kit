package menu

import (
	"fmt"
	"strings"
)

// The beer spotlight: a stored rotation of house beers, each with a photo and
// a blurb, of which the wall shows only what is pouring.
//
// Written once rather than generated: Untappd blocks us, so there is no feed
// of descriptions to keep it current, and a beer worth a spotlight comes back
// often enough that its card is worth keeping. Filtering at render against
// the live tap list is what lets a stored card be safe: a beer that kicks
// drops out by itself and returns with the keg. Style and ABV come from the
// tap row for the same reason.
//
// Every beer on the panel shares its one slot on the rail, a different beer
// each pass, so a long rotation never costs the events a turn.

// SpotlightBeer is one card in a spotlight panel.
type SpotlightBeer struct {
	// Name matches the tap list's name, ignoring case.
	Name  string `json:"name"`
	Image string `json:"image"`
	Alt   string `json:"alt,omitempty"`
	Blurb string `json:"blurb"`
}

// spotlightBlurbMax keeps a blurb to the three or four lines that fit under
// the photo; anything longer runs off the bottom of the panel.
const spotlightBlurbMax = 160

func (p *Panel) validateSpotlight(i int) error {
	if len(p.Beers) == 0 {
		return fmt.Errorf("%w: spotlight panel %d has no beers", ErrPayloadInvalid, i+1)
	}
	for j, b := range p.Beers {
		switch {
		case strings.TrimSpace(b.Name) == "":
			return fmt.Errorf("%w: spotlight panel %d beer %d has no name", ErrPayloadInvalid, i+1, j+1)
		case !strings.HasPrefix(b.Image, AssetRef) && !strings.HasPrefix(b.Image, "data:image/"):
			return fmt.Errorf("%w: spotlight beer %q needs an image, %q plus an uploaded asset key",
				ErrPayloadInvalid, b.Name, AssetRef)
		case strings.TrimSpace(b.Blurb) == "":
			return fmt.Errorf("%w: spotlight beer %q has no blurb", ErrPayloadInvalid, b.Name)
		case len([]rune(b.Blurb)) > spotlightBlurbMax:
			return fmt.Errorf("%w: spotlight beer %q blurb is %d characters, over the %d that fit",
				ErrPayloadInvalid, b.Name, len([]rune(b.Blurb)), spotlightBlurbMax)
		}
	}
	return nil
}

// expandSpotlight turns each spotlight panel into one panel per beer on tap,
// all in one group so they share a slot. A spotlight with nothing pouring is
// gone for now.
func expandSpotlight(panels []Panel, taps []Tap) []Panel {
	out := make([]Panel, 0, len(panels))
	for i, p := range panels {
		if p.Kind != PanelSpotlight {
			out = append(out, p)
			continue
		}
		group := p.Group
		if group == "" {
			group = fmt.Sprintf("spotlight-%d", i)
		}
		for _, b := range p.Beers {
			tap, ok := findTap(taps, b.Name)
			if !ok {
				continue
			}
			out = append(out, Panel{
				Kind:     PanelSpotlight,
				Label:    p.Label,
				Group:    group,
				Image:    b.Image,
				Alt:      b.Alt,
				Headline: tap.Name,
				Body:     b.Blurb,
				Detail:   tapDetail(tap),
			})
		}
	}
	return out
}

func findTap(taps []Tap, name string) (Tap, bool) {
	name = strings.TrimSpace(name)
	for _, t := range taps {
		if strings.EqualFold(strings.TrimSpace(t.Name), name) {
			return t, true
		}
	}
	return Tap{}, false
}

// tapDetail is the line under the name: "IPA - Hazy New England · 6.5%".
func tapDetail(t Tap) string {
	parts := make([]string, 0, 2)
	for _, s := range []string{t.Style, t.ABV} {
		if s = strings.TrimSpace(s); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " · ")
}
