package events

import (
	"os"
	"testing"
)

// TestWriteTopperPreview renders the sample layout to KIT_TOPPER_OUT when set.
// Scaffolding for eyeballing the design; skipped in CI.
func TestWriteTopperPreview(t *testing.T) {
	out := os.Getenv("KIT_TOPPER_OUT")
	if out == "" {
		t.Skip("set KIT_TOPPER_OUT to render a preview")
	}
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	top := sampleTopper()
	for i := range top.Rows {
		src := os.Getenv("KIT_TOPPER_POSTER")
		if src == "" || i%2 == 1 {
			continue
		}
		raw, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		img, err := posterThumb(raw)
		if err != nil {
			t.Fatal(err)
		}
		top.Rows[i].Poster = img
	}
	if err := RenderTopperPDF(top, f); err != nil {
		t.Fatal(err)
	}
}
