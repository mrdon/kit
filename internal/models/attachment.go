package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Attachment is a tenant-scoped uploaded file. Data holds the encrypted
// bytes (nonce||ciphertext) exactly as stored; callers decrypt via
// internal/crypto. Size is the original plaintext length.
type Attachment struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	CreatedBy *uuid.UUID
	Filename  string
	Mime      string
	Size      int
	Data      []byte
	CreatedAt time.Time
}

// CreateAttachment inserts an encrypted attachment and returns its metadata
// (without the bytes). createdBy may be uuid.Nil for system-created rows.
func CreateAttachment(ctx context.Context, pool *pgxpool.Pool, tenantID, createdBy uuid.UUID, filename, mime string, size int, encrypted []byte) (*Attachment, error) {
	var createdByArg *uuid.UUID
	if createdBy != uuid.Nil {
		createdByArg = &createdBy
	}
	a := &Attachment{}
	err := pool.QueryRow(ctx, `
		INSERT INTO attachments (id, tenant_id, created_by, filename, mime, size, data)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, tenant_id, created_by, filename, mime, size, created_at
	`, uuid.New(), tenantID, createdByArg, filename, mime, size, encrypted).Scan(
		&a.ID, &a.TenantID, &a.CreatedBy, &a.Filename, &a.Mime, &a.Size, &a.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("creating attachment: %w", err)
	}
	return a, nil
}

// ErrAttachmentNotFound is returned when no attachment matches (id, tenant).
var ErrAttachmentNotFound = errors.New("attachment not found")

// GetAttachment loads an attachment (including encrypted Data) scoped to the
// tenant. Returns ErrAttachmentNotFound when no row matches.
func GetAttachment(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID) (*Attachment, error) {
	a := &Attachment{}
	err := pool.QueryRow(ctx, `
		SELECT id, tenant_id, created_by, filename, mime, size, data, created_at
		FROM attachments
		WHERE id = $1 AND tenant_id = $2
	`, id, tenantID).Scan(
		&a.ID, &a.TenantID, &a.CreatedBy, &a.Filename, &a.Mime, &a.Size, &a.Data, &a.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrAttachmentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("loading attachment: %w", err)
	}
	return a, nil
}

// GetAttachmentMeta loads attachment metadata without the bytes (for the
// turn manifest). Returns ErrAttachmentNotFound when no row matches.
func GetAttachmentMeta(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID) (*Attachment, error) {
	a := &Attachment{}
	err := pool.QueryRow(ctx, `
		SELECT id, tenant_id, created_by, filename, mime, size, created_at
		FROM attachments
		WHERE id = $1 AND tenant_id = $2
	`, id, tenantID).Scan(
		&a.ID, &a.TenantID, &a.CreatedBy, &a.Filename, &a.Mime, &a.Size, &a.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrAttachmentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("loading attachment meta: %w", err)
	}
	return a, nil
}
