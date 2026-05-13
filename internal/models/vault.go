package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// VaultTenant is the tenant-level vault crypto state. One row per tenant
// once an admin has set up the vault. Every member unlocks against the
// same auth_hash; the wrapped_vault_key is decrypted browser-side and
// then cached in a SharedWorker for the session.
type VaultTenant struct {
	TenantID             uuid.UUID
	KDFParams            json.RawMessage
	AuthHash             []byte
	WrappedVaultKey      []byte
	WrappedVaultKeyNonce []byte
	VaultGeneration      int
	LockedUntil          *time.Time
	LastRotatedByUserID  *uuid.UUID
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// VaultEntry is one stored secret. Title/username/url/tags are plaintext for
// search; value_ciphertext + value_nonce are AES-GCM(JSON{password,notes},
// vault_key) — encrypted in the browser, server never sees plaintext.
//
// Authz is a single role: RoleID NULL means "everyone in the tenant",
// RoleID set means "members of that role plus the owner". Per-user
// scoping was removed in v1.5; users who need fanout pick a role
// or tenant-wide.
type VaultEntry struct {
	ID              uuid.UUID
	TenantID        uuid.UUID
	OwnerUserID     uuid.UUID
	RoleID          *uuid.UUID
	RoleName        *string // populated by read paths via LEFT JOIN roles; nil for legacy/orphaned scopes
	Title           string
	Username        *string
	URL             *string
	Tags            []string
	ValueCiphertext []byte
	ValueNonce      []byte
	CreatedAt       time.Time
	UpdatedAt       time.Time
	LastViewedAt    *time.Time
}

// ===== vault_tenants =====

// GetVaultTenant returns the tenant's vault row, or (nil, nil) if not set up.
func GetVaultTenant(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) (*VaultTenant, error) {
	row := pool.QueryRow(ctx, `
		SELECT tenant_id, kdf_params, auth_hash,
		       wrapped_vault_key, wrapped_vault_key_nonce,
		       vault_generation, locked_until, last_rotated_by_user_id,
		       created_at, updated_at
		FROM app_vault_tenants
		WHERE tenant_id = $1
	`, tenantID)
	var v VaultTenant
	if err := row.Scan(
		&v.TenantID, &v.KDFParams, &v.AuthHash,
		&v.WrappedVaultKey, &v.WrappedVaultKeyNonce,
		&v.VaultGeneration, &v.LockedUntil, &v.LastRotatedByUserID,
		&v.CreatedAt, &v.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil //nolint:nilnil // not found
		}
		return nil, fmt.Errorf("getting vault tenant: %w", err)
	}
	return &v, nil
}

// VaultSetupParams is the input for one-time-per-tenant vault setup.
type VaultSetupParams struct {
	TenantID             uuid.UUID
	KDFParams            json.RawMessage
	AuthHash             []byte
	WrappedVaultKey      []byte
	WrappedVaultKeyNonce []byte
	SetupByUserID        uuid.UUID
}

// InitVaultTenant inserts the tenant's vault row. Refuses (returns
// ErrVaultAlreadySetup) if a row already exists — there is no "replace
// setup" path; replacing is rotate-with-old-password or nuke-and-redo.
func InitVaultTenant(ctx context.Context, pool *pgxpool.Pool, p VaultSetupParams) error {
	tag, err := pool.Exec(ctx, `
		INSERT INTO app_vault_tenants
			(tenant_id, kdf_params, auth_hash,
			 wrapped_vault_key, wrapped_vault_key_nonce,
			 vault_generation, last_rotated_by_user_id)
		VALUES ($1, $2, $3, $4, $5, 1, $6)
		ON CONFLICT (tenant_id) DO NOTHING
	`, p.TenantID, p.KDFParams, p.AuthHash,
		p.WrappedVaultKey, p.WrappedVaultKeyNonce, p.SetupByUserID)
	if err != nil {
		return fmt.Errorf("initializing vault tenant: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrVaultAlreadySetup
	}
	return nil
}

// VaultRotateParams is the input for rotation (admin replaces all the
// password-derived bits with new ones; vault_key is unchanged so entries
// remain decryptable under the new password).
type VaultRotateParams struct {
	TenantID             uuid.UUID
	KDFParams            json.RawMessage
	AuthHash             []byte
	WrappedVaultKey      []byte
	WrappedVaultKeyNonce []byte
	RotatedByUserID      uuid.UUID
}

// RotateVaultTenant atomically updates kdf_params/auth_hash/wrapped key
// and increments vault_generation. Refuses if no row exists.
func RotateVaultTenant(ctx context.Context, pool *pgxpool.Pool, p VaultRotateParams) (int, error) {
	var newGen int
	err := pool.QueryRow(ctx, `
		UPDATE app_vault_tenants
		   SET kdf_params = $2,
		       auth_hash = $3,
		       wrapped_vault_key = $4,
		       wrapped_vault_key_nonce = $5,
		       vault_generation = vault_generation + 1,
		       last_rotated_by_user_id = $6,
		       updated_at = now()
		 WHERE tenant_id = $1
		 RETURNING vault_generation
	`, p.TenantID, p.KDFParams, p.AuthHash, p.WrappedVaultKey,
		p.WrappedVaultKeyNonce, p.RotatedByUserID).Scan(&newGen)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrVaultNotSetUp
		}
		return 0, fmt.Errorf("rotating vault tenant: %w", err)
	}
	return newGen, nil
}

