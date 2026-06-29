package widget

import (
	"context"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/models"
)

// Usage reports the number of active (non-revoked) widget embed tokens.
func (a *App) Usage(ctx context.Context, tenantID uuid.UUID) (string, error) {
	if a.pool == nil {
		return "", nil
	}
	tokens, err := models.ListActiveWidgetTokens(ctx, a.pool, tenantID)
	if err != nil {
		return "", err
	}
	return apps.CountLabel(len(tokens), "active token", "active tokens"), nil
}
