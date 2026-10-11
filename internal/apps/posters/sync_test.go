package posters

import "testing"

// The measure task downloads unmeasured photos through the renderer a
// batch at a time and leaves the rest for the next run.
func TestMeasurePhotosInBatches(t *testing.T) {
	f := newFixture(t)
	files := []DriveFile{
		{ID: "x", Name: "x.jpg", Modified: "m"},
		{ID: "y", Name: "y.jpg", Modified: "m"},
		{ID: "z", Name: "z.jpg", Modified: "m"},
	}
	if _, err := recordListedPhotos(f.ctx, f.pool, f.tenant.ID, files); err != nil {
		t.Fatal(err)
	}
	if err := f.app.measurePhotos(f.ctx, f.tenant.ID, 2); err != nil {
		t.Fatal(err)
	}
	if len(f.fake.InspectCalls) != 2 {
		t.Fatalf("inspected %d, want 2", len(f.fake.InspectCalls))
	}
	left, _ := countUnmeasured(f.ctx, f.pool, f.tenant.ID)
	if left != 1 {
		t.Fatalf("left unmeasured = %d, want 1", left)
	}
	if err := f.app.measurePhotos(f.ctx, f.tenant.ID, 2); err != nil {
		t.Fatal(err)
	}
	if left, _ = countUnmeasured(f.ctx, f.pool, f.tenant.ID); left != 0 {
		t.Fatalf("left unmeasured after second run = %d", left)
	}
	x, _ := getPhotoByFileID(f.ctx, f.pool, f.tenant.ID, "x")
	if x.Width != 1600 || x.Orientation != "landscape" {
		t.Fatalf("facts not recorded: %+v", x)
	}
}