// NukeVaultTenant deletes the tenant's vault row and all entries in a
// single transaction. Returns the count of entries destroyed for audit.
// This is the recovery path when the shared password is lost — see the
// plan's "Nuke UX" section. There is no undo.
func NukeVaultTenant(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) (int, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("beginning nuke tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var entryCount int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM app_vault_entries WHERE tenant_id = $1
	`, tenantID).Scan(&entryCount); err != nil {
		return 0, fmt.Errorf("counting entries: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		DELETE FROM app_vault_entries WHERE tenant_id = $1
	`, tenantID); err != nil {
		return 0, fmt.Errorf("deleting entries: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		DELETE FROM app_vault_tenants WHERE tenant_id = $1
	`, tenantID); err != nil {
		return 0, fmt.Errorf("deleting tenant row: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("committing nuke: %w", err)
	}
	return entryCount, nil
}

// ===== vault_entries =====

// CreateVaultEntry inserts a new entry. RoleID NULL means "visible to
// everyone in the tenant"; set means "visible to that role's members
// plus the owner". The owner is always implicitly visible regardless
// of role.
func CreateVaultEntry(ctx context.Context, pool *pgxpool.Pool, e VaultEntry) (uuid.UUID, error) {
	tags := e.Tags
	if tags == nil {
		tags = []string{}
	}
	var id uuid.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO app_vault_entries
			(tenant_id, owner_user_id, role_id, title, username, url, tags,
			 value_ciphertext, value_nonce)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id
	`, e.TenantID, e.OwnerUserID, e.RoleID, e.Title, e.Username, e.URL, tags,
		e.ValueCiphertext, e.ValueNonce).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("inserting vault entry: %w", err)
	}
	return id, nil
}

// GetVaultEntry returns one entry the caller is authorized to view, or
// (nil, ErrNotFound) on miss-or-no-scope. Returning the same error for
// "doesn't exist" and "no scope" prevents existence enumeration.
func GetVaultEntry(ctx context.Context, pool *pgxpool.Pool, tenantID, entryID, callerID uuid.UUID, callerRoleIDs []uuid.UUID) (*VaultEntry, error) {
	roles := callerRoleIDs
	if roles == nil {
		roles = []uuid.UUID{}
	}
	row := pool.QueryRow(ctx, `
		SELECT e.id, e.tenant_id, e.owner_user_id, e.role_id, r.name,
		       e.title, e.username, e.url, e.tags,
		       e.value_ciphertext, e.value_nonce, e.created_at, e.updated_at, e.last_viewed_at
		FROM app_vault_entries e
		LEFT JOIN roles r ON r.id = e.role_id AND r.tenant_id = e.tenant_id
		WHERE e.tenant_id = $1 AND e.id = $2
		  AND ( e.owner_user_id = $3
		     OR e.role_id = ANY($4) )
	`, tenantID, entryID, callerID, roles)

	var e VaultEntry
	if err := row.Scan(
		&e.ID, &e.TenantID, &e.OwnerUserID, &e.RoleID, &e.RoleName,
		&e.Title, &e.Username, &e.URL, &e.Tags,
		&e.ValueCiphertext, &e.ValueNonce, &e.CreatedAt, &e.UpdatedAt, &e.LastViewedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("getting vault entry: %w", err)
	}
	return &e, nil
}

