package kiosk

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrLabelInvalid is a button name that is missing or too long to sit next to
// Menu and Events; ErrButtonNotFound is a delete of a button that isn't there.
var (
	ErrLabelInvalid   = errors.New("button name is required and must be at most 30 characters")
	ErrButtonNotFound = errors.New("button not found")
)

const maxButtonLabel = 30

// Button is a workspace-defined screen destination: an outside page staff
// switch a screen to often enough to want it one tap away.
type Button struct {
	ID    uuid.UUID
	Label string
	URL   string
}

// ButtonInput is a new button as submitted from the console.
type ButtonInput struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// ListButtons returns the tenant's buttons in the order they were added.
func ListButtons(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) ([]Button, error) {
	const q = `SELECT id, label, url FROM app_kiosk_buttons
	           WHERE tenant_id = $1 ORDER BY created_at, id`
	rows, err := pool.Query(ctx, q, tenantID)
	if err != nil {
		return nil, fmt.Errorf("querying kiosk buttons: %w", err)
	}
	defer rows.Close()
	var out []Button
	for rows.Next() {
		var b Button
		if err := rows.Scan(&b.ID, &b.Label, &b.URL); err != nil {
			return nil, fmt.Errorf("scanning kiosk button: %w", err)
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating kiosk buttons: %w", err)
	}
	return out, nil
}

// CreateButton validates and stores a button.
func (s *Service) CreateButton(ctx context.Context, tenantID uuid.UUID, in ButtonInput) (*Button, error) {
	label := strings.TrimSpace(in.Label)
	url := strings.TrimSpace(in.URL)
	if label == "" || utf8.RuneCountInString(label) > maxButtonLabel {
		return nil, ErrLabelInvalid
	}
	if !ValidTargetURL(url) {
		return nil, ErrURLInvalid
	}
	b := Button{Label: label, URL: url}
	const q = `INSERT INTO app_kiosk_buttons (tenant_id, label, url) VALUES ($1, $2, $3) RETURNING id`
	if err := s.pool.QueryRow(ctx, q, tenantID, label, url).Scan(&b.ID); err != nil {
		return nil, fmt.Errorf("inserting kiosk button: %w", err)
	}
	return &b, nil
}

// DeleteButton removes a button. Screens already showing its URL keep it.
func (s *Service) DeleteButton(ctx context.Context, tenantID, id uuid.UUID) error {
	const q = `DELETE FROM app_kiosk_buttons WHERE tenant_id = $1 AND id = $2`
	tag, err := s.pool.Exec(ctx, q, tenantID, id)
	if err != nil {
		return fmt.Errorf("deleting kiosk button: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrButtonNotFound
	}
	return nil
}
