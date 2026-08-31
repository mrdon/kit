// Package menu renders a taproom tap list as a full-screen page for a wall
// display, and serves it at a stable public URL a kiosk can be pointed at.
//
// It is the content half of a pair: this app answers "what should the screen
// show", and the kiosk app answers "which screen shows it". They are kept
// apart on purpose. A menu board is authored and re-rendered many times a
// week; a kiosk board is a physical machine that gets repointed once in a
// blue moon. Coupling them would mean every menu edit touched the row a TV
// depends on. Instead this app hands out a URL, and someone pastes that URL
// into a kiosk board once.
//
// The page renders SERVER-SIDE and ships as one self-contained document. The
// screen it feeds hangs on a wall with nobody watching it, so a page that
// needs a working API at paint time is a page that shows a spinner all
// evening. Fonts and the logo are embedded as data URIs for the same reason:
// no external request can fail. The only client-side JavaScript rotates the
// side panel, ticks a clock, and shrinks over-long beer names. If none of it
// runs, the board still shows every tap and the first panel.
//
// Sizing is built for a 55" 1080p screen (~40px per inch), where 46px beer
// names land near 0.8in of cap height -- legible from about fifteen feet and
// comfortable at eight to ten, which is where people stand at the bar.
package menu

import (
	"encoding/json"
	"fmt"
	"strings"
)

// DefaultPour is the house pour. Rows matching it carry no size label: on a
// seventeen-tap board that is ten labels printed to communicate seven
// exceptions, so the default is stated once in the footer and only the beers
// that differ are marked -- which also makes the smaller, stronger pours
// stand out, since that is where the per-ounce price climbs.
const DefaultPour = "16oz"

// Board is the whole rendered document: what is on tap, and what the side
// panel rotates through.
type Board struct {
	Venue  Venue   `json:"venue"`
	Taps   []Tap   `json:"taps"`
	Panels []Panel `json:"panels"`
}

// Venue carries the chrome around the tap list.
type Venue struct {
	Wordmark string   `json:"wordmark"`
	Footer   []string `json:"footer"`
}

// Tap is one beer. Price is a bare string rather than cents because it is
// display text on a menu board and never arithmetic -- "6.50" and "8" are
// both correct as written, and rounding either into the other would be wrong.
type Tap struct {
	Section string `json:"section"`
	Name    string `json:"name"`
	Style   string `json:"style"`
	ABV     string `json:"abv"`
	Price   string `json:"price"`
	Size    string `json:"size"`
}

// Panel kinds. Anything else is rejected at validation rather than silently
// rendering an empty box on a wall.
const (
	PanelAgenda = "agenda"
	PanelPoster = "poster"
	PanelCTA    = "cta"
)

// Panel is one slide in the rotating side rail.
type Panel struct {
	Kind   string  `json:"kind"`
	Label  string  `json:"label"`
	Events []Event `json:"events,omitempty"`

	// Poster.
	Image string `json:"image,omitempty"`
	Alt   string `json:"alt,omitempty"`

	// CTA.
	Headline string   `json:"headline,omitempty"`
	Body     string   `json:"body,omitempty"`
	Contact  []string `json:"contact,omitempty"`
}

// Event is one dated line in an agenda panel.
type Event struct {
	When  string `json:"when"`
	Time  string `json:"time"`
	Title string `json:"title"`
	Note  string `json:"note"`
}

// ParseBoard decodes and validates a board payload. Unknown fields are
// rejected: a typo'd key in a hand-authored document should fail the push
// rather than quietly render a board missing a section.
func ParseBoard(raw []byte) (*Board, error) {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var b Board
	if err := dec.Decode(&b); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrPayloadInvalid, err)
	}
	if err := b.Validate(); err != nil {
		return nil, err
	}
	return &b, nil
}

// Validate enforces the rules that would otherwise show up as a broken wall
// display rather than an error anyone sees.
func (b *Board) Validate() error {
	if len(b.Taps) == 0 {
		return fmt.Errorf("%w: a board needs at least one tap", ErrPayloadInvalid)
	}
	if len(b.Taps) > MaxTaps {
		return fmt.Errorf("%w: %d taps exceeds the %d the layout can show",
			ErrPayloadInvalid, len(b.Taps), MaxTaps)
	}
	for i, t := range b.Taps {
		if strings.TrimSpace(t.Name) == "" {
			return fmt.Errorf("%w: tap %d has no name", ErrPayloadInvalid, i+1)
		}
		if strings.TrimSpace(t.Section) == "" {
			return fmt.Errorf("%w: tap %q has no section", ErrPayloadInvalid, t.Name)
		}
	}
	for i, p := range b.Panels {
		if err := p.validate(i); err != nil {
			return err
		}
	}
	return nil
}

func (p *Panel) validate(i int) error {
	// An image is optional everywhere except a poster, but wherever it
	// appears it must be something the page can inline — the board never
	// fetches at paint time, whichever panel the image is on.
	if p.Image != "" && !strings.HasPrefix(p.Image, AssetRef) &&
		!strings.HasPrefix(p.Image, "data:image/") {
		return fmt.Errorf("%w: panel %d image must be %q plus an uploaded asset key, "+
			"or a data:image/ URI", ErrPayloadInvalid, i+1, AssetRef)
	}
	switch p.Kind {
	case PanelAgenda:
		if len(p.Events) == 0 {
			return fmt.Errorf("%w: agenda panel %d has no events", ErrPayloadInvalid, i+1)
		}
	case PanelPoster:
		if p.Image == "" {
			return fmt.Errorf("%w: poster panel %d has no image", ErrPayloadInvalid, i+1)
		}
	case PanelCTA:
		if strings.TrimSpace(p.Headline) == "" {
			return fmt.Errorf("%w: cta panel %d has no headline", ErrPayloadInvalid, i+1)
		}
	default:
		return fmt.Errorf("%w: panel %d has unknown kind %q (want agenda, poster, or cta)",
			ErrPayloadInvalid, i+1, p.Kind)
	}
	return nil
}

// SizeLabel is the pour label for a row, empty for the house default.
func (t Tap) SizeLabel() string {
	if t.Size == "" || t.Size == DefaultPour {
		return ""
	}
	return strings.Replace(t.Size, "oz", " oz", 1)
}