// ListVaultEntries returns entries the caller is authorized to view, optionally
// filtered by FTS query, tag, and owning role. roleID is an optional filter:
// when set, only entries scoped to that exact role are returned (and the
// existing authz filter still applies, so callers can't widen visibility).
func ListVaultEntries(ctx context.Context, pool *pgxpool.Pool, tenantID, callerID uuid.UUID, callerRoleIDs []uuid.UUID, query, tag string, roleID *uuid.UUID, limit int) ([]VaultEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	roles := callerRoleIDs
	if roles == nil {
		roles = []uuid.UUID{}
	}
	args := []any{tenantID, callerID, roles}
	where := "e.tenant_id = $1 AND ( e.owner_user_id = $2 OR e.role_id = ANY($3) )"
	orderBy := "e.last_viewed_at DESC NULLS LAST, e.created_at DESC"

	if query != "" {
		haystack := "coalesce(e.title,'')||' '||coalesce(e.url,'')||' '||coalesce(e.username,'')"
		args = append(args, query)
		ftsParam := len(args)
		fts := fmt.Sprintf("to_tsvector('english', %s) @@ plainto_tsquery('english', $%d)", haystack, ftsParam)
		// Per-token ILIKE fallback handles runtogether/camelCase titles
		// (e.g. "SignupGenius") that the English FTS dictionary keeps as a
		// single token, so a natural-language "signup genius" query
		// otherwise misses entirely.
		var ilikeClauses []string
		for tok := range strings.FieldsSeq(query) {
			args = append(args, "%"+escapeLikePattern(tok)+"%")
			ilikeClauses = append(ilikeClauses, fmt.Sprintf("%s ILIKE $%d", haystack, len(args)))
		}
		if len(ilikeClauses) > 0 {
			where += fmt.Sprintf(" AND ( %s OR ( %s ) )", fts, strings.Join(ilikeClauses, " AND "))
		} else {
			where += " AND " + fts
		}
		orderBy = fmt.Sprintf("ts_rank(to_tsvector('english', %s), plainto_tsquery('english', $%d)) DESC, %s", haystack, ftsParam, orderBy)
	}
	if tag != "" {
		args = append(args, tag)
		where += fmt.Sprintf(" AND $%d = ANY(e.tags)", len(args))
	}
	if roleID != nil {
		args = append(args, *roleID)
		where += fmt.Sprintf(" AND e.role_id = $%d", len(args))
	}
	args = append(args, limit)

	q := fmt.Sprintf(`
		SELECT e.id, e.tenant_id, e.owner_user_id, e.role_id, r.name,
		       e.title, e.username, e.url, e.tags,
		       e.value_ciphertext, e.value_nonce, e.created_at, e.updated_at, e.last_viewed_at
		FROM app_vault_entries e
		LEFT JOIN roles r ON r.id = e.role_id AND r.tenant_id = e.tenant_id
		WHERE %s
		ORDER BY %s
		LIMIT $%d
	`, where, orderBy, len(args))

	rows, err := pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("listing vault entries: %w", err)
	}
	defer rows.Close()
	return scanVaultEntries(rows)
}

