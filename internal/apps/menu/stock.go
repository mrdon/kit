package menu

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/posters"
)

// stockRef is the prefix find_stock_photos hands out. A stored image can name
// one instead of a URL, since Pixabay's own download links are not public.
const stockRef = "pixabay:"

// stockPhotos resolves a stock reference to a download URL, or is nil when
// the posters app is not running.
var stockPhotos = func() func(ctx context.Context, tenantID uuid.UUID, ref string) (string, error) {
	if app := posters.Instance(); app != nil {
		return app.StockPhotoURL
	}
	return nil
}

// imageSource is the URL to fetch an asset from: the one given, or the
// download URL behind a pixabay:<id> reference.
func imageSource(ctx context.Context, tenantID uuid.UUID, src string) (string, error) {
	if !strings.HasPrefix(src, stockRef) {
		return src, nil
	}
	resolve := stockPhotos()
	if resolve == nil {
		return "", errors.New("stock photos are not available here")
	}
	return resolve(ctx, tenantID, src)
}
