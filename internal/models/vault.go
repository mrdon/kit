package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// VaultUser is one user's per-tenant crypto state. The wrapped_vault_key is
// nil until an existing tenant member grants this user access; pending is
// false only after the browser has completed a self-unlock test.
type VaultUser struct {
	TenantID                 uuid.UUID
	UserID                   uuid.UUID
	KDFParams                json.RawMessage
	AuthHash                 []byte
	UserPublicKey            []byte
	UserPrivateKeyCiphertext []byte
	UserPrivateKeyNonce      []byte
	WrappedVaultKey          []byte // nil = not yet granted
	GrantedByUserID          *uuid.UUID
	GrantedAt                *time.Time
	FailedUnlocks            int
	LockedUntil              *time.Time
	ResetPendingUntil        *time.Time
	Pending                  bool
	CreatedAt                time.Time
	UpdatedAt                time.Time
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

// ===== vault_users =====

// GetVaultUser returns the caller's vault_users row, or (nil, nil) if none.
func GetVaultUser(ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID) (*VaultUser, error) {
	row := pool.QueryRow(ctx, `
		SELECT tenant_id, user_id, kdf_params, auth_hash, user_public_key,
		       user_private_key_ciphertext, user_private_key_nonce,
		       wrapped_vault_key, granted_by_user_id, granted_at,
		       failed_unlocks, locked_until, reset_pending_until, pending,
		       created_at, updated_at
		FROM app_vault_users
		WHERE tenant_id = $1 AND user_id = $2
	`, tenantID, userID)
	var v VaultUser
	if err := row.Scan(
		&v.TenantID, &v.UserID, &v.KDFParams, &v.AuthHash, &v.UserPublicKey,
		&v.UserPrivateKeyCiphertext, &v.UserPrivateKeyNonce,
		&v.WrappedVaultKey, &v.GrantedByUserID, &v.GrantedAt,
		&v.FailedUnlocks, &v.LockedUntil, &v.ResetPendingUntil, &v.Pending,
		&v.CreatedAt, &v.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil //nolint:nilnil // not found
		}
		return nil, fmt.Errorf("getting vault user: %w", err)
	}
	return &v, nil
}

// VaultRegisterParams is the input to upsert a vault_users row at registration
// or master-password reset (with Replace=true). For the very first user in a
// tenant, WrappedVaultKey is set in the same call (self-grant); for subsequent
// users it is nil and waits for an existing member to call SetGrant.
type VaultRegisterParams struct {
	TenantID                 uuid.UUID
	UserID                   uuid.UUID
	KDFParams                json.RawMessage
	AuthHash                 []byte
	UserPublicKey            []byte
	UserPrivateKeyCiphertext []byte
	UserPrivateKeyNonce      []byte
	WrappedVaultKey          []byte // non-nil only for tenant initializer
	Replace                  bool   // master-password reset path
}

