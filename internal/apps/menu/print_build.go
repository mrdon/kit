package menu

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
)

// Assembling a print run.
//
// The paper menu reads the same upstream the wall board does, but it reads it
// itself rather than reusing the stored board: the board payload has already
// collapsed each beer to one price, and the printed columns need all of them.
// A print is a rare, deliberate act -- somebody is standing at a printer -- so
// one extra fetch is the right trade for not touching the board's shape.
//
// Everything after the tap list is best-effort. Descriptions, the masthead
// photograph and the logo can all fail independently, and each failure costs
// exactly its own feature. The one thing that can fail the print is having no
// tap list at all, because that is not a menu.

// printTimeout bounds the whole assembly -- the board fetch plus however many
// beer pages are new. It is generous compared to the board's eight seconds
// because nothing is waiting on it but the person who clicked print, and a
// menu that takes four seconds and has descriptions beats one that returns
// instantly without them.
const printTimeout = 25 * time.Second

// defaultFlight is what the strap says when a venue has not written one. It is
// the line the original carried, and it is true of every taproom that pours
// four-ounce tasters.
const defaultFlight = "Try any set of four 4oz pours as a flight"

// defaultSizes explains the one price column. The menu prints the full pour;
// every beer also has a half pour and a taster, and saying so once here beats
// three columns of numbers on every row.
const defaultSizes = "Full pours are 16oz unless marked — 9oz and 4oz also available"

// buildPrintMenu gathers everything one sheet needs.
func (a *App) buildPrintMenu(ctx context.Context, tenant *models.Tenant) (PrintMenu, error) {
	row, err := GetBoard(ctx, a.pool, tenant.ID)
	if err != nil && !errors.Is(err, ErrNotFound) && !errors.Is(err, pgx.ErrNoRows) {
		return PrintMenu{}, fmt.Errorf("loading menu board: %w", err)
	}

	state, err := LoadPrintState(ctx, a.pool, tenant.ID)
	if err != nil {
		return PrintMenu{}, err
	}
	cfg := state.Config

	rows, err := a.printRows(ctx, row)
	if err != nil {
		return PrintMenu{}, err
	}
	if len(rows) == 0 && len(cfg.Extras) == 0 {
		return PrintMenu{}, errors.New("no tap list to print")
	}

	// Descriptions. A brewery slug that is not set, or an Untappd that will
	// not answer, means a menu without prose -- not a failed print.
	//
	// Hand-written notes are folded in as though they were already cached, so
	// they both win over Untappd and stop it being asked about that beer.
	cache := state.Notes
	for name, note := range cfg.Notes {
		if strings.TrimSpace(note) == "" {
			continue
		}
		cache[normalizeBeerName(name)] = note
	}
	if brand := strings.TrimSpace(cfg.Brand); brand != "" && len(rows) > 0 {
		found, err := AttachNotes(ctx, printClient(), brand, rows, cache)
		if err != nil {
			slog.Warn("menu print: fetching descriptions",
				"tenant_id", tenant.ID, "brand", brand, "error", err)
		}
		if len(found) > 0 {
			if err := MergePrintNotes(ctx, a.pool, tenant.ID, found); err != nil {
				slog.Warn("menu print: caching descriptions",
					"tenant_id", tenant.ID, "error", err)
			}
		}
	}

	// With no brand configured the scrape never runs, so hand-written notes
	// still have to reach their rows.
	if strings.TrimSpace(cfg.Brand) == "" {
		for i := range rows {
			if note, ok := cache[normalizeBeerName(rows[i].Name)]; ok && note != "" {
				rows[i].Notes = note
			}
		}
	}

	m := PrintMenu{
		Title:     firstNonEmpty(cfg.Title, "Beers"),
		Subtitle:  firstNonEmpty(cfg.Subtitle, "& Beverages"),
		Flight:    firstNonEmpty(cfg.Flight, defaultFlight),
		Sizes:     firstNonEmpty(cfg.Sizes, defaultSizes),
		FootLeft:  cfg.FootLeft,
		FootRight: cfg.FootRight,
		Sections:  buildSections(mergeExtras(rows, cfg.Extras), cfg.Colors, cfg.Blurbs),
	}
	a.attachPrintArt(ctx, tenant.ID, cfg, &m)
	return m, nil
}

