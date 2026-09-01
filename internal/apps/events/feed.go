package events

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
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
func feedItem(e *Event, s Settings, until time.Time) FeedItem {
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
		Upcoming:        feedUpcoming(e, until),
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

// feedWindowMonths is how far ahead the feed reaches.
//
// The website is a "what's on" page, not an archive. A visitor is deciding
// what to do over the next few weeks, and a build handed every published event
// renders a wall of dates nobody has planned around yet -- which is exactly
// what happens to a venue that books a season ahead. Two months keeps the near
// term complete, including the far side of a month boundary, and stops there.
// Anything further out arrives on a later build, and the site rebuilds far
// more often than two months.
const feedWindowMonths = 2

// maxFeedEvents caps the feed even inside the window, because a busy venue can
// put forty things in eight weeks. The cut is made here, nearest first, rather
// than left to whoever writes the site template -- the template is someone
// else's repository, and this is the surface that knows what "soon" means.
// Nothing is lost permanently: the events past the cut move up as the ones
// ahead of them happen.
const maxFeedEvents = 20

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
	now := timeNow()
	until := now.AddDate(0, feedWindowMonths, 0)
	events, err := listEvents(ctx, s.pool, tenantID, ListFilter{
		Status:     StatusPublished,
		Visibility: VisibilityPublic,
		From:       &now,
		To:         &until,
		Limit:      500,
	})
	if err != nil {
		return Feed{}, err
	}

	entries := selectFeedEvents(events, until)
	if len(entries) > maxFeedEvents {
		// Worth a line: this is the one place events silently stop reaching
		// the website, and "why is my event missing" is otherwise unanswerable.
		slog.Info("events feed: trimmed to the cap", "tenant_id", tenantID,
			"kept", maxFeedEvents, "dropped", len(entries)-maxFeedEvents)
		entries = entries[:maxFeedEvents]
	}

	feed := Feed{
		GeneratedAt: now.UTC().Format(time.RFC3339),
		Events:      make([]FeedItem, 0, len(entries)),
	}
	for _, entry := range entries {
		item := feedItem(entry.event, settings, until)
		// Poster URL is assembled here rather than in feedItem because only
		// the caller knows the host. No hero, no field -- the site then falls
		// back to its own artwork instead of requesting a 404.
		if posterBase != "" && entry.event.HeroAttachmentID != nil {
			item.ImageURL = strings.TrimSuffix(posterBase, "/") + "/events/" + entry.event.Slug + "/poster"
		}
		feed.Events = append(feed.Events, item)
	}
	return feed, nil
}

// feedEntry pairs an event with the date a visitor would actually turn up on,
// which is not the same as its starts_at once recurrence is involved.
type feedEntry struct {
	event *Event
	next  time.Time
}

// selectFeedEvents keeps what genuinely happens inside the window and puts it
// in the order the site reads it: soonest first.
//
// Ordering by starts_at -- which is what the query gives back -- looks right
// until a weekly series is in the list. Trivia that began in 2023 stores 2023,
// so it sorts to the very top and, worse, would eat the cap ahead of events
// that are actually sooner. Ordering by the next real occurrence puts every
// kind of event on the same footing.
func selectFeedEvents(events []Event, until time.Time) []feedEntry {
	out := make([]feedEntry, 0, len(events))
	for i := range events {
		e := &events[i]
		// Belt and braces. The query already filters, but this is the one
		// predicate that decides what reaches the open web, so the serialiser
		// asks it too rather than trusting a WHERE clause to stay correct
		// through future edits.
		if !e.IsPubliclyVisible() {
			continue
		}
		next, ok := feedNext(e, until)
		if !ok {
			continue
		}
		out = append(out, feedEntry{event: e, next: next})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].next.Before(out[j].next) })
	return out
}

// feedNext is the next date the event actually happens, or false when it does
// not happen inside the window at all.
//
// For a one-off this is just starts_at; the query has already bounded it. A
// repeating row is the whole reason this exists. It stores its FIRST
// occurrence, and the query deliberately exempts repeating rows from the lower
// bound -- so a series whose rule ran out last spring still comes back from
// the database. Expanding is the only way to ask whether it still runs.
func feedNext(e *Event, until time.Time) (time.Time, bool) {
	if !e.Repeats() {
		return e.StartsAt, true
	}
	occ := e.Occurrences(feedExpandFrom(e), until)
	if len(occ) == 0 {
		return time.Time{}, false
	}
	return occ[0].Start, true
}