// AnyVaultUserExists returns true if the tenant vault has been
// successfully bootstrapped — at least one user holds a
// wrapped_vault_key AND has completed the self-unlock canary
// (pending=false). Pending rows from a failed bootstrap retry don't
// count, so a canary-failure retry by the same admin still classifies
// as "bootstrap" and they can re-upload a fresh wrapped_vault_key.
func AnyVaultUserExists(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) (bool, error) {
	var ok bool
	err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM app_vault_users
			WHERE tenant_id = $1
			  AND wrapped_vault_key IS NOT NULL
			  AND pending = FALSE
		)
	`, tenantID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("checking vault initialization: %w", err)
	}
	return ok, nil
}

// RegisterVaultUser inserts (or, with Replace=true, replaces) a vault_users row.
// All new rows start pending=true; the caller flips it via MarkVaultUserActive
// after the browser passes the self-unlock canary.
func RegisterVaultUser(ctx context.Context, pool *pgxpool.Pool, p VaultRegisterParams) error {
	if p.Replace {
		// Replace requires an existing row. If the row was deleted
		// between the service-layer GetVaultUser check and now, we
		// should fail rather than silently no-op.
		tag, err := pool.Exec(ctx, `
			UPDATE app_vault_users
			   SET kdf_params = $3,
			       auth_hash = $4,
			       user_public_key = $5,
			       user_private_key_ciphertext = $6,
			       user_private_key_nonce = $7,
			       wrapped_vault_key = NULL,
			       granted_by_user_id = NULL,
			       granted_at = NULL,
			       failed_unlocks = 0,
			       locked_until = NULL,
			       reset_pending_until = now() + interval '24 hours',
			       pending = TRUE,
			       updated_at = now()
			 WHERE tenant_id = $1 AND user_id = $2
		`, p.TenantID, p.UserID, p.KDFParams, p.AuthHash, p.UserPublicKey,
			p.UserPrivateKeyCiphertext, p.UserPrivateKeyNonce)
		if err != nil {
			return fmt.Errorf("replacing vault user: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrVaultUserNotFound
		}
		return nil
	}

	// Bootstrap initiator (first user in tenant): WrappedVaultKey is set,
	// granted_by_user_id stays NULL so the partial unique index
	// idx_app_vault_first_user matches and prevents a second initiator.
	// granted_at is set to "now" so we have a record of when the tenant
	// vault was bootstrapped.
	var grantedAt any
	if p.WrappedVaultKey != nil {
		grantedAt = time.Now()
	}
	// UPSERT keyed on (tenant_id, user_id), but ONLY allowed when the
	// existing row is still pending=true. If a user's previous register
	// attempt threw between INSERT and the self-unlock canary POST, this
	// lets them retry by submitting again. Activated rows are protected
	// by the WHERE clause; the only way to replace those is the explicit
	// Replace=true path, which is gated separately.
	tag, err := pool.Exec(ctx, `
		INSERT INTO app_vault_users
			(tenant_id, user_id, kdf_params, auth_hash, user_public_key,
			 user_private_key_ciphertext, user_private_key_nonce,
			 wrapped_vault_key, granted_by_user_id, granted_at, pending)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULL, $9, TRUE)
		ON CONFLICT (tenant_id, user_id) DO UPDATE SET
			kdf_params = EXCLUDED.kdf_params,
			auth_hash = EXCLUDED.auth_hash,
			user_public_key = EXCLUDED.user_public_key,
			user_private_key_ciphertext = EXCLUDED.user_private_key_ciphertext,
			user_private_key_nonce = EXCLUDED.user_private_key_nonce,
			wrapped_vault_key = EXCLUDED.wrapped_vault_key,
			granted_at = EXCLUDED.granted_at,
			updated_at = now()
		WHERE app_vault_users.pending = TRUE
	`, p.TenantID, p.UserID, p.KDFParams, p.AuthHash, p.UserPublicKey,
		p.UserPrivateKeyCiphertext, p.UserPrivateKeyNonce,
		p.WrappedVaultKey, grantedAt)
	if err != nil {
		return fmt.Errorf("inserting vault user: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Row exists and is already active (pending=false). Caller must
		// use Replace=true to reset.
		return errors.New("vault user already active; use master-password reset to replace")
	}
	return nil
}

// MarkVaultUserActive flips pending=false after a successful self-unlock test.
func MarkVaultUserActive(ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID) error {
	_, err := pool.Exec(ctx, `
		UPDATE app_vault_users
		   SET pending = FALSE, updated_at = now()
		 WHERE tenant_id = $1 AND user_id = $2
	`, tenantID, userID)
	if err != nil {
		return fmt.Errorf("marking vault user active: %w", err)
	}
	return nil
}

// CancelVaultReset deletes a vault_users row that's currently in the
// 24h reset cooldown. Used when the legitimate user spots a reset they
// didn't initiate (Slack-account-takeover defense): wiping the row
// invalidates the attacker-supplied keys before any teammate can grant
// against them. The user is left in a "not registered" state and must
// re-register normally — same path as a fresh user joining the vault.
//
// Refuses if the row exists but isn't in cooldown (no reset to cancel)
// or if the row is missing (no row, no reset). Returns ErrNotFound in
// either case so callers can surface a uniform "nothing to do" error.
func CancelVaultReset(ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID) error {
	tag, err := pool.Exec(ctx, `
		DELETE FROM app_vault_users
		 WHERE tenant_id = $1 AND user_id = $2
		   AND reset_pending_until IS NOT NULL
		   AND reset_pending_until > now()
	`, tenantID, userID)
	if err != nil {
		return fmt.Errorf("cancelling vault reset: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// AdminDeleteVaultUser unconditionally removes a user's vault_users row.
// Used by the admin-driven master-password reset path: the admin approves
// a decision card requesting reset for a user who forgot theirs; that
// approval calls this and the user re-registers from scratch via the
// existing register flow.
//
// Unlike CancelVaultReset (legitimate-user escape hatch during the 24h
// reset cooldown) and DeclinePending (admin rejecting an unwrapped row),
// this works regardless of the row's pending/granted/cooldown state.
// Returns ErrNotFound if no row exists so callers can surface a clean
// "user has no vault registration" message.
func AdminDeleteVaultUser(ctx context.Context, pool *pgxpool.Pool, tenantID, targetUserID uuid.UUID) error {
	tag, err := pool.Exec(ctx, `
		DELETE FROM app_vault_users
		 WHERE tenant_id = $1 AND user_id = $2
	`, tenantID, targetUserID)
	if err != nil {
		return fmt.Errorf("admin-deleting vault user: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetVaultGrant writes a wrapped_vault_key onto the target user's row,
// granted by granterID. Returns ErrNotFound if the target row doesn't exist.
func SetVaultGrant(ctx context.Context, pool *pgxpool.Pool, tenantID, targetUserID, granterID uuid.UUID, wrapped []byte) error {
	tag, err := pool.Exec(ctx, `
		UPDATE app_vault_users
		   SET wrapped_vault_key = $3,
		       granted_by_user_id = $4,
		       granted_at = now(),
		       reset_pending_until = NULL,
		       updated_at = now()
		 WHERE tenant_id = $1 AND user_id = $2
	`, tenantID, targetUserID, wrapped, granterID)
	if err != nil {
		return fmt.Errorf("setting vault grant: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrVaultUserNotFound
	}
	return nil
}

// RevokeVaultGrant nulls out wrapped_vault_key for the target user. Existing
// browser sessions that have already cached the unwrapped key are not affected
// in v1 (forward secrecy is a v2 item; see plan).
func RevokeVaultGrant(ctx context.Context, pool *pgxpool.Pool, tenantID, targetUserID uuid.UUID) error {
	_, err := pool.Exec(ctx, `
		UPDATE app_vault_users
		   SET wrapped_vault_key = NULL,
		       granted_by_user_id = NULL,
		       granted_at = NULL,
		       updated_at = now()
		 WHERE tenant_id = $1 AND user_id = $2
	`, tenantID, targetUserID)
	if err != nil {
		return fmt.Errorf("revoking vault grant: %w", err)
	}
	return nil
}

// IncrementFailedUnlocks bumps the rate-limit counter and returns the new value.
// Caller decides whether to set locked_until based on the threshold.
func IncrementFailedUnlocks(ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID) (int, error) {
	var n int
	err := pool.QueryRow(ctx, `
		UPDATE app_vault_users
		   SET failed_unlocks = failed_unlocks + 1, updated_at = now()
		 WHERE tenant_id = $1 AND user_id = $2
		RETURNING failed_unlocks
	`, tenantID, userID).Scan(&n)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil // missing row: caller does dummy compare anyway
		}
		return 0, fmt.Errorf("incrementing failed unlocks: %w", err)
	}
	return n, nil
}

// ResetFailedUnlocks zeros the counter on a successful unlock.
func ResetFailedUnlocks(ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID) error {
	_, err := pool.Exec(ctx, `
		UPDATE app_vault_users
		   SET failed_unlocks = 0, locked_until = NULL, updated_at = now()
		 WHERE tenant_id = $1 AND user_id = $2
	`, tenantID, userID)
	if err != nil {
		return fmt.Errorf("resetting failed unlocks: %w", err)
	}
	return nil
}

// SetVaultUserLockedUntil sets the temporary lockout window after the
// failed-unlock threshold is crossed.
func SetVaultUserLockedUntil(ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID, until time.Time) error {
	_, err := pool.Exec(ctx, `
		UPDATE app_vault_users
		   SET locked_until = $3, updated_at = now()
		 WHERE tenant_id = $1 AND user_id = $2
	`, tenantID, userID, until)
	if err != nil {
		return fmt.Errorf("setting locked_until: %w", err)
	}
	return nil
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
		args = append(args, query)
		ftsParam := len(args)
		where += fmt.Sprintf(" AND to_tsvector('english', coalesce(e.title,'')||' '||coalesce(e.url,'')||' '||coalesce(e.username,'')) @@ plainto_tsquery('english', $%d)", ftsParam)
		orderBy = fmt.Sprintf("ts_rank(to_tsvector('english', coalesce(e.title,'')||' '||coalesce(e.url,'')||' '||coalesce(e.username,'')), plainto_tsquery('english', $%d)) DESC, %s", ftsParam, orderBy)
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

// ===== helpers =====

// ErrVaultUserNotFound is returned by SetVaultGrant when the target user
// has no vault_users row (i.e., they haven't registered yet).
var ErrVaultUserNotFound = errors.New("vault user not found")

// ErrNotFound is the canonical "not found / no scope" sentinel returned by
// vault read paths so callers can return uniform 404s.
var ErrNotFound = errors.New("not found")

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
