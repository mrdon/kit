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
	"github.com/mrdon/kit/internal/apps/googlecalendar"
	"github.com/mrdon/kit/internal/auth"
)

// registerSettingsRoutes wires the admin area. All admin-only.
func registerSettingsRoutes(mux apps.Mux, a *App) {
	adminRoute := func(h http.HandlerFunc) http.Handler {
		return console.AdminJSON(a.pool, a.signer, h)
	}
	mux.Handle("GET /{slug}/api/events/settings", adminRoute(a.handleGetSettings))
	mux.Handle("PUT /{slug}/api/events/settings", adminRoute(a.handleSaveSettings))
	mux.Handle("POST /{slug}/api/events/settings/feed-token", adminRoute(a.handleRotateFeedToken))
	mux.Handle("POST /{slug}/api/events/sync", adminRoute(a.handleSyncNow))
	mux.Handle("POST /{slug}/api/events/reconcile", adminRoute(a.handleReconcile))
}

// calendarOption is one entry in the picker.
type calendarOption struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Writable bool   `json:"writable"`
	Primary  bool   `json:"primary"`
}

type settingsPayload struct {
	CalendarID        string `json:"calendar_id"`
	Timezone          string `json:"timezone"`
	PublicURLTemplate string `json:"public_url_template"`
	FeedToken         string `json:"feed_token,omitempty"`
	FeedURL           string `json:"feed_url,omitempty"`

	GoogleConnected bool             `json:"google_connected"`
	Calendars       []calendarOption `json:"calendars"`
	// CalendarsError explains why the picker is empty instead of leaving the
	// admin staring at an empty dropdown with no reason given.
	CalendarsError string `json:"calendars_error,omitempty"`

	Recent []runPayload `json:"recent"`
}

type runPayload struct {
	At          string `json:"at"`
	OK          bool   `json:"ok"`
	TriggeredBy string `json:"triggered_by"`
	Created     int    `json:"created"`
	Updated     int    `json:"updated"`
	Deleted     int    `json:"deleted"`
	Error       string `json:"error,omitempty"`
}

func (a *App) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	payload, err := a.buildSettingsPayload(r, caller.TenantID)
	if err != nil {
		a.serviceErr(w, err)
		return
	}
	eventsJSON(w, http.StatusOK, payload)
}

// buildSettingsPayload assembles configuration plus the calendar options.
//
// The options ride along on this response rather than living behind a separate
// endpoint, following the Netlify site picker: one round trip, and the client
// cannot render a half-loaded picker. A listing failure degrades to an
// explanatory string rather than failing the whole page -- an admin who cannot
// reach Google should still be able to set the URL template.
func (a *App) buildSettingsPayload(r *http.Request, tenantID uuid.UUID) (settingsPayload, error) {
	ctx := r.Context()
	settings, err := a.svc.Settings(ctx, tenantID)
	if err != nil {
		return settingsPayload{}, err
	}
	payload := settingsPayload{
		CalendarID:        settings.CalendarID,
		Timezone:          firstNonEmpty(settings.Timezone, DefaultTimezone),
		PublicURLTemplate: settings.PublicURLTemplate,
		FeedToken:         settings.FeedToken,
	}
	if tenant := auth.TenantFromContext(ctx); tenant != nil && settings.FeedToken != "" {
		payload.FeedURL = FeedURL(baseURLFrom(r), tenant.Slug)
	}

	client, err := googlecalendar.Instance().LoadClientOnly(ctx, tenantID)
	switch {
	case errors.Is(err, googlecalendar.ErrNotConfigured):
		payload.CalendarsError = "Google Calendar is not connected yet. Connect it on the Integrations page, then share the events calendar with the service account."
	case err != nil:
		slog.Warn("events: loading calendar client", "tenant_id", tenantID, "error", err)
		payload.CalendarsError = "Could not reach Google Calendar: " + err.Error()
	default:
		payload.GoogleConnected = true
		payload.Calendars, payload.CalendarsError = listCalendarOptions(r, client)
	}

	runs, err := a.ListRecentRuns(ctx, tenantID, 8)
	if err != nil {
		return settingsPayload{}, err
	}
	for _, run := range runs {
		payload.Recent = append(payload.Recent, runPayload{
			At:          run.CreatedAt.Format("2006-01-02 15:04"),
			OK:          run.Succeeded(),
			TriggeredBy: run.Meta.TriggeredBy,
			Created:     run.Meta.Created,
			Updated:     run.Meta.Updated,
			Deleted:     run.Meta.Deleted,
			Error:       run.Meta.Error,
		})
	}
	return payload, nil
}

// listCalendarOptions returns the pickable calendars, or an explanation.
//
// An empty list is the expected first experience, not a bug: a service account
// only sees calendars explicitly shared with it. Saying so is the difference
// between a two-minute fix and a support conversation.
func listCalendarOptions(r *http.Request, client *googlecalendar.Client) ([]calendarOption, string) {
	entries, err := client.ListCalendars(r.Context())
	if err != nil {
		return nil, "Could not list calendars: " + err.Error()
	}
	var out []calendarOption
	for _, e := range entries {
		out = append(out, calendarOption{
			ID: e.ID, Name: firstNonEmpty(e.Summary, e.ID),
			Writable: e.Writable(), Primary: e.Primary,
		})
	}
	if len(out) == 0 {
		return nil, "The service account cannot see any calendars yet. In Google Calendar, share the events calendar with the service account's email address and give it 'Make changes to events'."
	}
	return out, ""
}

