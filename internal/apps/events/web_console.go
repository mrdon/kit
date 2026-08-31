package events

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/console"
	"github.com/mrdon/kit/internal/auth"
)

// registerConsoleRoutes wires the console JSON API.
//
// Events are a shared team surface -- anyone in the workspace can author and
// publish one -- so the CRUD routes use console.JSON (a caller is required, but
// not an admin). Configuration is admin-only and uses console.AdminJSON, which
// enforces that at the middleware rather than trusting the client to hide a
// button.
func registerConsoleRoutes(mux apps.Mux, a *App) {
	jsonRoute := func(h http.HandlerFunc) http.Handler {
		return console.JSON(a.pool, a.signer, h)
	}

	mux.Handle("GET /{slug}/api/events", jsonRoute(a.handleList))
	mux.Handle("POST /{slug}/api/events", jsonRoute(a.handleCreate))
	mux.Handle("GET /{slug}/api/events/meta", jsonRoute(a.handleMeta))
	// Not admin-gated: anyone who can publish an event can put it on the
	// website. Configuring the build hook stays admin-only -- that is a
	// credential, this is the everyday action it enables.
	mux.Handle("GET /{slug}/api/events/site", jsonRoute(a.handleSiteStatus))
	mux.Handle("POST /{slug}/api/events/site/publish", jsonRoute(a.handleSitePublish))
	mux.Handle("GET /{slug}/api/events/{id}", jsonRoute(a.handleGet))
	mux.Handle("PATCH /{slug}/api/events/{id}", jsonRoute(a.handleUpdate))
	mux.Handle("POST /{slug}/api/events/{id}/clone", jsonRoute(a.handleClone))
	mux.Handle("DELETE /{slug}/api/events/{id}", jsonRoute(a.handleDelete))
	mux.Handle("GET /{slug}/api/events/{id}/poster", jsonRoute(a.handleConsolePoster))
	mux.Handle("POST /{slug}/api/events/{id}/poster", jsonRoute(a.handleUploadPoster))
	mux.Handle("DELETE /{slug}/api/events/{id}/poster", jsonRoute(a.handleDeletePoster))
	mux.Handle("POST /{slug}/api/events/{id}/publish", jsonRoute(a.handlePublish))
	mux.Handle("POST /{slug}/api/events/{id}/unpublish", jsonRoute(a.handleTransition(unpublishAction)))
	mux.Handle("POST /{slug}/api/events/{id}/cancel", jsonRoute(a.handleTransition(cancelAction)))
	mux.Handle("POST /{slug}/api/events/{id}/reopen", jsonRoute(a.handleTransition(reopenAction)))
}

type transitionAction string

const (
	unpublishAction transitionAction = "unpublish"
	cancelAction    transitionAction = "cancel"
	reopenAction    transitionAction = "reopen"
)

func (a *App) handleList(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	q := r.URL.Query()

	settings, err := a.svc.Settings(r.Context(), caller.TenantID)
	if err != nil {
		a.serviceErr(w, err)
		return
	}
	f := ListFilter{
		Status:     Status(strings.ToLower(q.Get("status"))),
		Visibility: Visibility(strings.ToLower(q.Get("visibility"))),
	}
	// "upcoming" is the default view; past and cancelled events are opt-in,
	// because the list people want on opening the page is what is coming, not
	// the archive. Cancelled rides the same toggle rather than vanishing
	// outright -- reopening one means finding it first.
	if q.Get("include_past") != "true" {
		now := timeNow()
		f.From = &now
		f.ExcludeCancelled = true
	}
	events, err := a.svc.List(r.Context(), caller.TenantID, f)
	if err != nil {
		a.serviceErr(w, err)
		return
	}
	if events == nil {
		events = []Event{}
	}
	eventsJSON(w, http.StatusOK, map[string]any{
		"events":   events,
		"settings": publicSettings(settings),
	})
}

func (a *App) handleGet(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	e, err := a.svc.Get(r.Context(), caller.TenantID, id)
	if err != nil {
		a.serviceErr(w, err)
		return
	}
	settings, err := a.svc.Settings(r.Context(), caller.TenantID)
	if err != nil {
		a.serviceErr(w, err)
		return
	}
	eventsJSON(w, http.StatusOK, map[string]any{
		"event":        e,
		"occurrences":  upcomingOccurrences(e, 12),
		"canonicalURL": canonicalIfPublic(e, settings),
	})
}

