package menu

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPreview renders a board to a file so the design can be looked at in a
// browser without a deploy, a database, or a workspace.
//
// It is a test rather than a cmd/ program on purpose: it needs the package's
// unexported render path, it must never ship in the binary, and `go test -run`
// is already the fastest way to run one function in a package. It no-ops
// unless MENU_PREVIEW_OUT is set, so it costs nothing in a normal test run.
//
//	MENU_PREVIEW_UNTAPPD=testdata/untappd_board.html \
//	MENU_PREVIEW_BOARD=/tmp/board.json \
//	MENU_PREVIEW_ASSETS=anniversary=poster.png,party=party.jpg \
//	MENU_PREVIEW_OUT=/tmp/board.html \
//	go test ./internal/apps/menu/ -run TestPreview
//
// MENU_PREVIEW_BOARD defaults to the testdata fixture and supplies the venue
// chrome and panels. MENU_PREVIEW_UNTAPPD replaces its taps with ones parsed
// from a captured Untappd board, which is what the layout has to survive now
// that the list is pulled rather than authored. MENU_PREVIEW_ASSETS is a
// comma-separated key=path list standing in for the images Kit would have
// stored, so the preview shows the real graphics rather than nothing.
func TestPreview(t *testing.T) {
	out := os.Getenv("MENU_PREVIEW_OUT")
	if out == "" {
		t.Skip("set MENU_PREVIEW_OUT to render a preview")
	}

	src := os.Getenv("MENU_PREVIEW_BOARD")
	if src == "" {
		src = filepath.Join("testdata", "board.json")
	}
	raw, err := os.ReadFile(src) //nolint:gosec // developer-supplied path, test only
	if err != nil {
		t.Fatalf("reading %s: %v", src, err)
	}

	board, err := ParseBoard(raw)
	if err != nil {
		t.Fatalf("parsing %s: %v", src, err)
	}

	if fixture := os.Getenv("MENU_PREVIEW_UNTAPPD"); fixture != "" {
		page, err := os.ReadFile(fixture) //nolint:gosec // developer-supplied path, test only
		if err != nil {
			t.Fatalf("reading %s: %v", fixture, err)
		}
		taps := ParseUntappdBoard(string(page))
		if len(taps) == 0 {
			t.Fatalf("parsed no taps from %s", fixture)
		}
		board.Taps = taps
	}

	assets := map[string]string{}
	for pair := range strings.SplitSeq(os.Getenv("MENU_PREVIEW_ASSETS"), ",") {
		key, path, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if !ok || key == "" {
			continue
		}
		img, err := os.ReadFile(path) //nolint:gosec // developer-supplied path, test only
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		mime := "image/png"
		if ext := strings.ToLower(filepath.Ext(path)); ext == ".jpg" || ext == ".jpeg" {
			mime = "image/jpeg"
		}
		assets[key] = "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(img)
	}
	// Naming a key the preview was not given is a typo worth catching here
	// rather than as a blank panel on a wall.
	for _, p := range board.Panels {
		if key, ok := strings.CutPrefix(p.Image, AssetRef); ok {
			if _, have := assets[key]; !have {
				t.Logf("warning: panel names %q but no asset was supplied for it", key)
			}
		}
	}

	html, err := Render(board, assets, "preview")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if err := os.WriteFile(out, []byte(html), 0o600); err != nil {
		t.Fatalf("writing %s: %v", out, err)
	}
	t.Logf("wrote %s — %d taps, %d panels, %d KB",
		out, len(board.Taps), len(board.Panels), len(html)/1024)
}
