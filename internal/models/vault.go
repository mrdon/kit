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
type VaultEntry struct {
	ID              uuid.UUID
	TenantID        uuid.UUID
	OwnerUserID     uuid.UUID
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

// VaultEntryScope mirrors the existing skill_scopes / rule_scopes pattern —
// default-deny, scope rows extend visibility from owner-only to named users
// / role members / the whole tenant.
type VaultEntryScope struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	EntryID   uuid.UUID
	ScopeKind string // "user" | "role" | "tenant"
	ScopeID   *uuid.UUID
	CreatedAt time.Time
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

// CreateVaultEntry inserts a new entry plus its scope rows in one transaction.
// scopes may be empty (default-deny → owner-only).
func CreateVaultEntry(ctx context.Context, pool *pgxpool.Pool, e VaultEntry, scopes []VaultEntryScope) (uuid.UUID, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	tags := e.Tags
	if tags == nil {
		tags = []string{}
	}

	var id uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO app_vault_entries
			(tenant_id, owner_user_id, title, username, url, tags,
			 value_ciphertext, value_nonce)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id
	`, e.TenantID, e.OwnerUserID, e.Title, e.Username, e.URL, tags,
		e.ValueCiphertext, e.ValueNonce).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("inserting vault entry: %w", err)
	}

	for _, s := range scopes {
		s.EntryID = id
		s.TenantID = e.TenantID
		if err := insertScopeTx(ctx, tx, s); err != nil {
			return uuid.Nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("commit: %w", err)
	}
	return id, nil
}

// GetVaultEntry returns one entry the caller is authorized to view, or
// (nil, ErrNotFound) on miss-or-no-scope. Returning the same error for
// "doesn't exist" and "no scope" prevents existence enumeration.
func GetVaultEntry(ctx context.Context, pool *pgxpool.Pool, tenantID, entryID, callerID uuid.UUID, callerRoleIDs []uuid.UUID) (*VaultEntry, error) {
	scopeFrag, args := vaultScopeFragment("e", 4, callerID, callerRoleIDs)
	q := fmt.Sprintf(`
		SELECT e.id, e.tenant_id, e.owner_user_id, e.title, e.username, e.url, e.tags,
		       e.value_ciphertext, e.value_nonce, e.created_at, e.updated_at, e.last_viewed_at
		FROM app_vault_entries e
		WHERE e.tenant_id = $1 AND e.id = $2
		  AND (e.owner_user_id = $3 OR EXISTS (%s))
	`, scopeFrag)
	row := pool.QueryRow(ctx, q, append([]any{tenantID, entryID, callerID}, args...)...)

	var e VaultEntry
	if err := row.Scan(
		&e.ID, &e.TenantID, &e.OwnerUserID, &e.Title, &e.Username, &e.URL, &e.Tags,
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
// filtered by FTS query and tag.
func ListVaultEntries(ctx context.Context, pool *pgxpool.Pool, tenantID, callerID uuid.UUID, callerRoleIDs []uuid.UUID, query, tag string, limit int) ([]VaultEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	scopeFrag, scopeArgs := vaultScopeFragment("e", 3, callerID, callerRoleIDs)

	args := []any{tenantID, callerID}
	args = append(args, scopeArgs...)

	where := fmt.Sprintf("e.tenant_id = $1 AND (e.owner_user_id = $2 OR EXISTS (%s))", scopeFrag)
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
	args = append(args, limit)

	q := fmt.Sprintf(`
		SELECT e.id, e.tenant_id, e.owner_user_id, e.title, e.username, e.url, e.tags,
		       e.value_ciphertext, e.value_nonce, e.created_at, e.updated_at, e.last_viewed_at
		FROM app_vault_entries e
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

// UpdateVaultEntry rewrites an entry's mutable fields. owner_user_id and
// tenant_id are intentionally not updatable here — owner transfer would
// require a separate gated endpoint.
func UpdateVaultEntry(ctx context.Context, pool *pgxpool.Pool, tenantID, entryID, callerID uuid.UUID, callerRoleIDs []uuid.UUID, e VaultEntry, scopes []VaultEntryScope) error {
	scopeFrag, scopeArgs := vaultScopeFragment("ent", 4, callerID, callerRoleIDs)

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	tags := e.Tags
	if tags == nil {
		tags = []string{}
	}
	q := fmt.Sprintf(`
		UPDATE app_vault_entries ent
		   SET title = $5, username = $6, url = $7, tags = $8,
		       value_ciphertext = $9, value_nonce = $10, updated_at = now()
		 WHERE ent.tenant_id = $1 AND ent.id = $2
		   AND (ent.owner_user_id = $3 OR EXISTS (%s))
	`, scopeFrag)
	args := []any{tenantID, entryID, callerID}
	args = append(args, scopeArgs...)
	args = append(args, e.Title, e.Username, e.URL, tags, e.ValueCiphertext, e.ValueNonce)

	tag, err := tx.Exec(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("updating vault entry: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}

	if scopes != nil {
		if _, err := tx.Exec(ctx, `DELETE FROM app_vault_entry_scopes WHERE tenant_id = $1 AND entry_id = $2`, tenantID, entryID); err != nil {
			return fmt.Errorf("clearing scopes: %w", err)
		}
		for _, s := range scopes {
			s.EntryID = entryID
			s.TenantID = tenantID
			if err := insertScopeTx(ctx, tx, s); err != nil {
				return err
			}
		}
	}

	return tx.Commit(ctx)
}

// DeleteVaultEntry deletes an entry the caller is authorized to view.
// (v1: anyone with view authz can delete; v2 may want stricter rules.)
func DeleteVaultEntry(ctx context.Context, pool *pgxpool.Pool, tenantID, entryID, callerID uuid.UUID, callerRoleIDs []uuid.UUID) error {
	scopeFrag, scopeArgs := vaultScopeFragment("ent", 4, callerID, callerRoleIDs)
	q := fmt.Sprintf(`
		DELETE FROM app_vault_entries ent
		WHERE ent.tenant_id = $1 AND ent.id = $2
		  AND (ent.owner_user_id = $3 OR EXISTS (%s))
	`, scopeFrag)
	args := []any{tenantID, entryID, callerID}
	args = append(args, scopeArgs...)
	tag, err := pool.Exec(ctx, q, args...)
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

// ListVaultEntryScopes returns all scope rows for the entry. Caller must have
// already authz-checked the parent entry.
func ListVaultEntryScopes(ctx context.Context, pool *pgxpool.Pool, tenantID, entryID uuid.UUID) ([]VaultEntryScope, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, tenant_id, entry_id, scope_kind, scope_id, created_at
		FROM app_vault_entry_scopes
		WHERE tenant_id = $1 AND entry_id = $2
		ORDER BY scope_kind, created_at
	`, tenantID, entryID)
	if err != nil {
		return nil, fmt.Errorf("listing scopes: %w", err)
	}
	defer rows.Close()
	var out []VaultEntryScope
	for rows.Next() {
		var s VaultEntryScope
		if err := rows.Scan(&s.ID, &s.TenantID, &s.EntryID, &s.ScopeKind, &s.ScopeID, &s.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan scope: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ReplaceVaultEntryScopes wipes & rewrites the scope rows for one entry,
// inside a transaction. Caller must have already authz-checked the entry.
func ReplaceVaultEntryScopes(ctx context.Context, pool *pgxpool.Pool, tenantID, entryID uuid.UUID, scopes []VaultEntryScope) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM app_vault_entry_scopes WHERE tenant_id = $1 AND entry_id = $2`, tenantID, entryID); err != nil {
		return fmt.Errorf("clearing scopes: %w", err)
	}
	for _, s := range scopes {
		s.EntryID = entryID
		s.TenantID = tenantID
		if err := insertScopeTx(ctx, tx, s); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ===== authz scope filter =====

// vaultScopeFragment builds the EXISTS-subquery body that matches an entry's
// scope rows against the caller's principals (their user_id and role_ids).
// alias is the parent table alias (e.g. "e" for app_vault_entries e); the
// subquery joins app_vault_entry_scopes s ON s.entry_id = alias.id.
//
// startParam is the next $N parameter index after the caller's tenant_id and
// caller user_id are already bound. The returned args list provides the
// caller user_id (for scope_kind='user' match) and role_ids (for
// scope_kind='role' match) in order.
func vaultScopeFragment(alias string, startParam int, callerID uuid.UUID, callerRoleIDs []uuid.UUID) (string, []any) {
	userParam := startParam
	rolesParam := startParam + 1
	frag := fmt.Sprintf(`
		SELECT 1 FROM app_vault_entry_scopes s
		 WHERE s.tenant_id = %s.tenant_id
		   AND s.entry_id  = %s.id
		   AND ( s.scope_kind = 'tenant'
			  OR (s.scope_kind = 'user' AND s.scope_id = $%d)
			  OR (s.scope_kind = 'role' AND s.scope_id = ANY($%d))
			)
	`, alias, alias, userParam, rolesParam)

	roleIDs := callerRoleIDs
	if roleIDs == nil {
		roleIDs = []uuid.UUID{}
	}
	return frag, []any{callerID, roleIDs}
}

// ===== helpers =====

// ErrVaultUserNotFound is returned by SetVaultGrant when the target user
// has no vault_users row (i.e., they haven't registered yet).
var ErrVaultUserNotFound = errors.New("vault user not found")

// ErrNotFound is the canonical "not found / no scope" sentinel returned by
// vault read paths so callers can return uniform 404s.
var ErrNotFound = errors.New("not found")

func insertScopeTx(ctx context.Context, tx pgx.Tx, s VaultEntryScope) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO app_vault_entry_scopes (tenant_id, entry_id, scope_kind, scope_id)
		VALUES ($1, $2, $3, $4)
	`, s.TenantID, s.EntryID, s.ScopeKind, s.ScopeID)
	if err != nil {
		return fmt.Errorf("inserting scope: %w", err)
	}
	return nil
}

func scanVaultEntries(rows pgx.Rows) ([]VaultEntry, error) {
	var out []VaultEntry
	for rows.Next() {
		var e VaultEntry
		if err := rows.Scan(
			&e.ID, &e.TenantID, &e.OwnerUserID, &e.Title, &e.Username, &e.URL, &e.Tags,
			&e.ValueCiphertext, &e.ValueNonce, &e.CreatedAt, &e.UpdatedAt, &e.LastViewedAt,
		); err != nil {
			return nil, fmt.Errorf("scan vault entry: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
