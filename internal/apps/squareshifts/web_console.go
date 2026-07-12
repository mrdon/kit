package squareshifts

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/googlecalendar"
	"github.com/mrdon/kit/internal/apps/integrations"
	"github.com/mrdon/kit/internal/apps/square"
	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
)

// runJSON is one row in the Manage page's history table.
type runJSON struct {
	Status      string `json:"status"` // "completed" | "failed"
	TriggeredBy string `json:"triggered_by"`
	Created     int    `json:"created"`
	Updated     int    `json:"updated"`
	Deleted     int    `json:"deleted"`
	DurationMS  int64  `json:"duration_ms"`
	Error       string `json:"error"`
	At          string `json:"at"` // RFC 3339
}

// statusJSON is the Manage page view-model.
type statusJSON struct {
	SquareConnected bool      `json:"square_connected"`
	GoogleConnected bool      `json:"google_connected"`
	Enabled         bool      `json:"enabled"`
	Recent          []runJSON `json:"recent"`
}

func (a *App) buildStatus(ctx context.Context, tenantID uuid.UUID) (statusJSON, error) {
	sq, err := models.GetIntegration(ctx, a.pool, tenantID, square.Provider, square.AuthType, nil)
	if err != nil {
		return statusJSON{}, err
	}
	gc, err := models.GetIntegration(ctx, a.pool, tenantID, googlecalendar.Provider, googlecalendar.AuthType, nil)
	if err != nil {
		return statusJSON{}, err
	}
	runs, err := listRecentRuns(ctx, a, tenantID, 10)
	if err != nil {
		return statusJSON{}, err
	}
	st := statusJSON{
		SquareConnected: sq != nil,
		GoogleConnected: gc != nil,
		Enabled:         apps.IsEnabled(ctx, tenantID, AppName),
		Recent:          make([]runJSON, 0, len(runs)),
	}
	for _, r := range runs {
		status := "completed"
		if r.Action == actionSyncFailed {
			status = "failed"
		}
		st.Recent = append(st.Recent, runJSON{
			Status:      status,
			TriggeredBy: r.Meta.TriggeredBy,
			Created:     r.Meta.Created,
			Updated:     r.Meta.Updated,
			Deleted:     r.Meta.Deleted,
			DurationMS:  r.Meta.DurationMS,
			Error:       r.Meta.Error,
			At:          r.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		})
	}
	return st, nil
}

func (a *App) handleStatusJSON(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	if caller == nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	st, err := a.buildStatus(r.Context(), caller.TenantID)
	if err != nil {
		slog.Error("squareshifts: building status", "error", err)
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (a *App) handleSyncJSON(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	if caller == nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if _, err := a.RunSync(r.Context(), caller.TenantID, "manual"); err != nil {
		// Setup errors are the caller's to fix — surface them as 400 with the
		// friendly hint rather than a 500.
		writeErr(w, http.StatusBadRequest, syncErrorMessage(err))
		return
	}
	// Return the refreshed status so the client re-renders in one round trip.
	st, err := a.buildStatus(r.Context(), caller.TenantID)
	if err != nil {
		slog.Error("squareshifts: building status after sync", "error", err)
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// handleConnectJSON mints a single-use integration setup URL for the Square
// or Google Calendar connector and returns it, so the Manage page's Connect
// buttons can send the admin straight to the secret-entry form.
func (a *App) handleConnectJSON(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	if caller == nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var body struct {
		Target string `json:"target"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	var provider, authType string
	switch body.Target {
	case "square":
		provider, authType = square.Provider, square.AuthType
	case "google":
		provider, authType = googlecalendar.Provider, googlecalendar.AuthType
	default:
		writeErr(w, http.StatusBadRequest, "unknown target")
		return
	}
	url, err := integrations.MintSetupURL(r.Context(), caller, provider, authType)
	if err != nil {
		if errors.Is(err, services.ErrForbidden) {
			writeErr(w, http.StatusForbidden, "admin only")
			return
		}
		slog.Error("squareshifts: minting setup url", "target", body.Target, "error", err)
		writeErr(w, http.StatusInternalServerError, "could not start setup")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}
