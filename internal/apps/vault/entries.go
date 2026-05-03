package vault

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
)

// EntryListItem is the metadata-only shape returned by list_secrets /
// find_secret. Never includes value_ciphertext — the agent must not see
// secrets even by accident.
type EntryListItem struct {
	ID           uuid.UUID  `json:"id"`
	Title        string     `json:"title"`
	Username     string     `json:"username,omitempty"`
	URL          string     `json:"url,omitempty"`
	Tags         []string   `json:"tags,omitempty"`
	LastViewedAt *time.Time `json:"last_viewed_at,omitempty"`
	ScopeSummary string     `json:"scope_summary"`
	// RoleID is the entry's owning role. Used by the reveal page to
	// prefill the role selector. After migration 047 this is always set.
	RoleID *uuid.UUID `json:"role_id,omitempty"`
	// RoleName is the resolved name of RoleID, or nil if the role row
	// is missing (orphaned scope — shouldn't happen, but the LEFT JOIN
	// keeps the entry visible if it does).
	RoleName *string `json:"role_name,omitempty"`
}

// EntryWithCiphertext is what the browser receives on the reveal/edit
// path: metadata + the encrypted value. The browser decrypts client-
// side. RoleID lives on the embedded EntryListItem.
type EntryWithCiphertext struct {
	EntryListItem
	OwnerUserID     uuid.UUID `json:"owner_user_id"`
	ValueCiphertext []byte    `json:"value_ciphertext"`
	ValueNonce      []byte    `json:"value_nonce"`
}

// CreateEntryParams is the input shape for creating a new vault entry.
// The caller is the implicit owner. value_ciphertext + value_nonce are
// produced in the browser and never decrypted server-side.
//
// RoleID is the single owning role: nil means "visible to everyone in
// the tenant", set means "visible to that role's members plus the
// owner". Per-user scoping was removed in v1.5; users who want to share
// with a single teammate either share with a role that contains them
// or accept the tenant-wide default.
type CreateEntryParams struct {
	Title           string
	Username        string
	URL             string
	Tags            []string
	ValueCiphertext []byte
	ValueNonce      []byte
	RoleID          *uuid.UUID
}

// UpdateEntryParams mirrors CreateEntryParams but never touches role_id.
// Re-scoping is done via SetEntryRole so the audit trail stays clean.
type UpdateEntryParams struct {
	Title           string
	Username        string
	URL             string
	Tags            []string
	ValueCiphertext []byte
	ValueNonce      []byte
}

// ListEntries returns entries the caller is authorized to view. Each
// result carries a human-readable scope_summary derived from the role
// so the agent can tell the user "yours" / "tenant-wide" / "shared" at
// a glance.
func (s *Service) ListEntries(ctx context.Context, c *services.Caller, query, tag string, roleID *uuid.UUID, limit int) ([]EntryListItem, error) {
	rows, err := models.ListVaultEntries(ctx, s.pool, c.TenantID, c.UserID, c.RoleIDs, query, tag, roleID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]EntryListItem, 0, len(rows))
	for _, e := range rows {
		out = append(out, toListItem(e, c))
	}
	return out, nil
}

// GetEntry returns one entry's metadata + ciphertext (caller-authz checked).
// Touching last_viewed_at is best-effort; failure doesn't block the read.
// Returns models.ErrNotFound for both miss and no-scope (uniform 404).
func (s *Service) GetEntry(ctx context.Context, c *services.Caller, entryID uuid.UUID, audit auditCtx) (*EntryWithCiphertext, error) {
	e, err := models.GetVaultEntry(ctx, s.pool, c.TenantID, entryID, c.UserID, c.RoleIDs)
	if err != nil {
		return nil, err
	}
	if e == nil {
		return nil, models.ErrNotFound
	}

	if err := models.TouchVaultEntryViewed(ctx, s.pool, c.TenantID, entryID); err != nil {
		slog.Warn("vault: touching last_viewed_at", "error", err)
	}
	audit.log(ctx, "vault.entry_view", "vault_entry", &entryID, EvtEntryView{})

	return &EntryWithCiphertext{
		EntryListItem:   toListItem(*e, c),
		OwnerUserID:     e.OwnerUserID,
		ValueCiphertext: e.ValueCiphertext,
		ValueNonce:      e.ValueNonce,
	}, nil
}

