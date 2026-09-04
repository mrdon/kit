package menu

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
)

// Assembling a print run.
//
// Nothing here touches the network. The tap list, the descriptions, the
// wording and the images are all read from storage, because a print is
// somebody standing at a printer and a third party has no business being on
// that path. Filling the storage is Sync's job -- see print_sync.go -- and it
// happens when a person asks for it, where its failures can be read.
//
// This was not the first shape. The menu used to scrape Untappd on the way to
// the PDF, which meant a slow afternoon at Untappd was a slow print, and a
// blocked request was a sheet that came out with no descriptions and nothing
// anywhere to say why.
//
// The masthead photograph and the logo can still fail independently, and each
// failure costs exactly its own feature. The one thing that fails a print is
// having no tap list at all, because that is not a menu.

// printTimeout bounds a print. It is short now that nothing is fetched: this
// covers reading a few rows and two images out of Postgres and composing a
// PDF, and anything approaching it is a fault rather than a slow upstream.
const printTimeout = 10 * time.Second

// defaultFlight is what the strap says when a venue has not written one. It is
// the line the original carried, and it is true of every taproom that pours
// four-ounce tasters.
const defaultFlight = "Try any set of four 4oz pours as a flight"

// defaultSizes explains the one price column. The menu prints the full pour;
// every beer also has a half pour and a taster, and saying so once here beats
// three columns of numbers on every row.
const defaultSizes = "Full pours are 16oz unless marked — 9oz and 4oz also available"

// buildPrintMenu gathers everything one sheet needs, all of it from storage.
func (a *App) buildPrintMenu(ctx context.Context, tenant *models.Tenant) (PrintMenu, error) {
	state, err := LoadPrintState(ctx, a.pool, tenant.ID)
	if err != nil {
		return PrintMenu{}, err
	}
	cfg := state.Config

	rows := append([]Beer(nil), state.Rows...)
	if len(rows) == 0 && len(cfg.Extras) == 0 {
		return PrintMenu{}, ErrNotSynced
	}

	// Descriptions are stored, in two layers: what somebody wrote here wins
	// over what was scraped, which is what makes Kit the last word on the copy
	// a customer reads. Applied at build time rather than baked into the rows,
	// so correcting a description takes effect on the next print rather than
	// needing a re-sync.
	cache := state.Notes
	for name, note := range cfg.Notes {
		if strings.TrimSpace(note) == "" {
			continue
		}
		cache[normalizeBeerName(name)] = note
	}
	for i := range rows {
		if note, ok := cache[normalizeBeerName(rows[i].Name)]; ok && note != "" {
			rows[i].Notes = note
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
