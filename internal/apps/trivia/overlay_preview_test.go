package trivia

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDumpOverlayPreview writes the overlay page to $TRIVIA_OVERLAY_DUMP so
// docs/trivia-obs-overlay/preview.mjs can screenshot it against fake states.
// Skipped unless the variable is set.
func TestDumpOverlayPreview(t *testing.T) {
	dir := os.Getenv("TRIVIA_OVERLAY_DUMP")
	if dir == "" {
		t.Skip("TRIVIA_OVERLAY_DUMP not set")
	}
	game := &Game{Name: "tuesday", Title: "Tuesday Quiz", JoinCode: "AB12"}
	html, err := RenderOverlay("https://kit.gravity.beer", "gravity", game, false, "")
	if err != nil {
		t.Fatalf("rendering overlay: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "overlay.html"), []byte(html), 0o600); err != nil {
		t.Fatalf("writing overlay: %v", err)
	}
}
