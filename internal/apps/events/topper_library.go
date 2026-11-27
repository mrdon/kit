package events

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The topper library: ready-made table toppers kept for printing on demand.
//
// The week's topper is composed from the events; these are finished PDFs
// designed somewhere else -- a happy hour card, a beer feature, a weekend
// sale. Kit stores them and the bar iPad lists them beside the week's card,
// so printing another copy is a tap rather than a hunt for the file. See
// migration 137 for the shape.

// errTopperNotFound is a library miss, kept apart from ErrNotFound so a tool
// says "no such topper" rather than "no such event".
var errTopperNotFound = errors.New("topper not found")

// topperTitleMax bounds a title, because it is a button label on a tablet.
const topperTitleMax = 60

// LibraryTopper is one stored topper.
type LibraryTopper struct {
	ID           uuid.UUID `json:"id"`
	Title        string    `json:"title"`
	AttachmentID uuid.UUID `json:"-"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// cleanTopperTitle trims a title and rejects one that would make a useless
// button.
func cleanTopperTitle(raw string) (string, error) {
	title := strings.Join(strings.Fields(raw), " ")
	switch {
	case title == "":
		return "", errors.New("a topper needs a title: it is the button staff press")
	case len([]rune(title)) > topperTitleMax:
		return "", fmt.Errorf("keep the title under %d characters: it is a button label", topperTitleMax)
	}
	return title, nil
}

func listLibraryToppers(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) ([]LibraryTopper, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, title, attachment_id, updated_at
		FROM app_event_topper_library
		WHERE tenant_id = $1
		ORDER BY lower(title)`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("listing library toppers: %w", err)
	}
	defer rows.Close()
	var out []LibraryTopper
	for rows.Next() {
		var t LibraryTopper
		if err := rows.Scan(&t.ID, &t.Title, &t.AttachmentID, &t.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning library topper: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func getLibraryTopper(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID) (*LibraryTopper, error) {
	var t LibraryTopper
	err := pool.QueryRow(ctx, `
		SELECT id, title, attachment_id, updated_at
		FROM app_event_topper_library
		WHERE tenant_id = $1 AND id = $2`, tenantID, id).
		Scan(&t.ID, &t.Title, &t.AttachmentID, &t.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errTopperNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("loading library topper: %w", err)
	}
	return &t, nil
}

// putLibraryTopper files an uploaded PDF under a title. Same title (any case)
// replaces the file in place, so the iPad's button keeps its id and its place;
// the replaced file's bytes are deleted rather than left behind unreferenced.
func putLibraryTopper(ctx context.Context, pool *pgxpool.Pool, tenantID, userID, attachmentID uuid.UUID, title string) (*LibraryTopper, bool, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("starting topper write: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var old uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT attachment_id FROM app_event_topper_library
		WHERE tenant_id = $1 AND lower(title) = lower($2)
		FOR UPDATE`, tenantID, title).Scan(&old)
	replaced := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, fmt.Errorf("finding existing topper: %w", err)
	}

	// Nil is a system upload, stored as no author rather than a dangling id.
	var createdBy *uuid.UUID
	if userID != uuid.Nil {
		createdBy = &userID
	}
	var t LibraryTopper
	err = tx.QueryRow(ctx, `
		INSERT INTO app_event_topper_library (tenant_id, title, attachment_id, created_by)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (tenant_id, lower(title)) DO UPDATE
			SET title = EXCLUDED.title, attachment_id = EXCLUDED.attachment_id, updated_at = NOW()
		RETURNING id, title, attachment_id, updated_at`,
		tenantID, title, attachmentID, createdBy).
		Scan(&t.ID, &t.Title, &t.AttachmentID, &t.UpdatedAt)
	if err != nil {
		return nil, false, fmt.Errorf("saving topper: %w", err)
	}
	if replaced && old != attachmentID {
		if _, err := tx.Exec(ctx, `DELETE FROM attachments WHERE tenant_id = $1 AND id = $2`,
			tenantID, old); err != nil {
			return nil, false, fmt.Errorf("deleting replaced topper file: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("committing topper: %w", err)
	}
	return &t, replaced, nil
}

// deleteLibraryTopper removes a topper and its file. Deleting the attachment
// cascades to the row, so one statement leaves nothing behind.
func deleteLibraryTopper(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID) (string, error) {
	var title string
	err := pool.QueryRow(ctx, `
		WITH gone AS (
			DELETE FROM attachments
			WHERE tenant_id = $1 AND id = (
				SELECT attachment_id FROM app_event_topper_library
				WHERE tenant_id = $1 AND id = $2)
			RETURNING id
		)
		SELECT title FROM app_event_topper_library
		WHERE tenant_id = $1 AND id = $2 AND attachment_id IN (SELECT id FROM gone)`,
		tenantID, id).Scan(&title)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errTopperNotFound
	}
	if err != nil {
		return "", fmt.Errorf("deleting topper: %w", err)
	}
	return title, nil
}
