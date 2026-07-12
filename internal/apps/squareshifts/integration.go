package squareshifts

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/admin"
	"github.com/mrdon/kit/internal/apps/googlecalendar"
	"github.com/mrdon/kit/internal/apps/square"
	"github.com/mrdon/kit/internal/models"
)

// squareShiftsIntegration surfaces the sync's readiness + last-run status on
// the admin Integrations index. It's registered from Configure. This is the
// admin UI for status today; a Manage page with a "Sync now" button is a
// follow-up in the React console (ManageURL stays "" until then).
type squareShiftsIntegration struct {
	app *App
}

func (i *squareShiftsIntegration) Name() string { return "Square Shift Sync" }

func (i *squareShiftsIntegration) Description() string {
	return "Mirror your published Square schedule into a connected Google Calendar."
}

func (i *squareShiftsIntegration) Slug() string { return "square-shift-sync" }

// Status reports readiness (both dependencies connected) plus the last sync
// summary. Connected is true only when Square and Google Calendar are both
// configured — the sync can't run otherwise.
func (i *squareShiftsIntegration) Status(ctx context.Context, tenantID uuid.UUID) (admin.Status, error) {
	if i.app == nil || i.app.pool == nil {
		return admin.Status{}, nil
	}
	sq, err := models.GetIntegration(ctx, i.app.pool, tenantID, square.Provider, square.AuthType, nil)
	if err != nil {
		return admin.Status{}, err
	}
	gc, err := models.GetIntegration(ctx, i.app.pool, tenantID, googlecalendar.Provider, googlecalendar.AuthType, nil)
	if err != nil {
		return admin.Status{}, err
	}
	switch {
	case sq == nil && gc == nil:
		return admin.Status{Connected: false, Detail: "Connect Square and Google Calendar to enable."}, nil
	case sq == nil:
		return admin.Status{Connected: false, Detail: "Square not connected."}, nil
	case gc == nil:
		return admin.Status{Connected: false, Detail: "Google Calendar not connected."}, nil
	}
	return admin.Status{Connected: true, Detail: i.lastRunDetail(ctx, tenantID)}, nil
}

// lastRunDetail formats the most recent sync for the status pill.
func (i *squareShiftsIntegration) lastRunDetail(ctx context.Context, tenantID uuid.UUID) string {
	lr, ok, err := getLastRun(ctx, i.app, tenantID)
	if err != nil || !ok {
		return "Connected — no sync yet."
	}
	ago := time.Since(lr.CreatedAt).Round(time.Minute)
	if lr.Action == actionSyncFailed {
		return fmt.Sprintf("Last sync failed %s ago: %s", ago, lr.Meta.Error)
	}
	changed := lr.Meta.Created + lr.Meta.Updated + lr.Meta.Deleted
	return fmt.Sprintf("Last sync %s ago — %d event(s) changed.", ago, changed)
}

func (i *squareShiftsIntegration) ManageURL(tenantSlug string) string {
	return "/" + tenantSlug + "/web/admin/square-shifts"
}
