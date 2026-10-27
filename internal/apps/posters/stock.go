package posters

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/integrations"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
)

// Stock photos are a per-workspace choice twice over: an admin turns the
// switch on in Posters settings, and connects Pixabay on the Integrations
// page, where every other external key lives. The key is stored by the
// integrations app, encrypted, and read back here. A server-wide
// PIXABAY_API_KEY, when the operator set one, stands in for workspaces
// that have not connected their own.

const (
	stockProvider = "pixabay"
	stockAuthType = "api_key"
)

// stockTypeSpec is the Integrations-page entry for Pixabay.
func stockTypeSpec() integrations.TypeSpec {
	return integrations.TypeSpec{
		Provider:    stockProvider,
		AuthType:    stockAuthType,
		DisplayName: "Pixabay (stock photos)",
		Description: "Lets Posters use a stock photo when your own library has nothing that fits. Free key from a Pixabay account; the Pixabay Content License allows commercial use without attribution. Turn stock photos on in Posters settings once connected.",
		Scope:       integrations.ScopeTenant,
		Fields: []integrations.FieldSpec{
			{Name: "api_key", Label: "API key", InputType: "password", Target: integrations.TargetPrimaryToken, Required: true,
				Help: "From pixabay.com/api/docs while signed in to your Pixabay account."},
		},
	}
}

// pixabayKey is the key to use for a tenant: its connected one, else the
// server's. "" means stock is unavailable here.
func (a *App) pixabayKey(ctx context.Context, tenantID uuid.UUID) (string, error) {
	integ, err := models.GetIntegration(ctx, a.pool, tenantID, stockProvider, stockAuthType, nil)
	if err != nil {
		return "", fmt.Errorf("looking up the Pixabay connection: %w", err)
	}
	if integ == nil {
		return a.serverPixabayKey, nil
	}
	primaryEnc, _, err := models.GetIntegrationTokens(ctx, a.pool, tenantID, integ.ID)
	if err != nil {
		return "", fmt.Errorf("reading the Pixabay key: %w", err)
	}
	if primaryEnc == "" {
		return a.serverPixabayKey, nil
	}
	if a.enc == nil {
		return "", errors.New("decrypting the Pixabay key: no encryptor configured")
	}
	key, err := a.enc.Decrypt(primaryEnc)
	if err != nil {
		return "", fmt.Errorf("decrypting the Pixabay key: %w", err)
	}
	return key, nil
}

// stockConfigured says whether a key exists for this tenant from either source.
func (a *App) stockConfigured(ctx context.Context, tenantID uuid.UUID) bool {
	key, err := a.pixabayKey(ctx, tenantID)
	return err == nil && key != ""
}

// stockAllowed reads the tenant switch that decides whether the agent gets
// the Pixabay tool at all. The switch means nothing without a key.
func (a *App) stockAllowed(ctx context.Context, caller *services.Caller) bool {
	if caller == nil || a.pool == nil {
		return false
	}
	s, err := getSettings(ctx, a.pool, caller.TenantID)
	return err == nil && s.AllowStockPhotos && a.stockConfigured(ctx, caller.TenantID)
}

// StockPhotoURL resolves a pixabay:<id> reference to a URL its full-size
// image can be downloaded from, for an app that keeps its own copy, such as
// the menu board's stored images. Pixabay's download URLs expire, so this is
// for fetching once, never for storing.
func (a *App) StockPhotoURL(ctx context.Context, tenantID uuid.UUID, ref string) (string, error) {
	key, err := a.pixabayKey(ctx, tenantID)
	if err != nil {
		return "", err
	}
	if key == "" || a.pixabay == nil {
		return "", errors.New("pixabay is not connected for this workspace")
	}
	s, err := a.pixabay.Lookup(ctx, key, ref)
	if err != nil {
		return "", err
	}
	return s.Large, nil
}

// LibraryPhotoJPEG renders one library photo, with its darktable look, as a
// JPEG no larger than size on the long edge, for an app that keeps its own
// copy, such as the menu board's stored images.
func (a *App) LibraryPhotoJPEG(ctx context.Context, tenantID, id uuid.UUID, size int) ([]byte, error) {
	p, err := getPhoto(ctx, a.pool, tenantID, id)
	if err != nil {
		return nil, err
	}
	if a.renderer == nil {
		return nil, errors.New("the poster renderer is not configured")
	}
	return a.renderer.Photo(ctx, p.LookRef(size), size)
}
