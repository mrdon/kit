package console

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/auth"
)

// appRow is the JSON shape the console Apps settings page renders. Core apps
// (console, admin) are always enabled and not toggleable — the UI greys them.
type appRow struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
	Core        bool   `json:"core"`
}

func (a *App) handleAppsList(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	if caller == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	disabled, err := apps.DisabledApps(r.Context(), caller.TenantID)
	if err != nil {
		http.Error(w, "could not load app settings", http.StatusInternalServerError)
		return
	}
	catalog := apps.Catalog()
	rows := make([]appRow, 0, len(catalog))
	for _, info := range catalog {
		rows = append(rows, appRow{
			Name:        info.Name,
			DisplayName: info.DisplayName,
			Description: info.Description,
			Enabled:     info.Core || !disabled[info.Name],
			Core:        info.Core,
		})
	}
	writeJSON(w, http.StatusOK, rows)
}

type setAppRequest struct {
	Enabled bool `json:"enabled"`
}

func (a *App) handleAppSet(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	if caller == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	name := r.PathValue("name")
	var req setAppRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if err := apps.SetEnabled(r.Context(), caller.TenantID, name, req.Enabled); err != nil {
		if errors.Is(err, apps.ErrCoreApp) {
			http.Error(w, "this app cannot be disabled", http.StatusBadRequest)
			return
		}
		http.Error(w, "could not update app", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": req.Enabled})
}
