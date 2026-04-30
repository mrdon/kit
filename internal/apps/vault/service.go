package vault

import (
	"context"
	cryptorand "crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
)

// Service is the vault's behavior layer. Tools (agent + MCP) and HTTP
// handlers both go through Service so authz, audit, and rate-limiting
// happen in one place.
type Service struct {
	pool *pgxpool.Pool

	// rateLimit is a single-process sliding-window limiter on
	// /api/vault/unlock attempts, keyed by client IP. Replace with
	// Redis/Postgres if Kit ever multi-hosts; sync.Map of token buckets
	// is fine for v1.
	rateLimit unlockLimiter

	// cards is the vault's card-creation surface, populated via the
	// package-level Configure func from main.go. nil-safe: cards just
	// don't fire when the service isn't wired (e.g., in tests).
	cards CardSurface

	// notify is the user-facing DM surface for security tripwires
	// (failed unlock, reset triggered, access granted). nil-safe.
	notify NotifySurface
}

// NewService constructs a vault service backed by the given pool.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{
		pool:      pool,
		rateLimit: newUnlockLimiter(perIPCapacity, perIPRefillInterval),
	}
}

// ===== read paths =====

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
		return nil, nil
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

	// Best-effort touch + audit; do not fail the read.
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

// ===== write paths =====

// CreateEntryParams is the input shape for creating a new vault entry. The
// caller is the implicit owner. value_ciphertext + value_nonce are produced
// in the browser and never decrypted server-side.
type CreateEntryParams struct {
	Title           string
	Username        string
	URL             string
	Tags            []string
	ValueCiphertext []byte
	ValueNonce      []byte
	Scopes          []models.VaultEntryScope // empty = owner-only (default-deny)
}

