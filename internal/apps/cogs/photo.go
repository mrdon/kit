package cogs

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// maxPhotoBytes bounds a brew-sheet photo. The iPad scales it down before
// upload; this is the backstop.
const maxPhotoBytes = 8 << 20

// SavePhoto stores a brew-sheet photo before the brew that it belongs to is
// saved; the brew links it to the batch.
func (s *Service) SavePhoto(ctx context.Context, tenantID uuid.UUID, mime string, data []byte) (uuid.UUID, error) {
	switch mime {
	case "image/jpeg", "image/png", "image/webp", "image/heic":
	default:
		return uuid.Nil, fmt.Errorf("%w: a photo must be JPEG, PNG, WebP or HEIC", ErrInvalid)
	}
	if len(data) == 0 || len(data) > maxPhotoBytes {
		return uuid.Nil, fmt.Errorf("%w: the photo is empty or too big", ErrInvalid)
	}
	var id uuid.UUID
	if err := s.pool.QueryRow(ctx, `INSERT INTO app_cogs_photos (tenant_id, mime, bytes)
		VALUES ($1, $2, $3) RETURNING id`, tenantID, mime, data).Scan(&id); err != nil {
		return uuid.Nil, fmt.Errorf("saving photo: %w", err)
	}
	return id, nil
}

// Photo loads a stored photo.
func (s *Service) Photo(ctx context.Context, tenantID, id uuid.UUID) (string, []byte, error) {
	var mime string
	var data []byte
	err := s.pool.QueryRow(ctx, `SELECT mime, bytes FROM app_cogs_photos WHERE tenant_id = $1 AND id = $2`,
		tenantID, id).Scan(&mime, &data)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, fmt.Errorf("photo: %w", ErrNotFound)
	}
	if err != nil {
		return "", nil, fmt.Errorf("loading photo: %w", err)
	}
	return mime, data, nil
}

// BatchPhotos lists the photo ids attached to a batch.
func (s *Service) BatchPhotos(ctx context.Context, tenantID, batchID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx, `SELECT id FROM app_cogs_photos WHERE tenant_id = $1 AND batch_id = $2
		ORDER BY created_at`, tenantID, batchID)
	if err != nil {
		return nil, fmt.Errorf("listing photos: %w", err)
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scanning photo: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
