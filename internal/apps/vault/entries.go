package vault

import (
	"context"
	"errors"
	"fmt"
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
	IsOwner      bool       `json:"is_owner"`
}

// EntryWithCiphertext is what the browser receives on the reveal/edit
// path: metadata + the encrypted value (browser decrypts client-side).
type EntryWithCiphertext struct {
	EntryListItem
	OwnerUserID     uuid.UUID `json:"owner_user_id"`
	ValueCiphertext []byte    `json:"value_ciphertext"`
	ValueNonce      []byte    `json:"value_nonce"`
}

// CreateEntryParams is the input shape for creating a new vault entry.
// The caller is the implicit owner. value_ciphertext + value_nonce are
// produced in the browser and never decrypted server-side.
type CreateEntryParams struct {
	Title           string
	Username        string
	URL             string
	Tags            []string
	ValueCiphertext []byte
	ValueNonce      []byte
	Scopes          []models.VaultEntryScope // empty = owner-only (default-deny)
}

// UpdateEntryParams mirrors CreateEntryParams plus a flag to control
// whether scope rows are replaced. UpdateScopes=false leaves scopes
// alone; UpdateScopes=true replaces them with Scopes (empty slice
// reverts to owner-only).
type UpdateEntryParams struct {
	Title           string
	Username        string
	URL             string
	Tags            []string
	ValueCiphertext []byte
	ValueNonce      []byte
	Scopes          []models.VaultEntryScope
	UpdateScopes    bool
}

// ListEntries returns entries the caller is authorized to view.
// Applies the central scope filter (mirrors skill_scopes / rule_scopes).
// Each result carries a human-readable scope_summary derived from the
// authz scope rows so the agent can tell the user "yours" / "tenant-wide"
// / "shared" at a glance.
func (s *Service) ListEntries(ctx context.Context, c *services.Caller, query, tag string, limit int) ([]EntryListItem, error) {
	rows, err := models.ListVaultEntries(ctx, s.pool, c.TenantID, c.UserID, c.RoleIDs, query, tag, limit)
	if err != nil {
		return nil, err
	}

	tenantWide, err := s.tenantScopedEntryIDs(ctx, c.TenantID, rows)
	if err != nil {
		// Non-fatal: lose the "tenant-wide" label, keep the listing.
		slog.Warn("vault: building scope summary failed", "error", err)
		tenantWide = nil
	}

	out := make([]EntryListItem, 0, len(rows))
	for _, e := range rows {
		item := toListItem(e, c)
		switch {
		case item.IsOwner:
			item.ScopeSummary = "yours"
		case tenantWide[e.ID]:
			item.ScopeSummary = "tenant-wide"
		default:
			item.ScopeSummary = "shared"
		}
		out = append(out, item)
	}
	return out, nil
}

