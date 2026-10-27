package events

import (
	"bytes"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/console"
	"github.com/mrdon/kit/internal/auth"
)

// The entrance sign's routes. Behind the topper's capability: it is the same
// job, printing the programme for customers, and the bar iPad that prints
// the table card on a Friday is the one that prints the door sign each day.

func registerSignRoutes(mux apps.Mux, a *App) {
	mux.Handle("GET /{slug}/events/sign.pdf",
		console.RequireCapPage(a.pool, a.signer, auth.CapEventsTopper, a.handleSign))
	mux.Handle("GET /{slug}/api/events/sign",
		console.RequireCap(a.pool, a.signer, auth.CapEventsTopper, a.handleSignChoices))
}

// signChoice is one of the day's events, for the page that offers its poster
// as the sign instead.
type signChoice struct {
	ID        uuid.UUID `json:"id"`
	Title     string    `json:"title"`
	Time      string    `json:"time,omitempty"`
	HasPoster bool      `json:"has_poster"`
}

// handleSignChoices lists the day's events, so the print page can offer the
// generated sign or any one event's poster.
func (a *App) handleSignChoices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenant := auth.TenantFromContext(ctx)
	if tenant == nil {
		http.NotFound(w, r)
		return
	}
	settings, err := getSettings(ctx, a.pool, tenant.ID)
	if err != nil {
		a.serviceErr(w, err)
		return
	}
	day, ok := signDay(r.URL.Query().Get("day"), timeNow().In(settings.Loc()))
	if !ok {
		eventsErr(w, http.StatusBadRequest, `day must be "today" or "tomorrow"`)
		return
	}
	events, err := a.listSignEvents(ctx, tenant.ID, day)
	if err != nil {
		a.serviceErr(w, err)
		return
	}
	s := composeSign(events, day, tenant.Name)
	choices := make([]signChoice, 0, len(s.Events))
	for _, e := range s.Events {
		choices = append(choices, signChoice{ID: e.EventID, Title: e.Title, Time: e.Time, HasPoster: e.posterID != nil})
	}
	eventsJSON(w, http.StatusOK, map[string]any{"date": s.Date, "events": choices})
}

// handleSign renders the sign, or with ?poster=<event id> that event's poster
// fitted to the page.
func (a *App) handleSign(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenant := auth.TenantFromContext(ctx)
	if tenant == nil {
		http.NotFound(w, r)
		return
	}
	settings, err := getSettings(ctx, a.pool, tenant.ID)
	if err != nil {
		slog.Error("events sign: settings", "tenant_id", tenant.ID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	day, ok := signDay(r.URL.Query().Get("day"), timeNow().In(settings.Loc()))
	if !ok {
		http.Error(w, `day must be "today" or "tomorrow"`, http.StatusBadRequest)
		return
	}
	// Rendered to memory first, as the topper is: a half-written PDF reads as
	// a broken printer, not as an error.
	var buf bytes.Buffer
	if raw := r.URL.Query().Get("poster"); raw != "" {
		if status, msg := a.renderSignPoster(r, tenant.ID, raw, &buf); status != http.StatusOK {
			http.Error(w, msg, status)
			return
		}
	} else {
		s, err := a.buildSign(ctx, tenant, day, siteHost(settings.PublicURLTemplate))
		if err == nil {
			err = RenderSignPDF(s, &buf)
		}
		if err != nil {
			slog.Error("events sign: rendering", "tenant_id", tenant.ID, "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `inline; filename="sign-`+day.Format("2006-01-02")+`.pdf"`)
	w.Header().Set("Cache-Control", "no-store, must-revalidate")
	if _, err := w.Write(buf.Bytes()); err != nil {
		slog.Warn("events sign: writing response", "tenant_id", tenant.ID, "error", err)
	}
}

// renderSignPoster prints one event's own poster, and says why not when it
// cannot: the event is not public, or has no poster.
func (a *App) renderSignPoster(r *http.Request, tenantID uuid.UUID, raw string, buf *bytes.Buffer) (int, string) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return http.StatusBadRequest, "poster must be an event id"
	}
	e, err := getEvent(r.Context(), a.pool, tenantID, id)
	if err != nil || e == nil || !e.IsPubliclyVisible() {
		return http.StatusNotFound, "no such public event"
	}
	store := a.attachments()
	if e.HeroAttachmentID == nil || store == nil {
		return http.StatusNotFound, "that event has no poster"
	}
	img := loadSignImage(r.Context(), store, tenantID, *e.HeroAttachmentID, signPosterPx)
	if img == nil {
		return http.StatusNotFound, "that event's poster could not be read"
	}
	if err := RenderSignPosterPDF(img, buf); err != nil {
		slog.Error("events sign: rendering poster", "tenant_id", tenantID, "error", err)
		return http.StatusInternalServerError, "internal error"
	}
	return http.StatusOK, ""
}
