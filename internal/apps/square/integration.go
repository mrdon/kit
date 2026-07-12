package square

import (
	"context"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/admin"
	"github.com/mrdon/kit/internal/models"
)

// squareIntegration is the admin.Integration impl for the Square
// connection. Registered from Configure so the admin integrations index
// renders a status card for it.
type squareIntegration struct {
	app *App
}

func (i *squareIntegration) Name() string { return "Square" }

func (i *squareIntegration) Description() string {
	return "Connect Square to sync the published staff schedule into your team calendar."
}

func (i *squareIntegration) Slug() string { return "square" }

func (i *squareIntegration) Status(ctx context.Context, tenantID uuid.UUID) (admin.Status, error) {
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
	if mid, ok := integ.Config["merchant_id"].(string); ok && mid != "" {
		detail = "Merchant " + mid
	}
	return admin.Status{Connected: true, Detail: detail}, nil
}

// ManageURL returns "" — Square has no standalone settings page in v1;
// configuration flows through the configure_integration hosted form.
func (i *squareIntegration) ManageURL(_ string) string { return "" }
