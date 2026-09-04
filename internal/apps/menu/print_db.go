package menu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

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
	//
	// The three collections below carry no omitempty, unlike the strings. An
	// omitted empty map is indistinguishable from an absent one on the wire,
	// and a console doing `.map(...)` on the result gets `undefined` rather
	// than an empty list -- which in React takes the whole page down. Storing
	// "colors": {} is a little noisier and a lot harder to trip over.
	Colors map[string]string `json:"colors"`

	// Notes are descriptions written here rather than in Untappd, keyed by
	// beer name. They win over anything scraped, which makes them both the fix
	// for a beer Untappd has no page for and the way to say something on paper
	// that you do not want on your public brewery listing.
	Notes map[string]string `json:"notes"`

	// Blurbs map a section heading to a sentence printed under it, instead of
	// or before its rows. A heading named here that matches nothing on the tap
	// list becomes a section of its own at the end of the menu, which is how
	// snacks get onto a beer list: one heading, one line, no price column.
	Blurbs map[string]string `json:"blurbs"`

	// Extras are the rows Untappd has no opinion about: canned non-alcoholics,
	// sodas, juice boxes. They are Beers rather than a lesser type because
	// a soda still has a section, a name and a price, and the renderer should
	// not have to care which half of the menu a row came from.
	Extras []Beer `json:"extras"`

	// Hero is the asset key of the masthead photograph, uploaded with
	// set_menu_asset. Empty prints the masthead as a flat colour band, which
	// is a clean result rather than a degraded one.
	Hero string `json:"hero,omitempty"`
}

// PrintState is the stored row.
type PrintState struct {
	Config PrintConfig
	Notes  map[string]string // normalised beer name -> description

	// Rows is the tap list as of the last sync, and the only thing the printed
	// menu draws beers from.
	//
	// It used to be fetched on the way to the PDF. That put a third party on
	// the critical path of somebody standing at a printer, and made every
	// failure silent -- a sheet with no descriptions and nothing to say why.
	// Now a deliberate Sync fills it in and says what it could not reach.
	//
	// A sync replaces this wholesale, so a beer that comes off the board takes
	// its row with it. The copy survives regardless: descriptions live in
	// Notes, keyed by beer name, rather than being carried on the row.
	Rows []Beer

	// SyncedAt is when the tap list last agreed with upstream, and SyncError
	// what went wrong if it did not. Nil and empty mean nobody has synced yet,
	// which is the state every workspace starts in and the reason the settings
	// page leads with the button rather than the form.
	SyncedAt  *time.Time
	SyncError string
}

// LoadPrintState reads a workspace's paper-menu state. A workspace that has
// never printed has no row, which is not an error -- it is the starting state,
// and returns empty config with an empty cache.
func LoadPrintState(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) (*PrintState, error) {
	var rawConfig, rawNotes, rawRows []byte
	state := &PrintState{Notes: map[string]string{}}
	err := pool.QueryRow(ctx,
		`SELECT config, notes, rows, synced_at, sync_error
		   FROM app_menu_print WHERE tenant_id = $1`,
		tenantID).Scan(&rawConfig, &rawNotes, &rawRows, &state.SyncedAt, &state.SyncError)
	if errors.Is(err, pgx.ErrNoRows) {
		return &PrintState{Notes: map[string]string{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("loading print state: %w", err)
	}
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
	if len(rawRows) > 0 {
		if err := json.Unmarshal(rawRows, &state.Rows); err != nil {
			return nil, fmt.Errorf("decoding print rows: %w", err)
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

// SavePrintRows records a sync: the tap list it found, and what went wrong.
//
// Rows are replaced rather than merged. A beer that has come off the board
// should leave the menu, and a merge would keep it there forever -- the
// failure mode being a sheet that grows every seasonal the brewery has ever
// poured. What a person wrote survives anyway, because descriptions live in
// the notes cache under the beer's name.
//
// A failed sync still writes: syncErr is recorded so the settings page can say
// what happened, and the rows it did manage to read are kept. Half a tap list
// beats none, and the error beside it says not to trust it yet.
func SavePrintRows(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID,
	rows []Beer, syncErr string) error {
	if rows == nil {
		rows = []Beer{}
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		return fmt.Errorf("encoding print rows: %w", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO app_menu_print (tenant_id, rows, synced_at, sync_error)
		VALUES ($1, $2, NOW(), $3)
		ON CONFLICT (tenant_id) DO UPDATE
		SET rows = EXCLUDED.rows, synced_at = NOW(),
		    sync_error = EXCLUDED.sync_error, updated_at = NOW()`,
		tenantID, raw, syncErr)
	if err != nil {
		return fmt.Errorf("saving print rows: %w", err)
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

// DeletePrintNotes removes cached descriptions by beer name.
//
// The key is the normalised name, the same one MergePrintNotes writes under,
// so a caller clears a description using the name it reads on the menu rather
// than having to know how Kit files it.
func DeletePrintNotes(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, names []string) error {
	if len(names) == 0 {
		return nil
	}
	keys := make([]string, 0, len(names))
	for _, n := range names {
		if key := normalizeBeerName(n); key != "" {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return nil
	}
	// `- text[]` drops every listed key in one statement, so this cannot lose
	// a concurrent write the way a read-modify-write would.
	_, err := pool.Exec(ctx,
		`UPDATE app_menu_print SET notes = notes - $2::text[], updated_at = NOW()
		  WHERE tenant_id = $1`, tenantID, keys)
	if err != nil {
		return fmt.Errorf("clearing print notes: %w", err)
	}
	return nil
}