// printRows reads the tap list, preferring a live pull.
//
// The stored board is the fallback rather than the source. It has only the one
// headline price per beer, so a menu built from it prints a single column --
// correct, but thinner than the real thing. That is the right behaviour when
// Untappd is down, and the wrong one to choose by default.
//
// Only the live branch narrows to what is pouring. The stored board has
// already dropped everything unpriced on its way in, and its pours are
// reconstructed rather than real, so there is no four-ounce price left to
// test -- filtering it again would empty the menu.
func (a *App) printRows(ctx context.Context, row *BoardRow) ([]Beer, error) {
	if row == nil {
		return nil, nil
	}
	if row.SourceKind == SourceUntappd && strings.TrimSpace(row.SourceID) != "" {
		body, _, err := FetchUntappdBody(ctx, printClient(), row.SourceID)
		if err == nil {
			// Plausibility is judged on everything parsed, then the list is
			// narrowed to what pours -- so a board that genuinely lost most of
			// its beers still trips the tripwire rather than looking like a
			// menu with nothing on it.
			if rows := ParseBeers(body); len(rows) >= minPlausibleTaps {
				return OnTapOnly(rows), nil
			}
			slog.Warn("menu print: implausible scrape, falling back to stored board",
				"source_id", row.SourceID)
		} else {
			slog.Warn("menu print: fetching Untappd board, falling back to stored board",
				"source_id", row.SourceID, "error", err)
		}
	}
	return storedBeers(row)
}

// storedBeers converts the saved board into printable rows.
func storedBeers(row *BoardRow) ([]Beer, error) {
	if row == nil || len(row.Payload) == 0 {
		return nil, nil
	}
	board, err := ParseBoard(row.Payload)
	if err != nil {
		return nil, fmt.Errorf("reading stored board: %w", err)
	}
	out := make([]Beer, 0, len(board.Taps))
	for _, t := range board.Taps {
		size := t.Size
		if size == "" {
			size = DefaultPour
		}
		out = append(out, Beer{
			Section: t.Section,
			Name:    t.Name,
			Style:   t.Style,
			ABV:     t.ABV,
			Pours:   []Pour{{Size: size, Label: size + " Draft", Price: t.Price}},
		})
	}
	return out, nil
}

// attachPrintArt loads the masthead images. Both are optional and neither is
// worth failing a print over, so a missing or unreadable asset is logged and
// left out.
func (a *App) attachPrintArt(ctx context.Context, tenantID uuid.UUID, cfg PrintConfig, m *PrintMenu) {
	if key := strings.TrimSpace(cfg.Hero); key != "" {
		if asset, err := loadPrintAsset(ctx, a.pool, tenantID, key); err != nil {
			slog.Warn("menu print: loading hero asset",
				"tenant_id", tenantID, "key", key, "error", err)
		} else if kind := imageKind(asset.Mime); kind != "" {
			m.Hero, m.HeroKind = asset.Bytes, kind
		}
	}
	// The logo is conventional rather than configured: a venue that wants one
	// on the masthead uploads it under this key, knocked out white for the
	// colour band it sits on.
	if asset, err := loadPrintAsset(ctx, a.pool, tenantID, printLogoKey); err == nil {
		if imageKind(asset.Mime) == "PNG" {
			m.Logo = asset.Bytes
		}
	}
}

// printLogoKey is the asset key the masthead logo is looked up under.
const printLogoKey = "print_logo"

// loadPrintAsset reads one asset's bytes. LoadAssets returns data URIs for the
// board's inline rendering; a PDF wants the bytes themselves.
func loadPrintAsset(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, key string) (*Asset, error) {
	const q = `SELECT key, mime, bytes FROM app_menu_assets
	           WHERE tenant_id = $1 AND key = $2`
	var a Asset
	err := pool.QueryRow(ctx, q, tenantID, key).Scan(&a.Key, &a.Mime, &a.Bytes)
	if err != nil {
		return nil, fmt.Errorf("loading menu asset %q: %w", key, err)
	}
	return &a, nil
}

// imageKind maps a stored MIME type to fpdf's image-type string. An unknown
// type returns empty and the image is skipped: fpdf would otherwise guess from
// the bytes and fail the whole document on a mismatch.
func imageKind(mime string) string {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "image/png":
		return "PNG"
	case "image/jpeg", "image/jpg":
		return "JPG"
	case "image/gif":
		return "GIF"
	}
	return ""
}

// printClient reads both Untappd hosts. The timeout is per-request and longer
// than the board sync's, because this path is not on a wall display's critical
// path and a description is worth waiting a moment for.
func printClient() *http.Client {
	return &http.Client{Timeout: 12 * time.Second}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
