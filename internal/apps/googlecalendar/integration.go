package googlecalendar

import (
	"context"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/admin"
	"github.com/mrdon/kit/internal/models"
)

// googleCalendarIntegration is the admin.Integration impl. Registered from
// Configure so the admin integrations index renders a status card.
type googleCalendarIntegration struct {
	app *App
}

func (i *googleCalendarIntegration) Name() string { return "Google Calendar" }

func (i *googleCalendarIntegration) Description() string {
	return "Write the synced staff schedule into an existing team calendar via a service account."
}

func (i *googleCalendarIntegration) Slug() string { return "google-calendar" }

func (i *googleCalendarIntegration) Status(ctx context.Context, tenantID uuid.UUID) (admin.Status, error) {
	if i.app == nil || i.app.pool == nil {
		return admin.Status{}, nil
	}
	integ, err := models.GetIntegration(ctx, i.app.pool, tenantID, Provider, AuthType, nil)
	if err != nil {
		return admin.Status{}, err
	}
	if integ == nil {
		return admin.Status{Connected: false}, nil
	}
	detail := "Connected"
	if cal, ok := integ.Config["calendar_id"].(string); ok && cal != "" {
		detail = "Calendar " + cal
	}
	return admin.Status{Connected: true, Detail: detail}, nil
}

// ManageURL returns "" — configuration flows through the hosted
// configure_integration form; no standalone page in v1.
func (i *googleCalendarIntegration) ManageURL(_ string) string { return "" }
