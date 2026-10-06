package menu

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Gluten reduced beers.
//
// A list of beer names, stored once and applied at render time, the same way
// happy hour is. It is about the recipe rather than the tap, so it survives a
// beer going off: the day it comes back, the wall marks it again. Untappd has
// no field to carry it, and the tap list is replaced whole on every sync, so
// there is nowhere upstream to keep it even if we wanted to.
//
// The wording is "gluten reduced", never "gluten free". A beer brewed from
// barley and treated to break the gluten down cannot be called gluten free
// (TTB Ruling 2014-2), and the people who read this mark are the ones for whom
// the difference matters. So the mark always travels with the caveat: the
// wall's footer carries it beside the badge's key, and the printed menu puts
// it in the beer's own line.

// GlutenReducedNote is the caveat printed wherever a beer is marked.
const GlutenReducedNote = "Gluten reduced. May contain gluten."

// LoadGlutenReduced reads the workspace's list. None stored is an empty list.
func LoadGlutenReduced(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) ([]string, error) {
	const q = `SELECT beers FROM app_menu_gluten_reduced WHERE tenant_id = $1`
	var raw []byte
	err := pool.QueryRow(ctx, q, tenantID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return []string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("loading gluten reduced beers: %w", err)
	}
	beers := []string{}
	if err := json.Unmarshal(raw, &beers); err != nil {
		return nil, fmt.Errorf("decoding gluten reduced beers: %w", err)
	}
	return beers, nil
}

// SaveGlutenReduced replaces the workspace's list.
func SaveGlutenReduced(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, beers []string) error {
	raw, err := json.Marshal(tidyBeerNames(beers))
	if err != nil {
		return fmt.Errorf("encoding gluten reduced beers: %w", err)
	}
	const q = `INSERT INTO app_menu_gluten_reduced (tenant_id, beers)
	           VALUES ($1, $2)
	           ON CONFLICT (tenant_id) DO UPDATE
	             SET beers = EXCLUDED.beers, updated_at = NOW()`
	if _, err := pool.Exec(ctx, q, tenantID, raw); err != nil {
		return fmt.Errorf("saving gluten reduced beers: %w", err)
	}
	return nil
}

// tidyBeerNames collapses spacing and drops blanks and duplicates, keeping
// the first spelling, so the stored list reads the way somebody typed it.
func tidyBeerNames(beers []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(beers))
	for _, b := range beers {
		b = strings.Join(strings.Fields(b), " ")
		if b == "" || seen[nameKey(b)] {
			continue
		}
		seen[nameKey(b)] = true
		out = append(out, b)
	}
	return out
}

// glutenReducedKeys is the list as join keys.
func glutenReducedKeys(beers []string) map[string]bool {
	keys := make(map[string]bool, len(beers))
	for _, b := range beers {
		keys[nameKey(b)] = true
	}
	return keys
}

// applyGlutenReduced marks the taps on the list.
func applyGlutenReduced(b *Board, beers []string) {
	keys := glutenReducedKeys(beers)
	for i := range b.Taps {
		b.Taps[i].GlutenReduced = keys[nameKey(b.Taps[i].Name)]
	}
}

// glutenStamp is the list's share of the board's version stamp, so marking a
// beer reaches the wall without waiting for a keg to blow. Empty when nothing
// is marked, which keeps a workspace that never uses this on the stamp it had.
func glutenStamp(beers []string) string {
	if len(beers) == 0 {
		return ""
	}
	keys := make([]string, 0, len(beers))
	for _, b := range beers {
		keys = append(keys, nameKey(b))
	}
	sum := sha256.Sum256([]byte(strings.Join(keys, "\n")))
	return "gr" + hex.EncodeToString(sum[:])[:6]
}