// feedExpandFrom is the start of today in the event's own zone.
//
// Expanding from this instant instead would drop an event still running this
// evening out of a build that happens mid-afternoon, which is when builds
// happen. A few hours of slack costs nothing and removes a whole class of
// "it vanished off the website while it was on" report.
func feedExpandFrom(e *Event) time.Time {
	loc := e.Loc()
	now := timeNow().In(loc)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
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
	// The ICS tiers sit behind the same bearer token as the JSON feed, even
	// though their whole purpose is to be subscribed to publicly. The site
	// build fetches them and republishes them token-free on the brewery's own
	// domain -- see feed_ics.go. Keeping the origin authenticated means the
	// public URL stays under our control rather than being whatever Kit
	// hostname a chamber's web admin happened to paste.
	mux.Handle("GET /{slug}/events/feed.ics",
		auth.TenantFromPath(a.pool)(http.HandlerFunc(a.handleFeedICS)))
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

// handleFeedICS serves one calendar tier.
//
// Auth and the not-configured 404 are deliberately identical to handleFeed:
// two endpoints over the same rows should not have two answers to "may you
// see this". Only the projection and the tier filter differ.
func (a *App) handleFeedICS(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenant := auth.TenantFromContext(ctx)
	if tenant == nil {
		http.NotFound(w, r)
		return
	}

	settings, err := getSettings(ctx, a.pool, tenant.ID)
	if err != nil {
		slog.Error("events ics: loading settings", "tenant_id", tenant.ID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if settings.FeedToken == "" {
		http.NotFound(w, r)
		return
	}
	if !validFeedToken(r, settings.FeedToken) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="events feed"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Default to the narrowest tier, not the widest. A typo in the query
	// string should under-share rather than hand a town calendar every happy
	// hour we run.
	tier := Tier(strings.TrimSpace(r.URL.Query().Get("tier")))
	if tier == "" {
		tier = TierFeatured
	}
	if !ValidTier(tier) {
		http.Error(w, "unknown tier", http.StatusBadRequest)
		return
	}

	body, err := a.svc.BuildICS(ctx, tenant.ID, tier, tenant.Name)
	if err != nil {
		slog.Error("events ics: building", "tenant_id", tenant.ID, "tier", tier, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	if _, err := io.WriteString(w, body); err != nil {
		slog.Warn("events ics: writing response", "tenant_id", tenant.ID, "error", err)
	}
}

// ICSFeedURL renders the URL a site build should fetch for one tier, for the
// admin page and the build script.
func ICSFeedURL(baseURL, tenantSlug string, tier Tier) string {
	return fmt.Sprintf("%s/%s/events/feed.ics?tier=%s", strings.TrimSuffix(baseURL, "/"), tenantSlug, tier)
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

// maxFeedOccurrences caps how many dates one item carries. The window usually
// binds first -- two months of a weekly series is nine dates -- so this is the
// backstop for the daily standing offer, which would otherwise ship sixty
// entries and read as a calendar export rather than a cadence.
const maxFeedOccurrences = 12

// feedUpcoming expands the occurrences that fall inside the feed's own window,
// in the event's own zone, so the site shows the time the doors actually open.
//
// The dates stop where the window stops, deliberately. A feed that only lists
// events for the next two months but hands a weekly series a year of dates is
// telling the site two different things about how far ahead it reaches, and
// the events page ends up padded with next spring's trivia. Recurrence rides
// alongside for consumers that want to say "every Tuesday" without a date.
//
// Nil for a one-off, which keeps the key absent from the JSON entirely rather
// than publishing a single-element array every consumer has to special-case:
// starts_at already says when a one-off happens.
func feedUpcoming(e *Event, until time.Time) []string {
	if !e.Repeats() {
		return nil
	}
	loc := e.Loc()
	occ := e.Occurrences(feedExpandFrom(e), until)
	if len(occ) > maxFeedOccurrences {
		occ = occ[:maxFeedOccurrences]
	}
	out := make([]string, len(occ))
	for i, o := range occ {
		out[i] = o.Start.In(loc).Format(time.RFC3339)
	}
	return out
}
