package menu

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestImageSourceResolvesStockRefs(t *testing.T) {
	orig := stockPhotos
	t.Cleanup(func() { stockPhotos = orig })
	stockPhotos = func() func(context.Context, uuid.UUID, string) (string, error) {
		return func(_ context.Context, _ uuid.UUID, ref string) (string, error) {
			return "https://pixabay.example/get/" + ref[len(stockRef):], nil
		}
	}
	ctx, tenant := context.Background(), uuid.New()

	if got, _ := imageSource(ctx, tenant, "https://example.com/a.png"); got != "https://example.com/a.png" {
		t.Errorf("plain url = %q", got)
	}
	if got, _ := imageSource(ctx, tenant, "pixabay:4194717"); got != "https://pixabay.example/get/4194717" {
		t.Errorf("stock ref = %q", got)
	}

	stockPhotos = func() func(context.Context, uuid.UUID, string) (string, error) { return nil }
	if _, err := imageSource(ctx, tenant, "pixabay:1"); err == nil {
		t.Error("stock ref without the posters app should fail")
	}
}
