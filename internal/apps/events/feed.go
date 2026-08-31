package events

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/auth"
)

// The website feed. A static-site build fetches this and generates a page per
// event.
//
// Everything here is shaped by one rule: this is the open web. The calendar is
// an internal and partner surface where staff notes and a customer's name are
// fine; this is not. The payload is therefore assembled by its own function
// rather than by marshalling an Event, so a field added to the row does not
// silently start being published.

// FeedItem is the wire contract the website builds against. Changing a field
// name here breaks someone else's build, so it is deliberately narrow and
// explicit rather than a reflection of the database row.
type FeedItem struct {
	ID    string `json:"id"`
	Slug  string `json:"slug"`
	Title string `json:"title"`
	// Type routes layout and JSON-LD on the website side. Only "event" and
	// "festival" are produced today, but it is emitted from day one so
	// additional kinds can be added later without a breaking change to a
	// contract the site already depends on.
	Type string `json:"type"`

	Summary     string `json:"summary,omitempty"`
	Description string `json:"description,omitempty"`

	StartsAt string `json:"starts_at"`
	EndsAt   string `json:"ends_at,omitempty"`
	AllDay   bool   `json:"all_day,omitempty"`
	Timezone string `json:"timezone"`
	// Recurrence is passed through unexpanded. The site expands it for display;
	// sending N materialised instances instead would flood the events page with
	// near-duplicate entries and thin pages.
	Recurrence string `json:"recurrence,omitempty"`
	// Upcoming is the next few occurrences, already expanded, in the event's
	// own zone. It covers all three repeat mechanisms uniformly -- weekly,
	// monthly, and an explicit date list -- so a consumer needs no recurrence
	// logic of its own.
	//
	// This exists because "pass the rule through and let the site expand it"
	// only ever worked for the weekly case. A static site generator can walk
	// forward to the next Tuesday; it cannot reasonably work out the first
	// Friday of next month, and it has nothing at all to go on for a list of
	// hand-picked dates. Both would silently publish as a one-off on a start
	// date that may be months past. Kit already owns a tested expander that
	// handles DST, short months and fifth weekdays -- so it expands once, here,
	// rather than being reimplemented less carefully downstream.
	//
	// Still ONE feed item and one page per event: these are dates, not
	// materialised instances. Recurrence is kept alongside for consumers that
	// want to render the cadence in words ("every Tuesday").
	Upcoming []string `json:"upcoming,omitempty"`

	Location     string `json:"location,omitempty"`
	CanonicalURL string `json:"canonical_url,omitempty"`

	PriceCents      *int64 `json:"price_cents,omitempty"`
	Currency        string `json:"currency,omitempty"`
	Capacity        *int   `json:"capacity,omitempty"`
	RegistrationURL string `json:"registration_url,omitempty"`

	// Featured asks the site to lead with this one. The site decides how to
	// use it -- Kit only says which event matters most.
	//
	// Kept as a boolean even though the database now holds three values
	// (migration 079). This is a contract someone else's build depends on;
	// widening it here would break them for no gain, since "lead with this" is
	// still exactly one question. Prominence carries the rest.
	Featured bool `json:"featured,omitempty"`
	// Prominence is the full editorial axis: "featured", "normal" or
	// "background". Additive and optional, so a site that ignores it behaves
	// exactly as before -- but a site that reads it can stop giving a standing
	// pizza offer the same full event page as the anniversary party.
	Prominence string `json:"prominence,omitempty"`

	// ImageURL is the event's poster, absent when none was uploaded. The site
	// falls back to its own artwork rather than showing a gap.
	ImageURL string `json:"image_url,omitempty"`

	UpdatedAt string `json:"updated_at"`
}

// Feed is the whole response.
type Feed struct {
	GeneratedAt string     `json:"generated_at"`
	Events      []FeedItem `json:"events"`
}