// eventBody is the console's wire shape for create and update. Every field is
// a pointer so PATCH can distinguish "not sent" from "cleared" -- the console
// form and the chat agent edit the same rows, and a full-object PUT would let
// whichever saved last silently revert the other's change.
type eventBody struct {
	Title       *string `json:"title"`
	Summary     *string `json:"summary"`
	Description *string `json:"description"`
	PrepNotes   *string `json:"prep_notes"`
	Location    *string `json:"location"`

	StartsAt   *string `json:"starts_at"`
	EndsAt     *string `json:"ends_at"`
	AllDay     *bool   `json:"all_day"`
	Timezone   *string `json:"timezone"`
	RepeatRule *string `json:"repeat_rule"`
	// RepeatDates carries the whole extra-date list. Pointer for the same
	// reason as the rest: PATCH must tell "not sent" from "cleared", and an
	// empty list means "back to a one-off".
	RepeatDates *[]string `json:"repeat_dates"`

	Visibility  *string `json:"visibility"`
	Venue       *string `json:"venue"`
	SpaceImpact *string `json:"space_impact"`

	PriceCents         *int64  `json:"price_cents"`
	ClearPrice         bool    `json:"clear_price"`
	Currency           *string `json:"currency"`
	Capacity           *int    `json:"capacity"`
	ClearCapacity      bool    `json:"clear_capacity"`
	ExpectedAttendance *int    `json:"expected_attendance"`
	RegistrationURL    *string `json:"registration_url"`
	NotifyFoodPartner  *bool   `json:"notify_food_partner"`
	Featured           *bool   `json:"featured"`
	Slug               *string `json:"slug"`
}

func (a *App) handleCreate(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	var body eventBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		eventsErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Title == nil || strings.TrimSpace(*body.Title) == "" {
		eventsErr(w, http.StatusBadRequest, "title is required")
		return
	}
	if body.StartsAt == nil || strings.TrimSpace(*body.StartsAt) == "" {
		eventsErr(w, http.StatusBadRequest, "a start time is required")
		return
	}
	p := CreateParams{
		Title:              *body.Title,
		Summary:            derefOr(body.Summary),
		Description:        derefOr(body.Description),
		PrepNotes:          derefOr(body.PrepNotes),
		Location:           derefOr(body.Location),
		StartsAt:           *body.StartsAt,
		EndsAt:             derefOr(body.EndsAt),
		AllDay:             body.AllDay != nil && *body.AllDay,
		Timezone:           derefOr(body.Timezone),
		RRule:              derefOr(body.RepeatRule),
		RepeatDates:        derefSlice(body.RepeatDates),
		Visibility:         Visibility(strings.ToLower(derefOr(body.Visibility))),
		Venue:              Venue(strings.ToLower(derefOr(body.Venue))),
		SpaceImpact:        SpaceImpact(strings.ToLower(derefOr(body.SpaceImpact))),
		PriceCents:         body.PriceCents,
		Currency:           derefOr(body.Currency),
		Capacity:           body.Capacity,
		ExpectedAttendance: body.ExpectedAttendance,
		RegistrationURL:    derefOr(body.RegistrationURL),
		NotifyFoodPartner:  body.NotifyFoodPartner,
		Featured:           body.Featured,
	}
	if caller.UserID != uuid.Nil {
		id := caller.UserID
		p.CreatedBy = &id
	}
	e, err := a.svc.Create(r.Context(), caller.TenantID, p)
	if err != nil {
		a.serviceErr(w, err)
		return
	}
	eventsJSON(w, http.StatusCreated, map[string]any{"event": e})
}

func (a *App) handleUpdate(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	var body eventBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		eventsErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	p := UpdateParams{
		Title: body.Title, Summary: body.Summary, Description: body.Description,
		PrepNotes: body.PrepNotes, Location: body.Location,
		StartsAt: body.StartsAt, EndsAt: body.EndsAt, AllDay: body.AllDay,
		Timezone: body.Timezone, RRule: body.RepeatRule,
		RepeatDates: body.RepeatDates,
		PriceCents:  body.PriceCents, ClearPrice: body.ClearPrice,
		Currency: body.Currency, Capacity: body.Capacity, ClearCapacity: body.ClearCapacity,
		ExpectedAttendance: body.ExpectedAttendance,
		RegistrationURL:    body.RegistrationURL,
		NotifyFoodPartner:  body.NotifyFoodPartner,
		Featured:           body.Featured,
		Slug:               body.Slug,
	}
	if body.Visibility != nil {
		v := Visibility(strings.ToLower(*body.Visibility))
		p.Visibility = &v
	}
	if body.Venue != nil {
		v := Venue(strings.ToLower(*body.Venue))
		p.Venue = &v
	}
	if body.SpaceImpact != nil {
		v := SpaceImpact(strings.ToLower(*body.SpaceImpact))
		p.SpaceImpact = &v
	}
	e, err := a.svc.Update(r.Context(), caller.TenantID, id, p)
	if err != nil {
		a.serviceErr(w, err)
		return
	}
	eventsJSON(w, http.StatusOK, map[string]any{"event": e})
}

func (a *App) handlePublish(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	res, err := a.svc.Publish(r.Context(), caller.TenantID, id)
	if err != nil {
		a.serviceErr(w, err)
		return
	}
	// Warnings ride along rather than blocking: a paid event with no ticket
	// link is probably a mistake, but refusing the publish would be worse.
	eventsJSON(w, http.StatusOK, map[string]any{
		"event":    res.Event,
		"warnings": res.Warnings,
	})
}

