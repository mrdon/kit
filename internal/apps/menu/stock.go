package menu

import (
	"context"
	"errors"
	"fmt"
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

// photoRef names a photo in the posters app's library, by its id from
// search_photos. Library photos live in Drive, so there is no URL to give.
const photoRef = "photo:"

// photoSize is the long edge a library photo is stored at: sharp across the
// rail's width on a 1080p wall without carrying a camera original.
const photoSize = 1400

// libraryPhotos renders a library photo, or is nil when the posters app is
// not running.
var libraryPhotos = func() func(ctx context.Context, tenantID, id uuid.UUID, size int) ([]byte, error) {
	if app := posters.Instance(); app != nil {
		return app.LibraryPhotoJPEG
	}
	return nil
}

// libraryPhoto loads the bytes behind a photo:<id> reference.
func libraryPhoto(ctx context.Context, tenantID uuid.UUID, ref string) ([]byte, error) {
	id, err := uuid.Parse(strings.TrimPrefix(ref, photoRef))
	if err != nil {
		return nil, fmt.Errorf("%q is not a photo id from search_photos", ref)
	}
	render := libraryPhotos()
	if render == nil {
		return nil, errors.New("the photo library is not available here")
	}
	return render(ctx, tenantID, id, photoSize)
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
