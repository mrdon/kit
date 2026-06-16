package netlify

import (
	"context"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/admin"
)

// netlifyIntegration is the admin.Integration impl for the Netlify
// website-management app. Registered with admin.RegisterIntegration
// from Configure so the integrations index renders it.
type netlifyIntegration struct {
	app *App
}

func (i *netlifyIntegration) Name() string { return "Netlify" }

func (i *netlifyIntegration) Description() string {
	return "Sign in with Netlify so Kit can edit the site that backs your marketing pages."
}

func (i *netlifyIntegration) Slug() string { return "netlify" }

func (i *netlifyIntegration) Status(ctx context.Context, tenantID uuid.UUID) (admin.Status, error) {
	if i.app == nil || i.app.svc == nil {
		return admin.Status{}, nil
	}
	cfg, err := GetConfig(ctx, i.app.pool, tenantID)
	if err != nil {
		return admin.Status{}, err
	}
	if cfg == nil {
		return admin.Status{Connected: false}, nil
	}
	if cfg.ConnectedNetlify() {
		detail := "Site: " + cfg.NetlifySiteName
		return admin.Status{Connected: true, Detail: detail}, nil
	}
	if cfg.NetlifyAccessTokenCipher != "" {
		return admin.Status{Connected: false, Detail: "Signed in — needs site pick."}, nil
	}
	return admin.Status{Connected: false}, nil
}

func (i *netlifyIntegration) ManageURL(tenantSlug string) string {
	return "/" + tenantSlug + "/web/netlify"
}
