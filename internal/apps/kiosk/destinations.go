package kiosk

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/events"
	"github.com/mrdon/kit/internal/apps/menu"
	"github.com/mrdon/kit/internal/apps/trivia"
)

// Destination is a one-tap button for what a screen shows, so repointing a
// board is "press Trivia", not "type a URL". Kit's own screen pages come
// first; the workspace's own buttons (Custom, deletable) follow. A typed
// address is still accepted; it is the rare case, not the interface.
type Destination struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	URL    string `json:"url"`
	Custom bool   `json:"custom"`
}

// destinationSpecs is ordered as the buttons appear. Each is gated on its
// app, so a workspace that has switched trivia off isn't offered a dead page.
var destinationSpecs = []struct {
	app, label, path string
}{
	{menu.AppName, "Menu", "/menu"},
	{events.AppName, "Events", "/events/screen"},
	{trivia.AppName, "Trivia", "/trivia/tv"},
}

// Destinations returns the screen pages enabled for a tenant, as absolute
// URLs built the same way the board's own public_url is, followed by the
// tenant's own buttons keyed by id.
func (a *App) Destinations(ctx context.Context, tenantID uuid.UUID, slug string) ([]Destination, error) {
	custom, err := ListButtons(ctx, a.pool, tenantID)
	if err != nil {
		return nil, fmt.Errorf("listing kiosk destinations: %w", err)
	}
	out := make([]Destination, 0, len(destinationSpecs)+len(custom))
	for _, d := range destinationSpecs {
		if !apps.IsEnabled(ctx, tenantID, d.app) {
			continue
		}
		out = append(out, Destination{Key: d.app, Label: d.label, URL: a.baseURL + "/" + slug + d.path})
	}
	for _, b := range custom {
		out = append(out, Destination{Key: b.ID.String(), Label: b.Label, URL: b.URL, Custom: true})
	}
	return out, nil
}
