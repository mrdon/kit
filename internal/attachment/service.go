// Package attachment is the general, tenant-scoped file store. It is the
// first place Kit persists original uploaded bytes (previously uploads were
// extracted to text and discarded). Bytes are encrypted at rest; every read
// is tenant-scoped. Consumers: the chat orchestrator (store turn
// attachments), the read_attachment tool, and the expense app (receipts).
package attachment

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/models"
)

// MaxBytes is the hard per-file size cap. The Claude API caps a base64 image
// at 10MB; we cap the raw upload here so even an image attachment stays
// servable and processable.
const MaxBytes = 10 << 20 // 10 MiB

// ErrTooLarge is returned by Store when raw exceeds MaxBytes.
var ErrTooLarge = fmt.Errorf("attachment exceeds %d bytes", MaxBytes)

// ErrNotFound re-exports the model sentinel so callers needn't import models.
var ErrNotFound = models.ErrAttachmentNotFound

// Service stores and loads encrypted attachments.
type Service struct {
	pool *pgxpool.Pool
	enc  *crypto.Encryptor
}

// NewService builds a Service. enc must be the same process Encryptor used
// elsewhere (bot tokens, vault); attachments share its key.
func NewService(pool *pgxpool.Pool, enc *crypto.Encryptor) *Service {
	return &Service{pool: pool, enc: enc}
}

// Store encrypts raw and persists it, returning the attachment metadata
// (without bytes). createdBy may be uuid.Nil for system-created rows.
func (s *Service) Store(ctx context.Context, tenantID, createdBy uuid.UUID, filename, mime string, raw []byte) (*models.Attachment, error) {
	if len(raw) == 0 {
		return nil, errors.New("attachment is empty")
	}
	if len(raw) > MaxBytes {
		return nil, ErrTooLarge
	}
	ciphertext, err := s.enc.EncryptBytes(raw)
	if err != nil {
		return nil, fmt.Errorf("encrypting attachment: %w", err)
	}
	return models.CreateAttachment(ctx, s.pool, tenantID, createdBy, filename, mime, len(raw), ciphertext)
}

// Load returns an attachment's metadata and decrypted bytes, scoped to the
// tenant. Returns ErrNotFound when no row matches (id, tenant).
func (s *Service) Load(ctx context.Context, tenantID, id uuid.UUID) (*models.Attachment, []byte, error) {
	a, err := models.GetAttachment(ctx, s.pool, tenantID, id)
	if err != nil {
		return nil, nil, err
	}
	plain, err := s.enc.DecryptBytes(a.Data)
	if err != nil {
		return nil, nil, fmt.Errorf("decrypting attachment: %w", err)
	}
	a.Data = nil // don't leak ciphertext to callers that only want metadata
	return a, plain, nil
}

// Meta returns attachment metadata only (no bytes), tenant-scoped.
func (s *Service) Meta(ctx context.Context, tenantID, id uuid.UUID) (*models.Attachment, error) {
	return models.GetAttachmentMeta(ctx, s.pool, tenantID, id)
}