// CreateEntry inserts a new entry plus its scope rows in one transaction.
func (s *Service) CreateEntry(ctx context.Context, c *services.Caller, p CreateEntryParams, audit auditCtx) (uuid.UUID, error) {
	if err := validateCiphertext(p.ValueCiphertext, p.ValueNonce); err != nil {
		return uuid.Nil, err
	}
	if err := validateScopes(p.Scopes); err != nil {
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

// UpdateEntryParams mirrors CreateEntryParams plus the entry id. Scopes
// nil means "leave as-is"; non-nil (even empty) means "replace with this".
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

// UpdateEntry rewrites an entry the caller is authorized to view.
func (s *Service) UpdateEntry(ctx context.Context, c *services.Caller, entryID uuid.UUID, p UpdateEntryParams, audit auditCtx) error {
	if err := validateCiphertext(p.ValueCiphertext, p.ValueNonce); err != nil {
		return err
	}
	if p.UpdateScopes {
		if err := validateScopes(p.Scopes); err != nil {
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

// UpdateScopes is the metadata-only "who can see this" change exposed as a
// gated agent/MCP tool. The diff against the existing scope set is logged
// to audit_events so widening operations leave a trail.
func (s *Service) UpdateScopes(ctx context.Context, c *services.Caller, entryID uuid.UUID, scopes []models.VaultEntryScope, audit auditCtx) error {
	// Authz: the caller must already see this entry (owner or scoped).
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

	prev, err := models.ListVaultEntryScopes(ctx, s.pool, c.TenantID, entryID)
	if err != nil {
		return err
	}
	added, removed := scopeDiff(prev, scopes)

	// Step-up auth on widening: any addition (whether tenant, role, or
	// user) requires a recent unlock. Pure narrowing (only removals)
	// runs direct.
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

// ===== unlock + grant =====

// UnlockResult is the response shape for a successful unlock call.
type UnlockResult struct {
	KDFParams                json.RawMessage `json:"kdf_params"`
	UserPrivateKeyCiphertext []byte          `json:"user_private_key_ciphertext"`
	UserPrivateKeyNonce      []byte          `json:"user_private_key_nonce"`
	WrappedVaultKey          []byte          `json:"wrapped_vault_key"`
}

// ErrUnlockMismatch is the uniform error returned for unlock miss, bad
// auth_hash, or no vault_users row — keeps callers from inferring which.
var ErrUnlockMismatch = errors.New("unlock failed")

// ErrUnlockLocked is returned when locked_until is in the future.
var ErrUnlockLocked = errors.New("unlock locked")

// ErrUnlockNotGranted is returned when the user has registered but not yet
// been granted access (wrapped_vault_key is NULL).
var ErrUnlockNotGranted = errors.New("not granted")

// ErrUnlockPending is returned when the user's row is pending=true (still
// in the self-unlock-test phase). They should retry the register flow.
var ErrUnlockPending = errors.New("registration pending")

// ErrStepUpRequired is returned by sensitive operations (grant,
// scope-widening) when the caller hasn't unlocked their vault recently
// enough. Plan §"Step-up auth on sensitive ops": 5-minute window.
var ErrStepUpRequired = errors.New("recent unlock required")

// stepUpWindow is how recently the caller must have completed a
// successful unlock before a sensitive operation is allowed.
const stepUpWindow = 5 * time.Minute

// Unlock validates auth_hash against the caller's vault_users row.
// Constant-time comparison is used; on miss, a dummy comparison is run
// against random bytes so the response timing doesn't leak row-existence.
// Rate-limit is enforced before the DB hit.
func (s *Service) Unlock(ctx context.Context, c *services.Caller, authHash []byte, audit auditCtx) (*UnlockResult, error) {
	if audit.ip != nil && !s.rateLimit.allow(audit.ip.String()) {
		return nil, ErrUnlockLocked
	}

	v, err := models.GetVaultUser(ctx, s.pool, c.TenantID, c.UserID)
	if err != nil {
		return nil, err
	}

	if v == nil {
		// No row: do a dummy compare so timing matches the "wrong
		// auth_hash" branch.
		_ = subtle.ConstantTimeCompare(authHash, dummyHash())
		audit.log(ctx, "vault.unlock_failed", "vault_user", &c.UserID, EvtUnlockFailed{FailedCount: 0})
		return nil, ErrUnlockMismatch
	}

	if v.LockedUntil != nil && time.Now().Before(*v.LockedUntil) {
		audit.log(ctx, "vault.unlock_failed", "vault_user", &c.UserID, EvtUnlockFailed{FailedCount: v.FailedUnlocks, Locked: true})
		return nil, ErrUnlockLocked
	}

	if subtle.ConstantTimeCompare(authHash, v.AuthHash) != 1 {
		count, _ := models.IncrementFailedUnlocks(ctx, s.pool, c.TenantID, c.UserID)
		locked := false
		var lockoutDuration time.Duration
		switch {
		case count >= unlockHardLockoutThreshold:
			// Sustained attack: 24h lockout. Plan calls for forcing
			// re-OAuth via Slack here too — for v1 the long lockout is
			// the enforced bound; admins can clear it via the DB.
			lockoutDuration = unlockHardLockoutDuration
			locked = true
		case count >= unlockLockoutThreshold:
			lockoutDuration = unlockLockoutDuration
			locked = true
		}
		if locked {
			_ = models.SetVaultUserLockedUntil(ctx, s.pool, c.TenantID, c.UserID, time.Now().Add(lockoutDuration))
			s.notifyUser(ctx, c.TenantID, c.UserID,
				fmt.Sprintf(":lock: %d failed unlock attempts on your Kit vault — was this you? "+
					"Your vault is locked for %s. If this wasn't you, your account may be compromised; "+
					"rotate your Slack credentials and re-OAuth.", count, lockoutDuration),
			)
		}
		audit.log(ctx, "vault.unlock_failed", "vault_user", &c.UserID, EvtUnlockFailed{FailedCount: count, Locked: locked})
		return nil, ErrUnlockMismatch
	}

	if v.Pending {
		// Browser hasn't passed the self-unlock canary yet — refuse to
		// hand out wrapped keys until the row is active.
		return nil, ErrUnlockPending
	}
	if v.WrappedVaultKey == nil {
		return nil, ErrUnlockNotGranted
	}

	if err := models.ResetFailedUnlocks(ctx, s.pool, c.TenantID, c.UserID); err != nil {
		slog.Warn("vault: resetting failed_unlocks", "error", err)
	}
	audit.log(ctx, "vault.unlock", "vault_user", &c.UserID, EvtUnlock{})

	return &UnlockResult{
		KDFParams:                v.KDFParams,
		UserPrivateKeyCiphertext: v.UserPrivateKeyCiphertext,
		UserPrivateKeyNonce:      v.UserPrivateKeyNonce,
		WrappedVaultKey:          v.WrappedVaultKey,
	}, nil
}

// RegisterParams is the input to /api/vault/register. WrappedVaultKey is
// non-nil only for the very first user in a tenant (self-grant); for
// everyone else the server enforces it stays NULL until a teammate grants.
type RegisterParams struct {
	KDFParams                json.RawMessage
	AuthHash                 []byte
	UserPublicKey            []byte
	UserPrivateKeyCiphertext []byte
	UserPrivateKeyNonce      []byte
	WrappedVaultKey          []byte
	Replace                  bool // master-password reset path
}

// Register creates or replaces the caller's vault_users row.
//
// Bootstrap rules:
//   - When no vault_users row exists in the tenant, the caller MUST be admin
//     and MUST supply a non-nil WrappedVaultKey (self-issued tenant init).
//   - Otherwise, WrappedVaultKey MUST be nil and a teammate will grant later.
//
// All new rows start pending=true; the browser flips it via SelfUnlockTest
// after a successful round-trip.
func (s *Service) Register(ctx context.Context, c *services.Caller, p RegisterParams, audit auditCtx) error {
	// Pubkey validation (defense against attacker-supplied keys).
	if err := validateRSAPubKey(p.UserPublicKey); err != nil {
		return fmt.Errorf("invalid public key: %w", err)
	}
	if len(p.AuthHash) != 32 {
		return errors.New("auth_hash must be 32 bytes")
	}
	if len(p.KDFParams) == 0 {
		return errors.New("kdf_params required")
	}
	if len(p.UserPrivateKeyCiphertext) == 0 || len(p.UserPrivateKeyNonce) != 12 {
		return errors.New("private key ciphertext / nonce required (12-byte nonce)")
	}

	tenantInitialized, err := models.AnyVaultUserExists(ctx, s.pool, c.TenantID)
	if err != nil {
		return err
	}
	isInitiator := !tenantInitialized && !p.Replace
	if isInitiator {
		if !c.IsAdmin {
			return errors.New("only tenant admins can initialize the vault")
		}
		if p.WrappedVaultKey == nil {
			return errors.New("first user in tenant must self-grant")
		}
	} else if p.WrappedVaultKey != nil {
		return errors.New("only the initial admin may pre-set wrapped_vault_key; teammates wait for a grant")
	}

	// On replace, refuse if a reset is already in flight, and capture
	// the prior pubkey *before* the write so the audit row can record
	// the diff (otherwise we read back the new key we just wrote).
	priorFP := ""
	if p.Replace {
		existing, err := models.GetVaultUser(ctx, s.pool, c.TenantID, c.UserID)
		if err != nil {
			return err
		}
		if existing == nil {
			return errors.New("no vault user to replace")
		}
		if existing.ResetPendingUntil != nil && time.Now().Before(*existing.ResetPendingUntil) {
			return errors.New("a reset is already in flight; cancel via your DM first")
		}
		priorFP = pubkeyFingerprint(existing.UserPublicKey)
	}

	if err := models.RegisterVaultUser(ctx, s.pool, models.VaultRegisterParams{
		TenantID:                 c.TenantID,
		UserID:                   c.UserID,
		KDFParams:                p.KDFParams,
		AuthHash:                 p.AuthHash,
		UserPublicKey:            p.UserPublicKey,
		UserPrivateKeyCiphertext: p.UserPrivateKeyCiphertext,
		UserPrivateKeyNonce:      p.UserPrivateKeyNonce,
		WrappedVaultKey:          p.WrappedVaultKey,
		Replace:                  p.Replace,
	}); err != nil {
		return err
	}

	newFP := pubkeyFingerprint(p.UserPublicKey)
	if p.Replace {
		audit.log(ctx, "vault.master_password_reset", "vault_user", &c.UserID, EvtMasterPasswordReset{
			OldPubKeyFingerprint: priorFP,
			NewPubKeyFingerprint: newFP,
		})
		s.notifyUser(ctx, c.TenantID, c.UserID,
			":warning: Your Kit vault password was reset. If this wasn't you, your Slack account "+
				"may be compromised — rotate your Slack credentials immediately. Until you do, do not "+
				"approve any incoming grant request for your account.",
		)
	} else {
		audit.log(ctx, "vault.register", "vault_user", &c.UserID, EvtRegister{
			Replace:           false,
			IsTenantInitiator: isInitiator,
			PubKeyFingerprint: newFP,
		})
	}

	// Fire an admin-targeted decision card unless this is the bootstrap
	// initiator (who has self-granted; nobody else needs to act).
	// Best-effort: card-creation failure does not roll back the registration.
	if !isInitiator {
		if err := s.fireGrantRequestCard(ctx, c, p.Replace, newFP); err != nil {
			slog.Warn("vault: firing grant-request decision card failed", "error", err)
		}
	}
	return nil
}

// fireGrantRequestCard creates an admin-scoped decision card asking a
// teammate to grant (or re-grant after reset) vault access to the caller.
// Body includes the user's display name + Slack handle + the fingerprint
// for out-of-band verification.
func (s *Service) fireGrantRequestCard(ctx context.Context, target *services.Caller, isReset bool, fingerprint string) error {
	if s.cards == nil {
		return nil // cards surface not wired; no-op (e.g. tests)
	}
	title := "Grant vault access"
	if isReset {
		title = "Re-grant vault access (password reset)"
	}

	// Best-effort enrichment: pull the target's display name from the
	// users table. Falls back to the bare slack handle / id if missing.
	user, _ := models.GetUserByID(ctx, s.pool, target.TenantID, target.UserID)
	body := buildGrantCardBody(target, user, isReset, fingerprint)

	return s.cards.CreateDecision(ctx, target, CardCreateInput{
		Title:      title,
		Body:       body,
		RoleScopes: []string{"admin"},
		Decision: &CardDecisionCreateInput{
			Priority:            "high",
			RecommendedOptionID: "open_grant_page",
			Options: []CardDecisionOption{
				{
					OptionID: "open_grant_page",
					Label:    "Review and grant",
					// No tool — the card body links the admin to
					// /vault/grant/<user_id> for the actual wrap.
				},
				{
					OptionID: "decline",
					Label:    "Decline",
				},
			},
		},
	})
}

func buildGrantCardBody(target *services.Caller, user *models.User, isReset bool, fingerprint string) string {
	displayName := target.Identity
	slackID := target.Identity
	if user != nil {
		slackID = user.SlackUserID
		if user.DisplayName != nil && *user.DisplayName != "" {
			displayName = *user.DisplayName
		}
	}

	var b strings.Builder
	if isReset {
		fmt.Fprintf(&b, "**%s** (<@%s>) reset their vault password. Their public-key fingerprint has changed.\n\n", displayName, slackID)
	} else {
		fmt.Fprintf(&b, "**%s** (<@%s>) registered for the vault and needs access.\n\n", displayName, slackID)
	}
	b.WriteString("Public-key fingerprint:\n\n```\n")
	b.WriteString(fingerprint)
	b.WriteString("\n```\n\n")
	b.WriteString("**Verify this fingerprint with them out-of-band** (e.g. ask them to read it back over a separate channel) before granting. ")
	b.WriteString("Open the grant page to complete the action.")
	return b.String()
}

// SelfUnlockTest flips pending=false after the browser has demonstrated the
// keys round-trip cleanly. Called immediately after Register, with the
// auth_hash the browser just derived. Constant-time comparison.
func (s *Service) SelfUnlockTest(ctx context.Context, c *services.Caller, authHash []byte) error {
	v, err := models.GetVaultUser(ctx, s.pool, c.TenantID, c.UserID)
	if err != nil {
		return err
	}
	if v == nil {
		return ErrUnlockMismatch
	}
	if subtle.ConstantTimeCompare(authHash, v.AuthHash) != 1 {
		return ErrUnlockMismatch
	}
	return models.MarkVaultUserActive(ctx, s.pool, c.TenantID, c.UserID)
}

// GrantParams is the input to /api/vault/grants/<target>. The granter (caller)
// must already be a vault member and have unlocked recently (step-up auth
// is enforced by the HTTP handler, not the service).
type GrantParams struct {
	TargetUserID    uuid.UUID
	WrappedVaultKey []byte // RSA-OAEP(vault_key, target's user_public_key)
}

// Grant writes a wrapped_vault_key onto the target user's row. Re-validates
// the target's stored pubkey before accepting the wrap. Step-up auth: the
// caller must have unlocked within stepUpWindow.
func (s *Service) Grant(ctx context.Context, c *services.Caller, p GrantParams, audit auditCtx) error {
	if err := s.requireRecentUnlock(ctx, c); err != nil {
		return err
	}
	if len(p.WrappedVaultKey) == 0 {
		return errors.New("wrapped_vault_key required")
	}
	target, err := models.GetVaultUser(ctx, s.pool, c.TenantID, p.TargetUserID)
	if err != nil {
		return err
	}
	if target == nil {
		return models.ErrVaultUserNotFound
	}
	if err := validateRSAPubKey(target.UserPublicKey); err != nil {
		return fmt.Errorf("target public key invalid: %w", err)
	}

	duringCooldown := target.ResetPendingUntil != nil && time.Now().Before(*target.ResetPendingUntil)

	if err := models.SetVaultGrant(ctx, s.pool, c.TenantID, p.TargetUserID, c.UserID, p.WrappedVaultKey); err != nil {
		return err
	}
	audit.log(ctx, "vault.grant", "vault_user", &p.TargetUserID, EvtGrant{
		TargetUserID:            p.TargetUserID,
		TargetPubKeyFingerprint: pubkeyFingerprint(target.UserPublicKey),
		DuringResetCooldown:     duringCooldown,
	})
	s.notifyUser(ctx, c.TenantID, p.TargetUserID,
		":unlock: Your Kit vault access is now active. Open the vault to add or look up secrets.",
	)
	return nil
}

// notifyUser sends a single-line Slack DM to the user, best-effort. nil
// notify surface = no-op (tests, misconfigured startup). Errors are
// logged but never propagated.
func (s *Service) notifyUser(ctx context.Context, tenantID, userID uuid.UUID, body string) {
	if s.notify == nil {
		return
	}
	if err := s.notify.NotifyUser(ctx, tenantID, userID, body); err != nil {
		slog.Warn("vault: notifying user failed", "user_id", userID, "error", err)
	}
}

// RevokeGrant nulls the target user's wrapped_vault_key. Forward secrecy
// is a v2 item — existing browser caches are unaffected.
func (s *Service) RevokeGrant(ctx context.Context, c *services.Caller, targetUserID uuid.UUID, audit auditCtx) error {
	if err := models.RevokeVaultGrant(ctx, s.pool, c.TenantID, targetUserID); err != nil {
		return err
	}
	audit.log(ctx, "vault.revoke_grant", "vault_user", &targetUserID, EvtRevokeGrant{TargetUserID: targetUserID})
	return nil
}

// ===== helpers =====

const (
	// unlockLockoutThreshold is the failed-unlock count that triggers a
	// 15-minute lockout. Counter resets on a successful unlock.
	unlockLockoutThreshold = 5

	// unlockLockoutDuration is the lockout after crossing the soft
	// threshold (plan: 15 min).
	unlockLockoutDuration = 15 * time.Minute

	// unlockHardLockoutThreshold is the cumulative failure count that
	// promotes a soft lockout into a 24-hour lockout (plan §"Unlock
	// attack surface": "Sustained attacks → locked_until = now() + 24h").
	unlockHardLockoutThreshold = 20

	// unlockHardLockoutDuration is the long lockout after sustained
	// brute-force attempts.
	unlockHardLockoutDuration = 24 * time.Hour

	// perIPCapacity / perIPRefillInterval define the in-process token
	// bucket on /api/vault/unlock per remote IP. Plan: secondary
	// throttle to the per-user check.
	perIPCapacity       = 20
	perIPRefillInterval = time.Minute
)

// validateRSAPubKey enforces RSA-2048 with e=65537 (defends against
// downgrade / e=1 / non-RSA / malformed DER attacks at registration).
func validateRSAPubKey(der []byte) error {
	if len(der) == 0 {
		return errors.New("empty public key")
	}
	pub, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return errors.New("not an RSA public key")
	}
	if rsaPub.N == nil || rsaPub.N.BitLen() != 2048 {
		return fmt.Errorf("modulus must be 2048 bits, got %d", rsaPub.N.BitLen())
	}
	if rsaPub.E != 65537 {
		return fmt.Errorf("public exponent must be 65537, got %d", rsaPub.E)
	}
	return nil
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func toListItem(e models.VaultEntry, c *services.Caller) EntryListItem {
	username := ""
	if e.Username != nil {
		username = *e.Username
	}
	url := ""
	if e.URL != nil {
		url = *e.URL
	}
	return EntryListItem{
		ID:           e.ID,
		Title:        e.Title,
		Username:     username,
		URL:          url,
		Tags:         e.Tags,
		LastViewedAt: e.LastViewedAt,
		IsOwner:      e.OwnerUserID == c.UserID,
	}
}

func validateCiphertext(ct, nonce []byte) error {
	if len(ct) == 0 {
		return errors.New("value_ciphertext required")
	}
	if len(nonce) != 12 {
		return errors.New("value_nonce must be 12 bytes (AES-GCM)")
	}
	return nil
}

func validateScopes(scopes []models.VaultEntryScope) error {
	seen := make(map[string]bool, len(scopes))
	for i, s := range scopes {
		switch s.ScopeKind {
		case "tenant":
			if s.ScopeID != nil {
				return fmt.Errorf("scope[%d]: tenant kind must not have scope_id", i)
			}
		case "user", "role":
			if s.ScopeID == nil {
				return fmt.Errorf("scope[%d]: %s kind requires scope_id", i, s.ScopeKind)
			}
		default:
			return fmt.Errorf("scope[%d]: unknown kind %q", i, s.ScopeKind)
		}
		key := scopeKey(s)
		if seen[key] {
			return fmt.Errorf("scope[%d]: duplicate %s", i, key)
		}
		seen[key] = true
	}
	return nil
}

func scopeKey(s models.VaultEntryScope) string {
	if s.ScopeID == nil {
		return s.ScopeKind + ":-"
	}
	return s.ScopeKind + ":" + s.ScopeID.String()
}

// scopeDiff compares two scope-row sets and returns the (added, removed)
// lists of (kind, id) pairs. Output is sorted by (kind, id) so audit
// rows are deterministic across runs (Go map iteration is randomized).
func scopeDiff(before, after []models.VaultEntryScope) (added, removed []ScopeRef) {
	prev := map[string]models.VaultEntryScope{}
	for _, s := range before {
		prev[scopeKey(s)] = s
	}
	now := map[string]models.VaultEntryScope{}
	for _, s := range after {
		now[scopeKey(s)] = s
	}
	for k, s := range now {
		if _, ok := prev[k]; !ok {
			added = append(added, ScopeRef{Kind: s.ScopeKind, ID: s.ScopeID})
		}
	}
	for k, s := range prev {
		if _, ok := now[k]; !ok {
			removed = append(removed, ScopeRef{Kind: s.ScopeKind, ID: s.ScopeID})
		}
	}
	sortScopeRefs(added)
	sortScopeRefs(removed)
	return added, removed
}

func sortScopeRefs(refs []ScopeRef) {
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Kind != refs[j].Kind {
			return refs[i].Kind < refs[j].Kind
		}
		var li, lj string
		if refs[i].ID != nil {
			li = refs[i].ID.String()
		}
		if refs[j].ID != nil {
			lj = refs[j].ID.String()
		}
		return li < lj
	})
}

// pubkeyFingerprint returns a Signal-style fingerprint of a public key:
// 24 hex characters in 6 groups of 4 (XXXX XXXX XXXX XXXX XXXX XXXX).
// The format is chosen for ease of out-of-band verification — short
// enough to read aloud, long enough that an attacker can't brute-force a
// collision under SHA-256.
func pubkeyFingerprint(pub []byte) string {
	if len(pub) == 0 {
		return ""
	}
	sum := sha256.Sum256(pub)
	hexStr := hex.EncodeToString(sum[:12])
	// Group as XXXX XXXX XXXX XXXX XXXX XXXX (24 → 6 × 4).
	var b strings.Builder
	for i := 0; i < len(hexStr); i += 4 {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(hexStr[i : i+4])
	}
	return b.String()
}

// dummyHash returns 32 random bytes for the constant-time miss path.
// Fresh per call so the comparison can't be distinguished from a real
// auth_hash compare via any side channel — only the timing matters.
func dummyHash() []byte {
	b := make([]byte, 32)
	// crypto/rand failures here would be catastrophic for the host; if
	// they happen, the all-zeros buffer is a safe fallback because the
	// caller compares against an attacker-supplied 32 bytes either way.
	_, _ = cryptorand.Read(b)
	return b
}

// ===== unlock rate limiter =====

type unlockLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*tokenBucket
	capacity int
	refill   time.Duration
}

type tokenBucket struct {
	tokens  int
	updated time.Time
}

func newUnlockLimiter(capacity int, refill time.Duration) unlockLimiter {
	return unlockLimiter{
		buckets:  make(map[string]*tokenBucket),
		capacity: capacity,
		refill:   refill,
	}
}

func (u *unlockLimiter) allow(key string) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	now := time.Now()
	b, ok := u.buckets[key]
	if !ok {
		b = &tokenBucket{tokens: u.capacity, updated: now}
		// Cap memory: evict older entries if the map gets too big.
		if len(u.buckets) > 10_000 {
			for k := range u.buckets {
				delete(u.buckets, k)
				if len(u.buckets) <= 5_000 {
					break
				}
			}
		}
		u.buckets[key] = b
	}
	// Refill: gain ceil((elapsed/refill) * capacity) tokens.
	elapsed := now.Sub(b.updated)
	gain := int(elapsed/u.refill) * u.capacity
	if gain > 0 {
		b.tokens += gain
		if b.tokens > u.capacity {
			b.tokens = u.capacity
		}
		b.updated = now
	}
	if b.tokens <= 0 {
		return false
	}
	b.tokens--
	return true
}

// ===== misc HTTP wiring helpers (re-exported for handlers) =====

// requireRecentUnlock enforces step-up auth: the caller must have a
// successful vault.unlock audit event within the last stepUpWindow.
// Returns ErrStepUpRequired if not. Implemented as an audit_events
// query so the unlock record is the source of truth — no extra column
// to keep in sync.
func (s *Service) requireRecentUnlock(ctx context.Context, c *services.Caller) error {
	var lastUnlock time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT created_at FROM audit_events
		WHERE tenant_id = $1 AND actor_user_id = $2 AND action = 'vault.unlock'
		ORDER BY created_at DESC
		LIMIT 1
	`, c.TenantID, c.UserID).Scan(&lastUnlock)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrStepUpRequired
		}
		return fmt.Errorf("step-up lookup: %w", err)
	}
	if time.Since(lastUnlock) > stepUpWindow {
		return ErrStepUpRequired
	}
	return nil
}

// tenantSlug looks up a tenant's URL slug by id. Used by the MCP layer to
// build reveal/add URLs since Caller doesn't carry the slug. Returns
// ("", err) on miss; ("", nil) is impossible (an existing tenant always
// has a slug).
func (s *Service) tenantSlug(ctx context.Context, tenantID uuid.UUID) (string, error) {
	t, err := models.GetTenantByID(ctx, s.pool, tenantID)
	if err != nil {
		return "", err
	}
	if t == nil {
		return "", errors.New("tenant not found")
	}
	return t.Slug, nil
}

// AuditFromRequest constructs the audit context for an HTTP-driven action.
// Convenience wrapper around newAuditCtx.
func (s *Service) AuditFromRequest(c *services.Caller, r *http.Request) auditCtx {
	var actor *uuid.UUID
	if c != nil {
		id := c.UserID
		actor = &id
	}
	tenantID := uuid.Nil
	if c != nil {
		tenantID = c.TenantID
	}
	return newAuditCtx(s.pool, tenantID, actor, r)
}