// feedItem projects one event onto the public wire contract.
//
// Note what is absent and must stay absent: prep_notes (the internal staff
// brief), expected_attendance, space_impact, notify_food_partner, and the
// created_by user. Those exist for the team, and the calendar is where the
// team reads them.
func feedItem(e *Event, s Settings) FeedItem {
	item := FeedItem{
		ID:              e.ID.String(),
		Slug:            e.Slug,
		Title:           e.Title,
		Type:            feedType(e),
		Summary:         e.Summary,
		Description:     e.Description,
		StartsAt:        e.StartsAt.In(e.Loc()).Format(time.RFC3339),
		AllDay:          e.AllDay,
		Timezone:        e.Timezone,
		Recurrence:      e.RRule,
		Upcoming:        feedUpcoming(e),
		Featured:        e.IsFeatured(),
		Prominence:      string(e.Prominence),
		Location:        e.Location,
		CanonicalURL:    s.CanonicalURL(e.Slug),
		PriceCents:      e.PriceCents,
		Capacity:        e.Capacity,
		RegistrationURL: e.RegistrationURL,
		UpdatedAt:       e.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if e.EndsAt != nil {
		item.EndsAt = e.EndsAt.In(e.Loc()).Format(time.RFC3339)
	}
	if e.PriceCents != nil {
		item.Currency = e.Currency
	}
	return item
}

// feedType maps the operational venue axis onto the presentation type the
// website routes on. An offsite appearance is still public -- "come see us at
// the festival" belongs on the site -- it just renders differently.
func feedType(e *Event) string {
	if e.Venue == VenueOffsite {
		return "festival"
	}
	return "event"
}

// BuildFeed assembles the public feed for a tenant.
//
// posterBase is the "https://host/tenant-slug" prefix used to build absolute
// poster URLs; pass "" and posters are simply omitted. It is a parameter
// rather than config because only the request knows the host it was reached
// on, and a feed consumed by an external build needs absolute URLs.
func (s *Service) BuildFeed(ctx context.Context, tenantID uuid.UUID, posterBase string) (Feed, error) {
	settings, err := getSettings(ctx, s.pool, tenantID)
	if err != nil {
		return Feed{}, err
	}
	now := time.Now()
	events, err := listEvents(ctx, s.pool, tenantID, ListFilter{
		Status:     StatusPublished,
		Visibility: VisibilityPublic,
		From:       &now,
		Limit:      500,
	})
	if err != nil {
		return Feed{}, err
	}

	feed := Feed{
		GeneratedAt: now.UTC().Format(time.RFC3339),
		Events:      make([]FeedItem, 0, len(events)),
	}
	for i := range events {
		e := &events[i]
		// Belt and braces. The query already filters, but this is the one
		// predicate that decides what reaches the open web, so the serialiser
		// asks it too rather than trusting a WHERE clause to stay correct
		// through future edits.
		if !e.IsPubliclyVisible() {
			continue
		}
		item := feedItem(e, settings)
		// Poster URL is assembled here rather than in feedItem because only
		// the caller knows the host. No hero, no field -- the site then falls
		// back to its own artwork instead of requesting a 404.
		if posterBase != "" && e.HeroAttachmentID != nil {
			item.ImageURL = strings.TrimSuffix(posterBase, "/") + "/events/" + e.Slug + "/poster"
		}
		feed.Events = append(feed.Events, item)
	}
	return feed, nil
}

// registerFeedRoutes mounts the public feed.
//
// The route carries {slug}, so it goes through TenantFromPath for resolution
// and — importantly — through the app-enablement gate: disabling the events app
// 404s the feed rather than leaving a live endpoint behind. There is
// deliberately no session middleware; a site build has no Kit session, it has
// a bearer token.
func (a *App) registerFeedRoutes(mux apps.Mux) {
	mux.Handle("GET /{slug}/events/feed.json",
		auth.TenantFromPath(a.pool)(http.HandlerFunc(a.handleFeed)))
	// Posters ride the same public path: no session, gated only by
	// IsPubliclyVisible. See poster.go for why they are unsigned.
	mux.Handle("GET /{slug}/events/{event}/poster",
		auth.TenantFromPath(a.pool)(http.HandlerFunc(a.handleServePoster)))
}

// handleFeed serves the build-time feed.
//
// Authentication is a bearer token rather than nothing. The events are public
// information, but consumption is server-side from a site build, so a shared
// secret costs the consumer nothing and keeps the endpoint off casual
// scrapers. It is compared in constant time -- cheap, and the alternative
// leaks token bytes through timing to anyone who can hit a public URL.
func (a *App) handleFeed(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenant := auth.TenantFromContext(ctx)
	if tenant == nil {
		http.NotFound(w, r)
		return
	}

	settings, err := getSettings(ctx, a.pool, tenant.ID)
	if err != nil {
		slog.Error("events feed: loading settings", "tenant_id", tenant.ID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if settings.FeedToken == "" {
		// Nothing configured yet: 404 rather than an empty feed, so a
		// misconfigured build fails loudly instead of publishing an empty page.
		http.NotFound(w, r)
		return
	}
	if !validFeedToken(r, settings.FeedToken) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="events feed"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	feed, err := a.svc.BuildFeed(ctx, tenant.ID, baseURLFrom(r)+"/"+tenant.Slug)
	if err != nil {
		slog.Error("events feed: building", "tenant_id", tenant.ID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	if err := json.NewEncoder(w).Encode(feed); err != nil {
		slog.Warn("events feed: writing response", "tenant_id", tenant.ID, "error", err)
	}
}

// validFeedToken accepts the token as a bearer header, which is what a build
// script sends.
func validFeedToken(r *http.Request, want string) bool {
	header := r.Header.Get("Authorization")
	got, ok := strings.CutPrefix(header, "Bearer ")
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimSpace(got)), []byte(want)) == 1
}

// FeedURL renders the URL a site build should fetch, for the admin page.
func FeedURL(baseURL, tenantSlug string) string {
	return fmt.Sprintf("%s/%s/events/feed.json", strings.TrimSuffix(baseURL, "/"), tenantSlug)
}

// maxFeedOccurrences caps how many dates one item carries. Enough to show a
// season of a monthly series or a full course, short of turning the feed into
// a calendar export.
const maxFeedOccurrences = 12

// feedUpcomingWindow is how far ahead to look. A year keeps an unbounded weekly
// series honest without expanding it forever, and is well past the point where
// a static site would have rebuilt anyway.
const feedUpcomingWindow = 12 // months

// feedUpcoming expands the next few occurrences in the event's own zone, so the
// site shows the time the doors actually open.
//
// The window starts slightly in the past -- at the beginning of today rather
// than at this instant -- so an event still running this evening does not
// vanish from the site mid-afternoon. The feed is consumed by a build that may
// happen at any hour.
//
// Nil for a one-off, which keeps the key absent from the JSON entirely rather
// than publishing a single-element array every consumer has to special-case:
// starts_at already says when a one-off happens.
func feedUpcoming(e *Event) []string {
	if !e.Repeats() {
		return nil
	}
	loc := e.Loc()
	now := timeNow().In(loc)
	from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)

	occ := e.Occurrences(from, from.AddDate(0, feedUpcomingWindow, 0))
	if len(occ) > maxFeedOccurrences {
		occ = occ[:maxFeedOccurrences]
	}
	out := make([]string, len(occ))
	for i, o := range occ {
		out[i] = o.Start.In(loc).Format(time.RFC3339)
	}
	return out
}