// tenantScopedEntryIDs returns the set of entry IDs in the given list
// that have a tenant-scope row. Used to label list_secrets results.
func (s *Service) tenantScopedEntryIDs(ctx context.Context, tenantID uuid.UUID, entries []models.VaultEntry) (map[uuid.UUID]bool, error) {
	if len(entries) == 0 {
		return map[uuid.UUID]bool{}, nil
	}
	ids := make([]uuid.UUID, 0, len(entries))
	for _, e := range entries {
		ids = append(ids, e.ID)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT entry_id FROM app_vault_entry_scopes
		WHERE tenant_id = $1 AND scope_kind = 'tenant' AND entry_id = ANY($2)
	`, tenantID, ids)
	if err != nil {
		return nil, fmt.Errorf("loading tenant scopes: %w", err)
	}
	defer rows.Close()
	out := make(map[uuid.UUID]bool, len(entries))
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
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

// CreateEntry inserts a new entry plus its scope rows in one transaction.
//
// Scopes is required: this is a team tool, and "personal-only" entries
// (no scope rows) are not a user-facing concept. The owner-implicit
// visibility in the SQL filter remains so the creator can always read
// their own row, but every new entry must carry at least one explicit
// scope so it shows up for someone other than the creator.
//
// TODO(primary-role): when Kit grows a "primary role" concept, default
// to the caller's primary role here when scopes is empty rather than
// rejecting. Until then, the web form is responsible for ensuring the
// user picks at least one scope before submit.
func (s *Service) CreateEntry(ctx context.Context, c *services.Caller, p CreateEntryParams, audit auditCtx) (uuid.UUID, error) {
	if err := validateCiphertext(p.ValueCiphertext, p.ValueNonce); err != nil {
		return uuid.Nil, err
	}
	if len(p.Scopes) == 0 {
		return uuid.Nil, errors.New("scope required: pick at least one role, user, or tenant-wide")
	}
	if err := validateScopes(p.Scopes); err != nil {
		return uuid.Nil, err
	}
	if err := s.validateScopesAgainstTenant(ctx, c.TenantID, p.Scopes); err != nil {
		return uuid.Nil, err
	}
	id, err := models.CreateVaultEntry(ctx, s.pool, models.VaultEntry{
		TenantID:        c.TenantID,
		OwnerUserID:     c.UserID,
		Title:           p.Title,
		Username:        nilIfEmpty(p.Username),
		URL:             nilIfEmpty(p.URL),
		Tags:            p.Tags,
		ValueCiphertext: p.ValueCiphertext,
		ValueNonce:      p.ValueNonce,
	}, p.Scopes)
	if err != nil {
		return uuid.Nil, err
	}
	audit.log(ctx, "vault.entry_create", "vault_entry", &id, EvtEntryCreate{})
	return id, nil
}

// UpdateEntry rewrites an entry the caller is authorized to view.
func (s *Service) UpdateEntry(ctx context.Context, c *services.Caller, entryID uuid.UUID, p UpdateEntryParams, audit auditCtx) error {
	if err := validateCiphertext(p.ValueCiphertext, p.ValueNonce); err != nil {
		return err
	}
	if p.UpdateScopes {
		if err := validateScopes(p.Scopes); err != nil {
			return err
		}
		if err := s.validateScopesAgainstTenant(ctx, c.TenantID, p.Scopes); err != nil {
			return err
		}
	}

	scopes := p.Scopes
	if !p.UpdateScopes {
		scopes = nil
	}

	err := models.UpdateVaultEntry(ctx, s.pool, c.TenantID, entryID, c.UserID, c.RoleIDs, models.VaultEntry{
		Title:           p.Title,
		Username:        nilIfEmpty(p.Username),
		URL:             nilIfEmpty(p.URL),
		Tags:            p.Tags,
		ValueCiphertext: p.ValueCiphertext,
		ValueNonce:      p.ValueNonce,
	}, scopes)
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

// UpdateScopes is the metadata-only "who can see this" change exposed as
// a gated agent/MCP tool. The diff against the existing scope set is
// logged to audit_events so widening operations leave a trail. Step-up
// auth required when widening (any added scope row); pure narrowing
// runs direct.
func (s *Service) UpdateScopes(ctx context.Context, c *services.Caller, entryID uuid.UUID, scopes []models.VaultEntryScope, audit auditCtx) error {
	existing, err := models.GetVaultEntry(ctx, s.pool, c.TenantID, entryID, c.UserID, c.RoleIDs)
	if err != nil {
		return err
	}
	if existing == nil {
		return models.ErrNotFound
	}
	if err := validateScopes(scopes); err != nil {
		return err
	}
	if err := s.validateScopesAgainstTenant(ctx, c.TenantID, scopes); err != nil {
		return err
	}

	prev, err := models.ListVaultEntryScopes(ctx, s.pool, c.TenantID, entryID)
	if err != nil {
		return err
	}
	added, removed := scopeDiff(prev, scopes)

	if len(added) > 0 {
		if err := s.requireRecentUnlock(ctx, c); err != nil {
			return err
		}
	}

	if err := models.ReplaceVaultEntryScopes(ctx, s.pool, c.TenantID, entryID, scopes); err != nil {
		return err
	}
	audit.log(ctx, "vault.scope_change", "vault_entry", &entryID, EvtScopeChange{
		Added:   added,
		Removed: removed,
	})
	return nil
}
