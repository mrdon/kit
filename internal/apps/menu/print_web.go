package menu

import (
	"bytes"
	"context"
	"errors"
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
// public board is a screen on a wall; this is an internal document, and the
// prices and wording on it are a staff-facing thing until somebody puts it on
// a table.

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
	// Nothing is fetched here any more, so this bounds a few reads and a PDF
	// compose. It exists so a stuck query cannot hold a browser open forever.
	ctx, cancel := context.WithTimeout(r.Context(), printTimeout)
	defer cancel()

	menu, err := a.buildPrintMenu(ctx, tenant)
	switch {
	case errors.Is(err, ErrNotSynced):
		// Not an error worth a stack trace: it is the state every workspace
		// starts in, and the message is the whole fix.
		slog.Info("menu print: not synced yet", "tenant_id", tenant.ID)
		http.Error(w, ErrNotSynced.Error(), http.StatusUnprocessableEntity)
		return
	case err != nil:
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
	// It reflects the last sync, and a sync can happen between two prints, so
	// a cached copy is a menu that silently disagrees with the one beside it.
	w.Header().Set("Cache-Control", "no-store, must-revalidate")
	if _, err := w.Write(buf.Bytes()); err != nil {
		slog.Warn("menu print: writing response", "tenant_id", tenant.ID, "error", err)
	}
}
