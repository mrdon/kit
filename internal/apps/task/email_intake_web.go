package task

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/email"
	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/models"
)

// emailIntakeView is the JSON shape the console panel reads/writes. The
// default_instructions field is read-only preview of the baked triage prose the
// user is extending; only extra_instructions is stored.
type emailIntakeView struct {
	Enabled             bool       `json:"enabled"`
	Schedule            string     `json:"schedule"`
	ExtraInstructions   string     `json:"extra_instructions"`
	LastScannedAt       *time.Time `json:"last_scanned_at"`
	HasMailbox          bool       `json:"has_mailbox"`
	DefaultInstructions string     `json:"default_instructions"`
}

func (a *TaskApp) handleGetEmailIntake(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())

	view := emailIntakeView{
		Schedule:            "0 7 * * *",
		HasMailbox:          a.callerHasMailbox(r, caller.TenantID, caller.UserID),
		DefaultInstructions: defaultEmailIntakeInstructions(),
	}

	row, err := models.GetEmailIntake(r.Context(), a.svc.pool, caller.TenantID, caller.UserID)
	switch {
	case errors.Is(err, models.ErrEmailIntakeNotFound):
		// Never opted in — return the defaults already set on view.
	case err != nil:
		taskErr(w, http.StatusInternalServerError, "internal error")
		return
	default:
		view.Enabled = row.Enabled
		view.Schedule = row.Schedule
		view.ExtraInstructions = row.ExtraInstructions
		view.LastScannedAt = row.LastScannedAt
	}
	taskJSON(w, http.StatusOK, view)
}

type emailIntakePutBody struct {
	Enabled           bool   `json:"enabled"`
	Schedule          string `json:"schedule"`
	ExtraInstructions string `json:"extra_instructions"`
}

func (a *TaskApp) handlePutEmailIntake(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	var body emailIntakePutBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		taskErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	schedule := strings.TrimSpace(body.Schedule)
	if schedule == "" {
		schedule = "0 7 * * *"
	}
	// Validate the cron up front so a bad expression can't silently wedge the
	// sweep (emailIntakeDue treats a parse error as not-due).
	if _, err := models.NextCronRun(schedule, "UTC", time.Now()); err != nil {
		taskErr(w, http.StatusBadRequest, "invalid schedule — use cron syntax like '0 7 * * *'")
		return
	}

	row, err := models.UpsertEmailIntake(r.Context(), a.svc.pool, caller.TenantID, caller.UserID,
		body.Enabled, schedule, strings.TrimSpace(body.ExtraInstructions))
	if err != nil {
		taskErr(w, http.StatusInternalServerError, "internal error")
		return
	}

	taskJSON(w, http.StatusOK, emailIntakeView{
		Enabled:             row.Enabled,
		Schedule:            row.Schedule,
		ExtraInstructions:   row.ExtraInstructions,
		LastScannedAt:       row.LastScannedAt,
		HasMailbox:          a.callerHasMailbox(r, caller.TenantID, caller.UserID),
		DefaultInstructions: defaultEmailIntakeInstructions(),
	})
}

// callerHasMailbox reports whether the caller has an email integration
// configured — the panel uses it to nudge them to set one up first.
func (a *TaskApp) callerHasMailbox(r *http.Request, tenantID, userID uuid.UUID) bool {
	uid := userID
	integ, err := models.GetIntegration(r.Context(), a.svc.pool, tenantID, email.Provider, email.AuthType, &uid)
	return err == nil && integ != nil
}
