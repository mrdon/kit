package events

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/console"
	"github.com/mrdon/kit/internal/auth"
)

// The print route.
//
// A PageRoute rather than a JSON one, because this URL is typed, bookmarked
// and opened in a tab: an expired session should land on the login page, not
// dump a 401 into the browser's PDF viewer. It is behind a session for the
// ordinary reason -- an unauthenticated print URL is a public URL, and the
// week's programme is Kit's to publish deliberately rather than to leak.

// errWeekParam keeps the message short enough to be useful in a browser that
// is showing it as plain text.
var errWeekParam = errors.New("week must be a date (YYYY-MM-DD), \"this\" or \"next\"")

// registerTopperRoutes mounts the printable table topper.
func registerTopperRoutes(mux apps.Mux, a *App) {
	mux.Handle("GET /{slug}/events/topper.pdf",
		console.PageRoute(a.pool, a.signer, a.handleTopper))
}

// handleTopper renders the sheet for the requested week.
func (a *App) handleTopper(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenant := auth.TenantFromContext(ctx)
	if tenant == nil {
		http.NotFound(w, r)
		return
	}
	day, err := topperWeekParam(r.URL.Query().Get("week"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	topper, err := a.buildTopper(ctx, tenant, day)
	if err != nil {
		slog.Error("events topper: building", "tenant_id", tenant.ID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Rendered to memory first. Writing straight to the ResponseWriter would
	// commit a 200 and then, on a font or image failure halfway down, hand the
	// browser a truncated PDF -- which reads as "the printer is broken" rather
	// than as an error anyone can act on.
	var buf bytes.Buffer
	if err := RenderTopperPDF(topper, &buf); err != nil {
		slog.Error("events topper: rendering", "tenant_id", tenant.ID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition",
		`inline; filename="this-week-`+topper.WeekStart.Format("2006-01-02")+`.pdf"`)
	// The whole point is that it reflects the events as they stand right now,
	// so a cached copy from before this morning's edit is worse than useless.
	w.Header().Set("Cache-Control", "no-store, must-revalidate")
	if _, err := w.Write(buf.Bytes()); err != nil {
		slog.Warn("events topper: writing response", "tenant_id", tenant.ID, "error", err)
	}
}

// topperWeekParam reads ?week=. Empty means this week and "next" means the one
// after, because printing next week's card on a Friday is the common case and
// nobody should have to work out Sunday's date to do it.
func topperWeekParam(raw string) (time.Time, error) {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "":
		return timeNow(), nil
	case "this":
		return timeNow(), nil
	case "next":
		return timeNow().AddDate(0, 0, 7), nil
	}
	day, err := time.Parse("2006-01-02", strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}, errWeekParam
	}
	return day, nil
}
