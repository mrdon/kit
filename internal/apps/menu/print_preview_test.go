package menu

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"
)

// Live checks against the real Untappd, opt-in.
//
// The fixture in testdata is a snapshot and drifts: beers come off the board,
// prices change, and a preview built from it shows a menu that was true in
// August. These fetch the real board and the real descriptions, which is the
// only way to know the whole chain works -- board scrape, brewery listing,
// beer pages, and the layout under real names and real line lengths.
//
// They need the network and somebody else's uptime, so they skip unless asked
// and never run in CI.
//
//	MENU_PRINT_BOARD=22128 MENU_PRINT_BRAND=gravitybrewing \
//	MENU_PRINT_OUT=/tmp/menu.pdf \
//	go test ./internal/apps/menu/ -run TestPrintLive -v

const liveTimeout = 60 * time.Second

// TestPrintLive fetches everything and renders, reporting what it got. It is
// both the design preview and the end-to-end check: if the board moves under
// us, or Untappd reskins a beer page, this is where it shows up.
func TestPrintLive(t *testing.T) {
	board := os.Getenv("MENU_PRINT_BOARD")
	if board == "" {
		t.Skip("set MENU_PRINT_BOARD (and optionally MENU_PRINT_BRAND, MENU_PRINT_OUT)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), liveTimeout)
	defer cancel()
	client := printClient()

	body, _, err := FetchUntappdBody(ctx, client, board)
	if err != nil {
		t.Fatalf("fetching board %s: %v", board, err)
	}
	parsed := ParseBeers(body)
	if len(parsed) < minPlausibleTaps {
		t.Fatalf("parsed %d rows from board %s, want at least %d",
			len(parsed), board, minPlausibleTaps)
	}
	rows := OnTapOnly(parsed)
	t.Logf("board %s: %d rows parsed, %d actually pouring", board, len(parsed), len(rows))
	for _, b := range parsed {
		if !b.OnTap() {
			t.Logf("  not pouring, left off: %s (%d containers)", b.Name, len(b.Pours))
		}
	}

	if brand := os.Getenv("MENU_PRINT_BRAND"); brand != "" {
		found, unmatched, err := AttachNotes(ctx, client, brand, rows, map[string]string{})
		if err != nil {
			t.Fatalf("fetching descriptions for %s: %v", brand, err)
		}
		t.Logf("descriptions: %d of %d taps", len(found), len(rows))
		// A tap whose page the listings never showed us is a coverage bug, not
		// a beer nobody wrote up. Two of Gravity's sixteen were invisible this
		// way for weeks, so the live check fails on it rather than logging it.
		if len(unmatched) > 0 {
			t.Errorf("no Untappd page found for %v — the brewery listings are not covering the board",
				unmatched)
		}
		// Some beers genuinely have no write-up in Untappd, but most do. A
		// run that matches almost nothing means the name matching broke, not
		// that the brewery stopped writing.
		if len(found)*2 < len(rows) {
			t.Errorf("only %d of %d taps matched a description — name matching looks broken",
				len(found), len(rows))
		}
	}

	cfg := samplePrintConfig()
	m := PrintMenu{
		Title:     cfg.Title,
		Subtitle:  cfg.Subtitle,
		Flight:    cfg.Flight,
		Sizes:     defaultSizes,
		FootLeft:  cfg.FootLeft,
		FootRight: cfg.FootRight,
		Sections:  buildSections(mergeExtras(rows, cfg.Extras), cfg.Colors, cfg.Blurbs),
	}
	for _, s := range m.Sections {
		t.Logf("  %-20s %d rows", s.Name, len(s.Rows))
		for _, r := range s.Rows {
			pour, _ := r.Headline()
			t.Logf("    %-30s %-24s %-6s %s %s  notes:%d",
				r.Name, tidyStyle(r.Style), tidyABV(r.ABV), money(pour.Price), pour.Size, len(r.Notes))
		}
	}

	var buf bytes.Buffer
	if err := RenderPrintPDF(m, &buf); err != nil {
		t.Fatalf("rendering: %v", err)
	}
	if !bytes.HasPrefix(buf.Bytes(), []byte("%PDF-")) {
		t.Fatal("output is not a PDF")
	}
	if out := os.Getenv("MENU_PRINT_OUT"); out != "" {
		if err := os.WriteFile(out, buf.Bytes(), 0o600); err != nil {
			t.Fatalf("writing %s: %v", out, err)
		}
		t.Logf("wrote %s (%d KB)", out, buf.Len()/1024)
	}
}
