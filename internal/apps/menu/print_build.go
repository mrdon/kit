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

// defaultFlight is what the masthead's flight badge says under "Try a flight"
// when a venue has not written its own line. It is true of every taproom that
// pours four-ounce tasters.
const defaultFlight = "Any four 4oz pours"

// defaultSizes explains the one price column. The menu prints the full pour;
// every beer also has a half pour and a taster, and saying so once here beats
// three columns of numbers on every row.
const defaultSizes = "Pours are 16oz unless marked · Also available in 9oz & 4oz"

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
	notes := mergedNotes(state.Notes, cfg.Notes)
	for i := range rows {
		if note, ok := findNote(notes, rows[i].Name); ok {
			rows[i].Notes = note
		}
	}

	out, err := LoadEightySix(ctx, a.pool, tenant.ID)
	if err != nil {
		return PrintMenu{}, err
	}
	rows = Overrides{Extras: cfg.Extras, EightySix: out}.applyRows(rows)
	if err := a.markGlutenReduced(ctx, tenant.ID, rows); err != nil {
		return PrintMenu{}, err
	}

	featured := a.resolveFeatured(ctx, tenant.ID, cfg.Featured, rows)

	m := PrintMenu{
		Featured:  featured,
		Title:     firstNonEmpty(cfg.Title, "Beers"),
		Subtitle:  firstNonEmpty(cfg.Subtitle, "& Beverages"),
		Flight:    firstNonEmpty(cfg.Flight, defaultFlight),
		Sizes:     firstNonEmpty(cfg.Sizes, defaultSizes),
		FootLeft:  cfg.FootLeft,
		FootRight: cfg.FootRight,
		Sections:  buildSections(rows, cfg.Blurbs),
		Brand:     a.printBrand(ctx, tenant),
	}
	a.attachPrintArt(ctx, tenant.ID, cfg, &m)
	return m, nil
}

// markGlutenReduced flags each gluten reduced beer for its GR badge; the
// footer carries the key. A load failure fails the print: a dietary mark
// silently missing from paper that then sits on tables for a month is worse
// than a retry.
func (a *App) markGlutenReduced(ctx context.Context, tenantID uuid.UUID, rows []Beer) error {
	beers, err := LoadGlutenReduced(ctx, a.pool, tenantID)
	if err != nil {
		return err
	}
	markGlutenReducedRows(rows, beers)
	return nil
}

func markGlutenReducedRows(rows []Beer, beers []string) {
	keys := glutenReducedKeys(beers)
	for i := range rows {
		if keys[nameKey(rows[i].Name)] {
			rows[i].GlutenReduced = true
		}
	}
}

// resolveFeatured turns the setting into the box, from storage only. The beer
// has to be on the printed tap list -- pouring, not 86'd -- or there is no
// box: paper that pushes a beer the bar cannot pour is worse than no push.
// Its row is starred in its own section. A photo that will not load costs the
// photo, never the box or the print.
func (a *App) resolveFeatured(ctx context.Context, tenantID uuid.UUID, fc *FeaturedConfig, rows []Beer) *Featured {
	if fc == nil || strings.TrimSpace(fc.Beer) == "" {
		return nil
	}
	want := nameKey(fc.Beer)
	for i := range rows {
		if nameKey(rows[i].Name) != want {
			continue
		}
		rows[i].Featured = true
		f := &Featured{
			Beer:  rows[i],
			Label: firstNonEmpty(fc.Label, defaultFeaturedLabel),
			Blurb: fc.Text,
		}
		if key, ok := strings.CutPrefix(fc.Photo, AssetRef); ok {
			a.attachFeaturedPhoto(ctx, tenantID, key, f)
		}
		return f
	}
	return nil
}

func (a *App) attachFeaturedPhoto(ctx context.Context, tenantID uuid.UUID, key string, f *Featured) {
	asset, err := loadPrintAsset(ctx, a.pool, tenantID, key)
	if err != nil {
		slog.Warn("menu print: loading featured photo", "tenant_id", tenantID, "key", key, "error", err)
		return
	}
	photo, err := cropPortrait(asset.Bytes)
	if err != nil {
		slog.Warn("menu print: cropping featured photo", "tenant_id", tenantID, "key", key, "error", err)
		return
	}
	f.Photo, f.PhotoKind = photo, "JPG"
}

// printBrand is the masthead's name: the board's wordmark, so paper and wall
// agree, falling back to the workspace's own name. A board that will not load
// costs the wordmark, never the print.
func (a *App) printBrand(ctx context.Context, tenant *models.Tenant) string {
	if row, err := GetBoard(ctx, a.pool, tenant.ID); err == nil && row != nil {
		if board, perr := ParseBoard(row.Payload); perr == nil {
			if w := strings.TrimSpace(board.Venue.Wordmark); w != "" {
				return w
			}
		}
	}
	return strings.TrimSpace(tenant.Name)
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
	// on the masthead uploads it under this key. It sits on the navy band, so
	// a mark in colour or knocked out white both read.
	for _, key := range printLogoKeys {
		asset, err := loadPrintAsset(ctx, a.pool, tenantID, key)
		if err != nil {
			continue
		}
		if imageKind(asset.Mime) == "PNG" {
			m.Logo = asset.Bytes
		}
		return
	}
}

// printLogoKeys are the asset keys the masthead logo is looked up under, in
// order. The key was "print_logo" until it turned out set_menu_asset only
// takes lowercase letters, digits and hyphens, so the documented key could
// never be set. "print-logo" is the one to use; the old one is still read in
// case a row was ever written by hand.
var printLogoKeys = []string{"print-logo", "print_logo"}

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
