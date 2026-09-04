package menu

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/console"
	"github.com/mrdon/kit/internal/auth"
)

// The print route.
//
// A PageRoute rather than a JSON one, for the same reason the events topper is
// one: this URL gets bookmarked and opened in a tab, and an expired session
// should land on the login page rather than drop a 401 into the browser's PDF
// viewer.
//
// It sits behind a session even though the board it prints is public. The
// public board is a screen on a wall; this is a document that costs two scrapes
// of somebody else's site to build, and leaving it unauthenticated would make
// it a free way to have Kit hammer Untappd.

// registerPrintRoutes mounts the printable menu.
func registerPrintRoutes(mux apps.Mux, a *App) {
	mux.Handle("GET /{slug}/menu/print.pdf",
		console.PageRoute(a.pool, a.signer, a.handlePrint))
}

// handlePrint renders the paper menu.
func (a *App) handlePrint(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	if tenant == nil {
		http.NotFound(w, r)
		return
	}
	// The assembly outlives the request only in the sense that it must not be
	// cut short by a browser that gave up; a print that half-finishes is worse
	// than one that takes four seconds.
	ctx, cancel := context.WithTimeout(r.Context(), printTimeout)
	defer cancel()

	menu, err := a.buildPrintMenu(ctx, tenant)
	if err != nil {
		slog.Error("menu print: building", "tenant_id", tenant.ID, "error", err)
		http.Error(w, "no menu to print yet — set a tap list first",
			http.StatusUnprocessableEntity)
		return
	}

	// Rendered to memory first. Writing straight to the ResponseWriter would
	// commit a 200 and then, on a font or image failure halfway down, hand the
	// browser a truncated PDF -- which reads as "the printer is broken" rather
	// than as an error anyone can act on.
	var buf bytes.Buffer
	if err := RenderPrintPDF(menu, &buf); err != nil {
		slog.Error("menu print: rendering", "tenant_id", tenant.ID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition",
		`inline; filename="menu-`+time.Now().Format("2006-01-02")+`.pdf"`)
	// The whole point is that it reflects what is pouring right now, so a
	// cached copy from before this morning's tap change is worse than useless.
	w.Header().Set("Cache-Control", "no-store, must-revalidate")
	if _, err := w.Write(buf.Bytes()); err != nil {
		slog.Warn("menu print: writing response", "tenant_id", tenant.ID, "error", err)
	}
}
