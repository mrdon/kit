package menu

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"html/template"
	"log/slog"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/events"
)

// Panels the events calendar fills in.
//
// A dated panel typed into the stored board is wrong the morning after its
// night: stored text does not expire. These are expanded from the calendar on
// every page load instead, so a poster comes down when its event ends and a
// renamed series is renamed on the wall without anyone touching the board.
const (
	// SourceUpcoming expands to one poster panel per one-off night at the
	// brewery this week that has a poster, soonest first. None, if none.
	SourceUpcoming = "events:upcoming"
	// SourceWeekly fills an agenda with the series that repeat on a rule.
	SourceWeekly = "events:weekly"
	// SourceLabel takes a cta's image from the poster of the next event
	// carrying the label after the colon, keeping the stored image when no
	// such event is booked. The text stays hand-written: Give Back copy
	// carries a per-pint figure Colorado requires word for word.
	SourceLabel = "events:label:"
)

// posterAsset prefixes the asset key a calendar poster is served under, so it
// can never collide with an image someone stored by hand.
const posterAsset = "event-poster:"

// eventFeed is the slice of the events app the board reads. An interface so
// the expansion can be tested without a database.
type eventFeed interface {
	BoardFeed(ctx context.Context, tenantID uuid.UUID, req events.BoardRequest) (*events.BoardFeed, error)
	BoardPoster(ctx context.Context, tenantID, posterID uuid.UUID) template.URL
}

// boardEvents is the events app, or nil when it is not running.
var boardEvents = func() eventFeed {
	if app := events.Instance(); app != nil {
		return app
	}
	return nil
}

func (p *Panel) validateEvents(i int) error {
	if p.Event != "" {
		if _, err := uuid.Parse(p.Event); err != nil {
			return fmt.Errorf("%w: panel %d event %q is not an event id", ErrPayloadInvalid, i+1, p.Event)
		}
	}
	want := ""
	switch {
	case p.Source == "":
		return nil
	case p.Source == SourceUpcoming:
		want = PanelPoster
	case p.Source == SourceWeekly:
		want = PanelAgenda
	case strings.HasPrefix(p.Source, SourceLabel) && len(p.Source) > len(SourceLabel):
		want = PanelCTA
	default:
		return fmt.Errorf("%w: panel %d has unknown source %q (want %s, %s or %s<label>)",
			ErrPayloadInvalid, i+1, p.Source, SourceUpcoming, SourceWeekly, SourceLabel)
	}
	if p.Kind != want {
		return fmt.Errorf("%w: panel %d source %q needs kind %q", ErrPayloadInvalid, i+1, p.Source, want)
	}
	return nil
}

// applyEventPanels expands the board's calendar panels in place and returns
// a stamp of the result for the version the wall polls. With assets non-nil
// it also loads the posters the expanded panels show into it.
//
// If the calendar cannot be read the stored panels stand as written: a
// missing poster costs a panel, never the tap list beside it.
func (a *App) applyEventPanels(ctx context.Context, tenantID uuid.UUID, b *Board, assets map[string]string) string {
	req, ok := eventRequest(b.Panels)
	feed := boardEvents()
	if !ok || feed == nil {
		return ""
	}
	f, err := feed.BoardFeed(ctx, tenantID, req)
	if err != nil {
		slog.Warn("reading events for the menu board", "tenant_id", tenantID, "error", err)
		return ""
	}
	b.Panels = expandEventPanels(b.Panels, f)
	if assets != nil {
		for _, p := range b.Panels {
			key, ok := strings.CutPrefix(p.Image, AssetRef+posterAsset)
			if !ok {
				continue
			}
			if id, err := uuid.Parse(key); err == nil {
				assets[posterAsset+key] = string(feed.BoardPoster(ctx, tenantID, id))
			}
		}
	}
	return panelStamp(b.Panels, f.Today)
}

// eventRequest collects what the stored panels ask of the calendar, and
// reports whether they ask anything at all.
func eventRequest(panels []Panel) (events.BoardRequest, bool) {
	var req events.BoardRequest
	ask := false
	for _, p := range panels {
		if id, err := uuid.Parse(p.Event); err == nil {
			req.Claimed = append(req.Claimed, id)
			ask = true
		}
		if label, ok := strings.CutPrefix(p.Source, SourceLabel); ok {
			req.Labels = append(req.Labels, label)
		}
		ask = ask || p.Source != ""
	}
	return req, ask
}

// expandEventPanels is the pure half of applyEventPanels.
func expandEventPanels(panels []Panel, f *events.BoardFeed) []Panel {
	out := make([]Panel, 0, len(panels))
	for _, p := range panels {
		if id, err := uuid.Parse(p.Event); err == nil && !f.Live[id] {
			continue
		}
		switch {
		case p.Source == SourceUpcoming:
			out = append(out, upcomingPanels(p, f.Upcoming)...)
		case p.Source == SourceWeekly:
			if p.Events = weeklyEvents(f.Weekly, p.Events); len(p.Events) > 0 {
				out = append(out, p)
			}
		case strings.HasPrefix(p.Source, SourceLabel):
			if ev, ok := f.ByLabel[strings.TrimPrefix(p.Source, SourceLabel)]; ok && ev.PosterID != nil {
				p.Image = AssetRef + posterAsset + ev.PosterID.String()
			}
			out = append(out, p)
		default:
			out = append(out, p)
		}
	}
	return out
}

// upcomingPanels is one poster panel per event, labelled with when it is so
// the wall reads "Saturday · Halloween Party" without a date ever being
// stored.
func upcomingPanels(tmpl Panel, evs []events.BoardEvent) []Panel {
	out := make([]Panel, 0, len(evs))
	for _, ev := range evs {
		p := tmpl
		p.Source = ""
		p.Label = ev.When + " · " + ev.Title
		p.Image = AssetRef + posterAsset + ev.PosterID.String()
		p.Alt = "Poster for " + ev.Title
		out = append(out, p)
	}
	return out
}

// weeklyEvents turns the calendar's series into agenda lines. With nothing
// on the calendar the stored lines stand, so an empty week never blanks a
// panel someone wrote.
func weeklyEvents(weekly []events.BoardWeekly, stored []Event) []Event {
	if len(weekly) == 0 {
		return stored
	}
	out := make([]Event, len(weekly))
	for i, w := range weekly {
		out[i] = Event{When: w.When, Time: w.Time, Title: w.Title, Note: w.Note}
	}
	return out
}

// panelStamp changes whenever the expanded panels do, and at midnight, when
// "Tomorrow" becomes "Tonight".
func panelStamp(panels []Panel, today string) string {
	raw, err := json.Marshal(panels)
	if err != nil {
		return today
	}
	h := fnv.New64a()
	_, _ = h.Write(raw)
	_, _ = h.Write([]byte(today))
	return strconv.FormatUint(h.Sum64(), 36)
}
