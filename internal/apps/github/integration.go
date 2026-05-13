package github

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/admin"
)

// githubIntegration is the admin.Integration impl for the Kit GitHub
// App install substrate. Registered with admin.RegisterIntegration
// from Configure so the integrations index renders it.
//
// v1 has no dedicated GitHub settings page — Manage links into the
// netlify settings page (the only consumer today). When a second app
// uses the install, the entry point gets promoted to a workspace-
// level surface owned by this package.
type githubIntegration struct {
	app *App
}

func (i *githubIntegration) Name() string { return "GitHub" }

func (i *githubIntegration) Description() string {
	return "Install the Kit GitHub App on the repository that backs your Netlify site (and any future Kit features that need GitHub)."
}

func (i *githubIntegration) Slug() string { return "github" }

func (i *githubIntegration) Status(ctx context.Context, tenantID uuid.UUID) (admin.Status, error) {
	if i.app == nil || i.app.pool == nil {
		return admin.Status{}, nil
	}
	inst, err := GetInstallation(ctx, i.app.pool, tenantID)
	if err != nil {
		return admin.Status{}, err
	}
	if inst == nil {
		return admin.Status{Connected: false}, nil
	}
	detail := fmt.Sprintf("Installation #%d", inst.InstallationID)
	if inst.AccountLogin != "" {
		detail = "Installed on " + inst.AccountLogin
	}
	return admin.Status{Connected: true, Detail: detail}, nil
}

func (i *githubIntegration) ManageURL(tenantSlug string) string {
	// No standalone GitHub settings page in v1. Bounce admins to the
	// netlify settings page since that's the only surface that
	// currently hosts the install button + disconnect.
	return "/" + tenantSlug + "/apps/netlify/settings"
}
