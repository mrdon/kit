package menu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PrintConfig is everything about the paper menu that the tap list cannot say.
//
// All of it is optional. A workspace that has set none of this still prints a
// usable menu -- the headings come from Untappd, the colours cycle through the
// house palette, and the masthead falls back to the tenant's name -- so the
// feature works before anybody configures it, and configuring it is how a
// venue makes it theirs.
type PrintConfig struct {
	// Brand is the brewery's untappd.com slug -- the "gravitybrewing" in
	// untappd.com/gravitybrewing. Without it there are no descriptions: the
	// digital board carries no prose, and nothing in it points at the consumer
	// pages that do.
	Brand string `json:"brand,omitempty"`

	Title     string `json:"title,omitempty"`      // "BEERS"
	Subtitle  string `json:"subtitle,omitempty"`   // "& Beverages"
	Flight    string `json:"flight,omitempty"`     // the strap above the footer
	Sizes     string `json:"sizes,omitempty"`      // the pour-sizes note under it
	FootLeft  string `json:"foot_left,omitempty"`  // "WIFI: Gravity  PW: ..."
	FootRight string `json:"foot_right,omitempty"` // "@thegravitybrewing"

	// Colors maps a section heading to a #rrggbb bar colour.
	Colors map[string]string `json:"colors,omitempty"`

	// Notes are descriptions written here rather than in Untappd, keyed by
	// beer name. They win over anything scraped, which makes them both the fix
	// for a beer Untappd has no page for and the way to say something on paper
	// that you do not want on your public brewery listing.
	Notes map[string]string `json:"notes,omitempty"`

	// Extras are the rows Untappd has no opinion about: canned non-alcoholics,
	// sodas, juice boxes. They are Beers rather than a lesser type because
	// a soda still has a section, a name and a price, and the renderer should
	// not have to care which half of the menu a row came from.
	Extras []Beer `json:"extras,omitempty"`

	// Hero is the asset key of the masthead photograph, uploaded with
	// set_menu_asset. Empty prints the masthead as a flat colour band, which
	// is a clean result rather than a degraded one.
	Hero string `json:"hero,omitempty"`
}

// PrintState is the stored row.
type PrintState struct {
	Config PrintConfig
	Notes  map[string]string // normalised beer name -> description
}

// LoadPrintState reads a workspace's paper-menu state. A workspace that has
// never printed has no row, which is not an error -- it is the starting state,
// and returns empty config with an empty cache.
func LoadPrintState(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) (*PrintState, error) {
	var rawConfig, rawNotes []byte
	err := pool.QueryRow(ctx,
		`SELECT config, notes FROM app_menu_print WHERE tenant_id = $1`,
		tenantID).Scan(&rawConfig, &rawNotes)
	if errors.Is(err, pgx.ErrNoRows) {
		return &PrintState{Notes: map[string]string{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("loading print state: %w", err)
	}
	state := &PrintState{Notes: map[string]string{}}
	if len(rawConfig) > 0 {
		if err := json.Unmarshal(rawConfig, &state.Config); err != nil {
			return nil, fmt.Errorf("decoding print config: %w", err)
		}
	}
	if len(rawNotes) > 0 {
		if err := json.Unmarshal(rawNotes, &state.Notes); err != nil {
			return nil, fmt.Errorf("decoding print notes: %w", err)
		}
	}
	if state.Notes == nil {
		state.Notes = map[string]string{}
	}
	return state, nil
}

// SavePrintConfig replaces a workspace's paper-menu settings, leaving the
// description cache alone.
func SavePrintConfig(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, cfg PrintConfig) error {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encoding print config: %w", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO app_menu_print (tenant_id, config)
		VALUES ($1, $2)
		ON CONFLICT (tenant_id) DO UPDATE
		SET config = EXCLUDED.config, updated_at = NOW()`,
		tenantID, raw)
	if err != nil {
		return fmt.Errorf("saving print config: %w", err)
	}
	return nil
}

// MergePrintNotes folds newly fetched descriptions into the cache.
//
// The merge happens in Postgres rather than by reading, merging and writing
// back, so two prints racing each other both keep their finds instead of the
// slower one clobbering the faster one's work.
func MergePrintNotes(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, notes map[string]string) error {
	if len(notes) == 0 {
		return nil
	}
	raw, err := json.Marshal(notes)
	if err != nil {
		return fmt.Errorf("encoding print notes: %w", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO app_menu_print (tenant_id, notes)
		VALUES ($1, $2)
		ON CONFLICT (tenant_id) DO UPDATE
		SET notes = app_menu_print.notes || EXCLUDED.notes, updated_at = NOW()`,
		tenantID, raw)
	if err != nil {
		return fmt.Errorf("saving print notes: %w", err)
	}
	return nil
}
