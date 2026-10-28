package menu

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// 86'd beers.
//
// Untappd will not give us API access, so when a keg blows the board keeps
// listing the beer until somebody edits Untappd, which nobody does mid-rush.
// This is the list staff reach for instead: a beer on it is left off the wall,
// the spotlight and the printed menu, exactly as if Untappd had dropped it.
//
// It is about a keg, not a recipe, so it does not outlive the tap the way the
// gluten reduced list does. When a sync stops seeing a beer, Untappd has caught
// up and the 86 has done its job, so the sync drops it here too. Otherwise the
// next keg of the same beer would come back hidden, and nobody would think to
// look for why.

// LoadEightySix reads the workspace's list. None stored is an empty list.
func LoadEightySix(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) ([]string, error) {
	const q = `SELECT beers FROM app_menu_eighty_six WHERE tenant_id = $1`
	var raw []byte
	err := pool.QueryRow(ctx, q, tenantID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return []string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("loading 86'd beers: %w", err)
	}
	beers := []string{}
	if err := json.Unmarshal(raw, &beers); err != nil {
		return nil, fmt.Errorf("decoding 86'd beers: %w", err)
	}
	if beers == nil {
		beers = []string{}
	}
	return beers, nil
}

// SaveEightySix replaces the workspace's list.
func SaveEightySix(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, beers []string) error {
	raw, err := json.Marshal(tidyBeerNames(beers))
	if err != nil {
		return fmt.Errorf("encoding 86'd beers: %w", err)
	}
	const q = `INSERT INTO app_menu_eighty_six (tenant_id, beers)
	           VALUES ($1, $2)
	           ON CONFLICT (tenant_id) DO UPDATE
	             SET beers = EXCLUDED.beers, updated_at = NOW()`
	if _, err := pool.Exec(ctx, q, tenantID, raw); err != nil {
		return fmt.Errorf("saving 86'd beers: %w", err)
	}
	return nil
}

// dropEightySix removes the taps on the list from the board.
func dropEightySix(b *Board, beers []string) {
	if len(beers) == 0 {
		return
	}
	out := glutenReducedKeys(beers)
	kept := b.Taps[:0:0]
	for _, t := range b.Taps {
		if !out[nameKey(t.Name)] {
			kept = append(kept, t)
		}
	}
	b.Taps = kept
}

// dropEightySixRows is dropEightySix for the printed menu's rows.
func dropEightySixRows(rows []Beer, beers []string) []Beer {
	if len(beers) == 0 {
		return rows
	}
	out := glutenReducedKeys(beers)
	kept := make([]Beer, 0, len(rows))
	for _, r := range rows {
		if !out[nameKey(r.Name)] {
			kept = append(kept, r)
		}
	}
	return kept
}

// stillPouring keeps the names that are on the tap list.
func stillPouring(beers []string, taps []Tap) []string {
	onTap := make(map[string]bool, len(taps))
	for _, t := range taps {
		onTap[nameKey(t.Name)] = true
	}
	kept := make([]string, 0, len(beers))
	for _, b := range beers {
		if onTap[nameKey(b)] {
			kept = append(kept, b)
		}
	}
	return kept
}

// pruneEightySix clears the 86 on every beer a sync no longer sees. A failure
// leaves the list alone: a beer hidden a little longer is the safe side.
func (a *App) pruneEightySix(ctx context.Context, tenantID uuid.UUID, taps []Tap) {
	beers, err := LoadEightySix(ctx, a.pool, tenantID)
	if err != nil || len(beers) == 0 {
		return
	}
	kept := stillPouring(beers, taps)
	if len(kept) == len(beers) {
		return
	}
	if err := SaveEightySix(ctx, a.pool, tenantID, kept); err != nil {
		slog.Warn("clearing 86'd beers Untappd dropped", "tenant_id", tenantID, "error", err)
	}
}

// eightySixFor loads the list for the wall. A failure to load shows the beer
// rather than blanking the board.
func (a *App) eightySixFor(ctx context.Context, tenantID uuid.UUID) []string {
	beers, err := LoadEightySix(ctx, a.pool, tenantID)
	if err != nil {
		slog.Warn("loading 86'd beers for the board", "tenant_id", tenantID, "error", err)
		return nil
	}
	return beers
}

// eightySixVersion puts the list's stamp in front of the version, so an 86
// reaches the wall on its next poll. A workspace with nothing 86'd keeps the
// stamp it had.
func eightySixVersion(beers []string, v string) string {
	if len(beers) == 0 {
		return v
	}
	keys := make([]string, 0, len(beers))
	for _, b := range beers {
		keys = append(keys, nameKey(b))
	}
	sum := sha256.Sum256([]byte(strings.Join(keys, "\n")))
	return "86" + hex.EncodeToString(sum[:])[:6] + "." + v
}