func (a *App) handleTransition(action transitionAction) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := auth.CallerFromContext(r.Context())
		id, ok := pathUUID(w, r)
		if !ok {
			return
		}
		var (
			e   *Event
			err error
		)
		switch action {
		case unpublishAction:
			e, err = a.svc.Unpublish(r.Context(), caller.TenantID, id)
		case cancelAction:
			e, err = a.svc.Cancel(r.Context(), caller.TenantID, id)
		case reopenAction:
			e, err = a.svc.Reopen(r.Context(), caller.TenantID, id)
		}
		if err != nil {
			a.serviceErr(w, err)
			return
		}
		eventsJSON(w, http.StatusOK, map[string]any{"event": e})
	}
}

// handleMeta feeds the form's dropdowns so the client never hardcodes the
// vocabulary. Adding an enum value server-side then reaches the UI without a
// matching frontend edit.
func (a *App) handleMeta(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	settings, err := a.svc.Settings(r.Context(), caller.TenantID)
	if err != nil {
		a.serviceErr(w, err)
		return
	}
	eventsJSON(w, http.StatusOK, map[string]any{
		"statuses":      []Status{StatusDraft, StatusPublished, StatusCancelled},
		"visibilities":  []Visibility{VisibilityPrivate, VisibilityPublic},
		"venues":        []Venue{VenueOnsite, VenueOffsite},
		"space_impacts": []SpaceImpact{SpaceImpactNone, SpaceImpactPartial},
		"settings":      publicSettings(settings),
	})
}

// publicSettings is the non-secret slice of configuration the everyday page
// may see. The feed token is admin-only and lives on the settings endpoint.
func publicSettings(s Settings) map[string]any {
	return map[string]any{
		"timezone":            firstNonEmpty(s.Timezone, DefaultTimezone),
		"calendar_configured": s.CalendarConfigured(),
		"public_url_template": s.PublicURLTemplate,
	}
}

func canonicalIfPublic(e *Event, s Settings) string {
	if !e.IsPubliclyVisible() {
		return ""
	}
	return s.CanonicalURL(e.Slug)
}

// upcomingOccurrences lists the next few dates a repeating event falls on, so
// the detail view can show them rather than leaving the reader to decode an
// RRULE.
func upcomingOccurrences(e *Event, limit int) []string {
	if !e.Repeats() {
		return nil
	}
	now := timeNow()
	occ := e.Occurrences(now, now.AddDate(1, 0, 0))
	if len(occ) > limit {
		occ = occ[:limit]
	}
	loc := e.Loc()
	out := make([]string, len(occ))
	for i, o := range occ {
		out[i] = o.Start.In(loc).Format("2006-01-02 15:04")
	}
	return out
}

func pathUUID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		eventsErr(w, http.StatusBadRequest, "invalid event id")
		return uuid.Nil, false
	}
	return id, true
}

// serviceErr maps a known domain error to a status and message, and anything
// else to a 500 with the detail logged rather than returned.
func (a *App) serviceErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		eventsErr(w, http.StatusNotFound, "event not found")
	case errors.Is(err, ErrInvalid):
		eventsErr(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), "invalid event: "))
	case errors.Is(err, ErrNoCalendar):
		eventsErr(w, http.StatusBadRequest, "no calendar is selected yet")
	default:
		slog.Error("events console: request failed", "error", err)
		eventsErr(w, http.StatusInternalServerError, "internal error")
	}
}

func eventsJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		slog.Warn("events console: writing response", "error", err)
	}
}

func eventsErr(w http.ResponseWriter, status int, msg string) {
	eventsJSON(w, status, map[string]any{"error": msg})
}

func derefOr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// handleClone copies an event into a new draft. Every rule about what a copy
// inherits lives in Service.Clone, so the button and the chat agent produce
// byte-identical rows.
func (a *App) handleClone(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	// A body is optional: the plain "duplicate" button sends none.
	var body struct {
		StartsAt string `json:"starts_at"`
		Title    string `json:"title"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	p := CloneParams{Title: body.Title, StartsAt: body.StartsAt}
	if caller.UserID != uuid.Nil {
		uid := caller.UserID
		p.CreatedBy = &uid
	}
	e, err := a.svc.Clone(r.Context(), caller.TenantID, id, p)
	if err != nil {
		a.serviceErr(w, err)
		return
	}
	eventsJSON(w, http.StatusCreated, map[string]any{"event": e})
}

// handleDelete destroys an event permanently. The guard lives in the service,
// so the agent and MCP surfaces get the same rule.
func (a *App) handleDelete(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		eventsErr(w, http.StatusBadRequest, "invalid event id")
		return
	}
	if err := a.svc.Delete(r.Context(), caller.TenantID, id); err != nil {
		a.serviceErr(w, err)
		return
	}
	eventsJSON(w, http.StatusOK, map[string]any{"deleted": true})
}