// UpdateVaultEntry rewrites an entry's mutable fields. owner_user_id,
// tenant_id, and role_id are intentionally not updatable here — owner
// transfer / scope changes have their own endpoints.
func UpdateVaultEntry(ctx context.Context, pool *pgxpool.Pool, tenantID, entryID, callerID uuid.UUID, callerRoleIDs []uuid.UUID, e VaultEntry) error {
	roles := callerRoleIDs
	if roles == nil {
		roles = []uuid.UUID{}
	}
	tags := e.Tags
	if tags == nil {
		tags = []string{}
	}
	tag, err := pool.Exec(ctx, `
		UPDATE app_vault_entries ent
		   SET title = $5, username = $6, url = $7, tags = $8,
		       value_ciphertext = $9, value_nonce = $10, updated_at = now()
		 WHERE ent.tenant_id = $1 AND ent.id = $2
		   AND ( ent.owner_user_id = $3
		      OR ent.role_id IS NULL
		      OR ent.role_id = ANY($4) )
	`, tenantID, entryID, callerID, roles,
		e.Title, e.Username, e.URL, tags, e.ValueCiphertext, e.ValueNonce)
	if err != nil {
		return fmt.Errorf("updating vault entry: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetVaultEntryRole rewrites an entry's role_id. Pass nil to make the
// entry visible to everyone in the tenant. Authz: same as visibility —
// owner, role member, or tenant-wide can re-scope.
func SetVaultEntryRole(ctx context.Context, pool *pgxpool.Pool, tenantID, entryID, callerID uuid.UUID, callerRoleIDs []uuid.UUID, roleID *uuid.UUID) error {
	roles := callerRoleIDs
	if roles == nil {
		roles = []uuid.UUID{}
	}
	tag, err := pool.Exec(ctx, `
		UPDATE app_vault_entries ent
		   SET role_id = $5, updated_at = now()
		 WHERE ent.tenant_id = $1 AND ent.id = $2
		   AND ( ent.owner_user_id = $3
		      OR ent.role_id IS NULL
		      OR ent.role_id = ANY($4) )
	`, tenantID, entryID, callerID, roles, roleID)
	if err != nil {
		return fmt.Errorf("setting vault entry role: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteVaultEntry deletes an entry the caller is authorized to view.
// (v1: anyone with view authz can delete; v2 may want stricter rules.)
func DeleteVaultEntry(ctx context.Context, pool *pgxpool.Pool, tenantID, entryID, callerID uuid.UUID, callerRoleIDs []uuid.UUID) error {
	roles := callerRoleIDs
	if roles == nil {
		roles = []uuid.UUID{}
	}
	tag, err := pool.Exec(ctx, `
		DELETE FROM app_vault_entries ent
		WHERE ent.tenant_id = $1 AND ent.id = $2
		  AND ( ent.owner_user_id = $3
		     OR ent.role_id IS NULL
		     OR ent.role_id = ANY($4) )
	`, tenantID, entryID, callerID, roles)
	if err != nil {
		return fmt.Errorf("deleting vault entry: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// TouchVaultEntryViewed bumps last_viewed_at without authz check; callers
// must have already checked authz via GetVaultEntry.
func TouchVaultEntryViewed(ctx context.Context, pool *pgxpool.Pool, tenantID, entryID uuid.UUID) error {
	_, err := pool.Exec(ctx, `
		UPDATE app_vault_entries SET last_viewed_at = now()
		 WHERE tenant_id = $1 AND id = $2
	`, tenantID, entryID)
	if err != nil {
		return fmt.Errorf("touching vault entry: %w", err)
	}
	return nil
}

// ===== sentinels =====

// ErrNotFound is the canonical "not found / no scope" sentinel returned by
// vault read paths so callers can return uniform 404s.
var ErrNotFound = errors.New("not found")

// ErrVaultNotSetUp is returned by RotateVaultTenant when no tenant row
// exists yet. Callers should redirect to /setup.
var ErrVaultNotSetUp = errors.New("vault not set up")

// ErrVaultAlreadySetup is returned by InitVaultTenant when a tenant row
// already exists. Callers should redirect to /unlock or /rotate.
var ErrVaultAlreadySetup = errors.New("vault already set up")

// escapeLikePattern escapes the three LIKE metachars (\, %, _) so a free-text
// search term is treated as a literal substring rather than a pattern.
func escapeLikePattern(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "%", `\%`)
	s = strings.ReplaceAll(s, "_", `\_`)
	return s
}

func scanVaultEntries(rows pgx.Rows) ([]VaultEntry, error) {
	var out []VaultEntry
	for rows.Next() {
		var e VaultEntry
		if err := rows.Scan(
			&e.ID, &e.TenantID, &e.OwnerUserID, &e.RoleID, &e.RoleName,
			&e.Title, &e.Username, &e.URL, &e.Tags,
			&e.ValueCiphertext, &e.ValueNonce, &e.CreatedAt, &e.UpdatedAt, &e.LastViewedAt,
		); err != nil {
			return nil, fmt.Errorf("scan vault entry: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