// CreateEntry inserts a new entry. RoleID is required: this is a team
// tool, and personal-only entries (no scope at all) aren't a user-facing
// concept. Pick a role you're a member of, or pass nil to mean
// "everyone in the tenant".
//
// TODO(primary-role): when Kit grows a "primary role" capability,
// default RoleID to the caller's primary role when unset rather than
// requiring the caller to pick. Until then, the web form ensures a
// pick before submit and the agent tools require an explicit role.
func (s *Service) CreateEntry(ctx context.Context, c *services.Caller, p CreateEntryParams, audit auditCtx) (uuid.UUID, error) {
	if err := validateCiphertext(p.ValueCiphertext, p.ValueNonce); err != nil {
		return uuid.Nil, err
	}
	if err := s.validateRoleForCaller(ctx, c, p.RoleID); err != nil {
		return uuid.Nil, err
	}
	id, err := models.CreateVaultEntry(ctx, s.pool, models.VaultEntry{
		TenantID:        c.TenantID,
		OwnerUserID:     c.UserID,
		RoleID:          p.RoleID,
		Title:           p.Title,
		Username:        nilIfEmpty(p.Username),
		URL:             nilIfEmpty(p.URL),
		Tags:            p.Tags,
		ValueCiphertext: p.ValueCiphertext,
		ValueNonce:      p.ValueNonce,
	})
	if err != nil {
		return uuid.Nil, err
	}
	audit.log(ctx, "vault.entry_create", "vault_entry", &id, EvtEntryCreate{})
	return id, nil
}

// UpdateEntry rewrites an entry's mutable fields. Re-scoping (role_id)
// goes through SetEntryRole, not here.
func (s *Service) UpdateEntry(ctx context.Context, c *services.Caller, entryID uuid.UUID, p UpdateEntryParams, audit auditCtx) error {
	if err := validateCiphertext(p.ValueCiphertext, p.ValueNonce); err != nil {
		return err
	}
	err := models.UpdateVaultEntry(ctx, s.pool, c.TenantID, entryID, c.UserID, c.RoleIDs, models.VaultEntry{
		Title:           p.Title,
		Username:        nilIfEmpty(p.Username),
		URL:             nilIfEmpty(p.URL),
		Tags:            p.Tags,
		ValueCiphertext: p.ValueCiphertext,
		ValueNonce:      p.ValueNonce,
	})
	if err != nil {
		return err
	}
	audit.log(ctx, "vault.entry_update", "vault_entry", &entryID, EvtEntryUpdate{})
	return nil
}

// DeleteEntry deletes an entry the caller is authorized to view.
func (s *Service) DeleteEntry(ctx context.Context, c *services.Caller, entryID uuid.UUID, audit auditCtx) error {
	if err := models.DeleteVaultEntry(ctx, s.pool, c.TenantID, entryID, c.UserID, c.RoleIDs); err != nil {
		return err
	}
	audit.log(ctx, "vault.entry_delete", "vault_entry", &entryID, EvtEntryDelete{})
	return nil
}

// SetEntryRole rewrites an entry's owning role. roleID is required;
// to make the entry visible to every tenant member, pass the tenant's
// 'member' role id (the default_role_id). Step-up auth required for
// any cross-role change (we don't know membership counts here, so we
// treat any move to a different role as widening and err on the side
// of step-up). No-op same-role calls bypass step-up.
func (s *Service) SetEntryRole(ctx context.Context, c *services.Caller, entryID uuid.UUID, roleID *uuid.UUID, audit auditCtx) error {
	existing, err := models.GetVaultEntry(ctx, s.pool, c.TenantID, entryID, c.UserID, c.RoleIDs)
	if err != nil {
		return err
	}
	if existing == nil {
		return models.ErrNotFound
	}
	if err := s.validateRoleForCaller(ctx, c, roleID); err != nil {
		return err
	}
	sameRole := existing.RoleID != nil && *existing.RoleID == *roleID
	if !sameRole {
		if err := s.requireRecentUnlock(ctx, c); err != nil {
			return err
		}
	}
	if err := models.SetVaultEntryRole(ctx, s.pool, c.TenantID, entryID, c.UserID, c.RoleIDs, roleID); err != nil {
		return err
	}
	audit.log(ctx, "vault.scope_change", "vault_entry", &entryID, EvtScopeChange{
		FromRoleID: existing.RoleID,
		ToRoleID:   roleID,
	})
	return nil
}
