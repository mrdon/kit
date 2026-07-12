package console

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/integrations"
	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
)

// handleIntegrationCatalog lists every registered integration type with the
// caller's connection state. Available to any authenticated caller (not
// admin-only) so user-scoped connectors like email can be self-served;
// tenant-scoped ones are marked can_manage=false for non-admins.
func (a *App) handleIntegrationCatalog(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	if caller == nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	catalog, err := integrations.CatalogFor(r.Context(), caller)
	if err != nil {
		slog.Error("console: loading integration catalog", "error", err)
		writeErr(w, http.StatusInternalServerError, "could not load integrations")
		return
	}
	writeJSON(w, http.StatusOK, catalog)
}

// handleIntegrationConnect mints a single-use setup URL for a chosen type and
// returns it. MintSetupURL enforces the scope rule (tenant-scoped → admin).
func (a *App) handleIntegrationConnect(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	if caller == nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var body struct {
		Provider string `json:"provider"`
		AuthType string `json:"auth_type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	url, err := integrations.MintSetupURL(r.Context(), caller, body.Provider, body.AuthType)
	if err != nil {
		if errors.Is(err, services.ErrForbidden) {
			writeErr(w, http.StatusForbidden, "Only admins can configure this integration.")
			return
		}
		slog.Error("console: minting setup url", "provider", body.Provider, "error", err)
		writeErr(w, http.StatusInternalServerError, "could not start setup")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

// handleIntegrationDelete removes an integration the caller owns (admins any
// in-tenant row).
func (a *App) handleIntegrationDelete(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	if caller == nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := integrations.DeleteIntegrationForCaller(r.Context(), caller, id); err != nil {
		if errors.Is(err, models.ErrIntegrationForbidden) {
			writeErr(w, http.StatusForbidden, "You can't remove that integration.")
			return
		}
		slog.Error("console: deleting integration", "id", id, "error", err)
		writeErr(w, http.StatusInternalServerError, "could not remove integration")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}
