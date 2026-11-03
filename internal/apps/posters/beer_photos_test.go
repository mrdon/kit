package posters

import (
	"testing"
)

// A beer's photos are found by the tag the indexer wrote, portraits first,
// and nothing tagged otherwise comes back.
func TestBeerPhotosMatchTheTag(t *testing.T) {
	f := newFixture(t)
	land := f.indexedPhoto(t, "lineup.jpg", "beer", "Three beers on the bar")
	port := f.indexedPhoto(t, "pint.jpg", "beer", "A pint of Wicked Nebula")
	other := f.indexedPhoto(t, "mild.jpg", "beer", "A pint of Little Dipper")
	for _, p := range []struct {
		id     *Photo
		orient string
		tags   []string
	}{
		{land, "landscape", []string{"beer", "wicked-nebula", "light-lift-lager"}},
		{port, "portrait", []string{"beer", "wicked-nebula"}},
		{other, "portrait", []string{"beer", "little-dipper"}},
	} {
		if _, err := f.pool.Exec(f.ctx, `UPDATE app_poster_photos SET tags = $3, orientation = $4
			WHERE tenant_id = $1 AND id = $2`, f.tenant.ID, p.id.ID, p.tags, p.orient); err != nil {
			t.Fatal(err)
		}
	}

	got, err := f.app.BeerPhotos(f.ctx, f.tenant.ID, "wicked-nebula", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != port.ID || got[1].ID != land.ID {
		t.Fatalf("got %+v, want the portrait then the lineup", got)
	}
	if none, _ := f.app.BeerPhotos(f.ctx, f.tenant.ID, "", 0); len(none) != 0 {
		t.Error("an empty tag must match nothing")
	}
}