func (a *App) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	var body struct {
		CalendarID        *string `json:"calendar_id"`
		Timezone          *string `json:"timezone"`
		PublicURLTemplate *string `json:"public_url_template"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		eventsErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	current, err := a.svc.Settings(r.Context(), caller.TenantID)
	if err != nil {
		a.serviceErr(w, err)
		return
	}
	next := current
	next.TenantID = caller.TenantID
	if body.CalendarID != nil {
		next.CalendarID = strings.TrimSpace(*body.CalendarID)
	}
	if body.Timezone != nil {
		next.Timezone = strings.TrimSpace(*body.Timezone)
	}
	if body.PublicURLTemplate != nil {
		tpl := strings.TrimSpace(*body.PublicURLTemplate)
		if tpl != "" && !strings.Contains(tpl, "{slug}") {
			eventsErr(w, http.StatusBadRequest, "the website URL template must contain {slug}")
			return
		}
		next.PublicURLTemplate = tpl
	}

	// Repointing at a different calendar strands every event already written
	// to the old one: reconcile only queries the configured calendar, so it
	// would never see them again. Warn rather than refuse -- the operator may
	// be moving deliberately -- but say what will happen and how to fix it.
	var warning string
	if current.CalendarConfigured() && next.CalendarID != current.CalendarID {
		stranded, err := countEventsOnCalendar(r.Context(), a.pool, caller.TenantID, current.CalendarID)
		if err != nil {
			a.serviceErr(w, err)
			return
		}
		if stranded > 0 {
			warning = "Events already on the previous calendar will be moved on the next sync. If any are left behind, run Reconcile."
		}
	}

	saved, err := a.svc.SaveSettings(r.Context(), next)
	if err != nil {
		a.serviceErr(w, err)
		return
	}
	payload, err := a.buildSettingsPayload(r, saved.TenantID)
	if err != nil {
		a.serviceErr(w, err)
		return
	}
	eventsJSON(w, http.StatusOK, map[string]any{"settings": payload, "warning": warning})
}

// handleRotateFeedToken mints a new token, which immediately invalidates the
// old one -- the website build must be updated with it.
func (a *App) handleRotateFeedToken(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	settings, err := a.svc.Settings(r.Context(), caller.TenantID)
	if err != nil {
		a.serviceErr(w, err)
		return
	}
	token, err := NewFeedToken()
	if err != nil {
		a.serviceErr(w, err)
		return
	}
	settings.TenantID = caller.TenantID
	settings.FeedToken = token
	if _, err := a.svc.SaveSettings(r.Context(), settings); err != nil {
		a.serviceErr(w, err)
		return
	}
	payload, err := a.buildSettingsPayload(r, caller.TenantID)
	if err != nil {
		a.serviceErr(w, err)
		return
	}
	eventsJSON(w, http.StatusOK, map[string]any{
		"settings": payload,
		"warning":  "The previous token stopped working. Update the website build with the new one.",
	})
}

func (a *App) handleSyncNow(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	sum, err := a.SyncNow(r.Context(), caller.TenantID)
	if err != nil {
		a.serviceErr(w, err)
		return
	}
	eventsJSON(w, http.StatusOK, map[string]any{"message": "Calendar sync finished: " + sum.String() + "."})
}

// handleReconcile defaults to a dry run.
//
// This is the only operation that deletes calendar entries, so seeing the plan
// first is the default and applying it takes an explicit flag.
func (a *App) handleReconcile(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	apply := r.URL.Query().Get("apply") == "true"

	if !apply {
		plan, err := a.PreviewReconcile(r.Context(), caller.TenantID)
		if err != nil {
			a.serviceErr(w, err)
			return
		}
		removals := make([]string, 0, len(plan.Delete))
		for _, ev := range plan.Delete {
			removals = append(removals, ev.Summary)
		}
		restores := make([]string, 0, len(plan.Create))
		for i := range plan.Create {
			restores = append(restores, plan.Create[i].Title)
		}
		eventsJSON(w, http.StatusOK, map[string]any{
			"dry_run":  true,
			"empty":    plan.Empty(),
			"removals": removals,
			"restores": restores,
			"message":  FormatReconcilePlan(plan),
		})
		return
	}

	sum, err := a.RunReconcile(r.Context(), caller.TenantID)
	if err != nil {
		a.serviceErr(w, err)
		return
	}
	eventsJSON(w, http.StatusOK, map[string]any{
		"dry_run": false,
		"message": "Reconcile finished: " + sum.String() + ".",
	})
}

// baseURLFrom reconstructs the externally visible base URL for display. Behind
// a proxy the forwarded headers are what the browser actually used.
func baseURLFrom(r *http.Request) string {
	scheme := "https"
	if fwd := r.Header.Get("X-Forwarded-Proto"); fwd != "" {
		scheme = fwd
	} else if r.TLS == nil {
		scheme = "http"
	}
	host := r.Host
	if fwd := r.Header.Get("X-Forwarded-Host"); fwd != "" {
		host = fwd
	}
	return scheme + "://" + host
}
