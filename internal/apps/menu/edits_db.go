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

// Edits are Kit's standing changes to the Untappd tap list: the items it adds
// and the names it hides. Both menus read them. See overrides.go for how they
// are applied and migration 129 for why they live apart from the printed
// menu's settings.
type Edits struct {
	Items  []Beer `json:"items"`
	Hidden Hidden `json:"hidden"`
}

// Hidden is what stays off both menus until somebody unhides it. Unlike an 86
// it survives a sync: a beer Untappd drops and later brings back is still
// hidden, because hiding is a decision about the menu, not about the keg.
type Hidden struct {
	Items    []string `json:"items"`
	Sections []string `json:"sections"`
}

// LoadEdits reads a workspace's edits. No row is the starting state: no items,
// nothing hidden.
func LoadEdits(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) (Edits, error) {
	var rawItems, rawHidden []byte
	err := pool.QueryRow(ctx,
		`SELECT items, hidden FROM app_menu_edits WHERE tenant_id = $1`,
		tenantID).Scan(&rawItems, &rawHidden)
	if errors.Is(err, pgx.ErrNoRows) {
		return Edits{}.normalized(), nil
	}
	if err != nil {
		return Edits{}, fmt.Errorf("loading menu edits: %w", err)
	}
	var e Edits
	if err := json.Unmarshal(rawItems, &e.Items); err != nil {
		return Edits{}, fmt.Errorf("decoding menu items: %w", err)
	}
	if err := json.Unmarshal(rawHidden, &e.Hidden); err != nil {
		return Edits{}, fmt.Errorf("decoding hidden menu names: %w", err)
	}
	return e.normalized(), nil
}

// SaveEdits replaces a workspace's edits.
func SaveEdits(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, e Edits) error {
	e = e.normalized()
	items, err := json.Marshal(e.Items)
	if err != nil {
		return fmt.Errorf("encoding menu items: %w", err)
	}
	hidden, err := json.Marshal(e.Hidden)
	if err != nil {
		return fmt.Errorf("encoding hidden menu names: %w", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO app_menu_edits (tenant_id, items, hidden)
		VALUES ($1, $2, $3)
		ON CONFLICT (tenant_id) DO UPDATE
		SET items = EXCLUDED.items, hidden = EXCLUDED.hidden, updated_at = NOW()`,
		tenantID, items, hidden)
	if err != nil {
		return fmt.Errorf("saving menu edits: %w", err)
	}
	return nil
}

// normalized never leaves a nil list: the console maps over every one of
// them, and a null takes the page down.
func (e Edits) normalized() Edits {
	if e.Items == nil {
		e.Items = []Beer{}
	}
	if e.Hidden.Items == nil {
		e.Hidden.Items = []string{}
	}
	if e.Hidden.Sections == nil {
		e.Hidden.Sections = []string{}
	}
	return e
}
