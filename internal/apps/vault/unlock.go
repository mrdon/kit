package vault

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
)

// UnlockResult is the response shape for a successful unlock call. The
// browser uses these to derive enc_key (from kdf_params + the password
// it just typed) and then AES-GCM-unwrap the wrapped vault key using
// the AAD that's pinned to the tenant_id.
type UnlockResult struct {
	KDFParams            json.RawMessage `json:"kdf_params"`
	WrappedVaultKey      []byte          `json:"wrapped_vault_key"`
	WrappedVaultKeyNonce []byte          `json:"wrapped_vault_key_nonce"`
	VaultGeneration      int             `json:"vault_generation"`
	// TenantIDBytes is the raw 16 bytes of the tenant UUID, hex-encoded
	// for JSON transport. The browser decodes it to a Uint8Array and
	// passes it as the AAD to AES-GCM-decrypt. Pinning the AAD to the
	// tenant id prevents a leaked wrapped_vault_key row from being
	// replayed against a different tenant.
	TenantIDBytes string `json:"tenant_id_bytes"`
}

// ErrUnlockMismatch is the uniform error returned for unlock miss, bad
// auth_hash, or no vault row — keeps callers from inferring which.
var ErrUnlockMismatch = errors.New("unlock failed")

// ErrUnlockLocked is returned when the per-IP rate limiter has emptied
// its bucket OR the tenant row's locked_until is in the future.
var ErrUnlockLocked = errors.New("unlock locked")

// ErrUnlockNotSetUp is returned when the tenant has no vault row yet.
// Callers should redirect to /setup.
var ErrUnlockNotSetUp = errors.New("vault not set up")

// ErrStepUpRequired is returned by sensitive operations when the caller
// hasn't unlocked their vault recently enough.
var ErrStepUpRequired = errors.New("recent unlock required")

const (
	// stepUpWindow is how recently the caller must have unlocked before
	// a sensitive operation is allowed.
	stepUpWindow = 5 * time.Minute

	// perIPCapacity / perIPRefillInterval define the per-IP throttle on
	// /api/vault/unlock. The whole rate-limit story now lives here —
	// there is no per-tenant counter, by design (see plan: a per-tenant
	// counter creates a self-DoS griefing vector for ex-employees who
	// know the old password).
	perIPCapacity       = 20
	perIPRefillInterval = time.Minute
)

// Unlock validates auth_hash against the tenant's vault row.
// Constant-time comparison; on miss, a dummy comparison runs so timing
// doesn't leak whether the tenant has a vault. Rate-limit is enforced
// before the DB hit.
func (s *Service) Unlock(ctx context.Context, c *services.Caller, authHash []byte, audit auditCtx) (*UnlockResult, error) {
	if audit.ip != nil && !s.rateLimit.allow(audit.ip.String()) {
		s.fireFailedUnlockDecision(ctx, c, "rate limited", time.Minute)
		audit.log(ctx, "vault.unlock_failed", "vault_tenant", nil, EvtUnlockFailed{RateLimited: true})
		return nil, ErrUnlockLocked
	}

	v, err := models.GetVaultTenant(ctx, s.pool, c.TenantID)
	if err != nil {
		return nil, err
	}

	if v == nil {
		// No tenant row: dummy compare for timing parity.
		_ = subtle.ConstantTimeCompare(authHash, dummyHash())
		audit.log(ctx, "vault.unlock_failed", "vault_tenant", nil, EvtUnlockFailed{NotSetUp: true})
		return nil, ErrUnlockNotSetUp
	}

	if v.LockedUntil != nil && time.Now().Before(*v.LockedUntil) {
		audit.log(ctx, "vault.unlock_failed", "vault_tenant", nil, EvtUnlockFailed{Locked: true})
		return nil, ErrUnlockLocked
	}

	if subtle.ConstantTimeCompare(authHash, v.AuthHash) != 1 {
		audit.log(ctx, "vault.unlock_failed", "vault_tenant", nil, EvtUnlockFailed{})
		return nil, ErrUnlockMismatch
	}

	// vault.unlock is fail-closed: requireRecentUnlock queries this row
	// shortly after to authorize step-up operations.
	if err := audit.logRequired(ctx, "vault.unlock", "vault_tenant", nil, EvtUnlock{}); err != nil {
		return nil, fmt.Errorf("recording unlock: %w", err)
	}

	return &UnlockResult{
		KDFParams:            v.KDFParams,
		WrappedVaultKey:      v.WrappedVaultKey,
		WrappedVaultKeyNonce: v.WrappedVaultKeyNonce,
		VaultGeneration:      v.VaultGeneration,
		TenantIDBytes:        encodeTenantIDForAAD(c.TenantID),
	}, nil
}

// SetupParams is the input to /api/vault/setup. Admin-only; refuses if
// the tenant already has a vault row.
type SetupParams struct {
	KDFParams            json.RawMessage
	AuthHash             []byte
	WrappedVaultKey      []byte
	WrappedVaultKeyNonce []byte
}

// SetupVault writes the tenant's vault row for the first time. Admin-only.
// The browser has already generated vault_key, derived enc_key/auth_hash
// from the typed password, wrapped vault_key with AAD = tenant_id, and
// done a local round-trip sanity check. The server's job is to validate
// the inputs and persist them.
func (s *Service) SetupVault(ctx context.Context, c *services.Caller, p SetupParams, audit auditCtx) error {
	if !c.IsAdmin {
		return services.ErrForbidden
	}
	if err := validateKDFParams(p.KDFParams); err != nil {
		return fmt.Errorf("invalid kdf_params: %w", err)
	}
	if len(p.AuthHash) != 32 {
		return errors.New("auth_hash must be 32 bytes")
	}
	if err := validateWrappedVaultKey(p.WrappedVaultKey, p.WrappedVaultKeyNonce); err != nil {
		return err
	}

	if err := models.InitVaultTenant(ctx, s.pool, models.VaultSetupParams{
		TenantID:             c.TenantID,
		KDFParams:            p.KDFParams,
		AuthHash:             p.AuthHash,
		WrappedVaultKey:      p.WrappedVaultKey,
		WrappedVaultKeyNonce: p.WrappedVaultKeyNonce,
		SetupByUserID:        c.UserID,
	}); err != nil {
		return err
	}
	audit.log(ctx, "vault.setup", "vault_tenant", nil, EvtSetup{})
	return nil
}

// RotateParams is the input to /api/vault/rotate. Admin-only; requires
// the browser to have unlocked locally with the old password first
// (auth proof = the caller's recent vault.unlock audit row, enforced via
// requireRecentUnlock; the same wrapped_vault_key is re-wrapped under
// the new derivation, so unrolling old → new happens entirely browser-side).
type RotateParams struct {
	KDFParams            json.RawMessage
	AuthHash             []byte
	WrappedVaultKey      []byte
	WrappedVaultKeyNonce []byte
}

// RotateVaultPassword updates the tenant's vault row with new password-
// derived material. The vault_key itself is unchanged (the browser
// unwrapped it under the old password and re-wrapped it under the new
// one), so existing encrypted entries continue to decrypt. Bumps
// vault_generation so live SharedWorkers in other tabs re-lock.
func (s *Service) RotateVaultPassword(ctx context.Context, c *services.Caller, p RotateParams, audit auditCtx) (int, error) {
	if !c.IsAdmin {
		return 0, services.ErrForbidden
	}
	if err := s.requireRecentUnlock(ctx, c); err != nil {
		return 0, err
	}
	if err := validateKDFParams(p.KDFParams); err != nil {
		return 0, fmt.Errorf("invalid kdf_params: %w", err)
	}
	if len(p.AuthHash) != 32 {
		return 0, errors.New("auth_hash must be 32 bytes")
	}
	if err := validateWrappedVaultKey(p.WrappedVaultKey, p.WrappedVaultKeyNonce); err != nil {
		return 0, err
	}

	newGen, err := models.RotateVaultTenant(ctx, s.pool, models.VaultRotateParams{
		TenantID:             c.TenantID,
		KDFParams:            p.KDFParams,
		AuthHash:             p.AuthHash,
		WrappedVaultKey:      p.WrappedVaultKey,
		WrappedVaultKeyNonce: p.WrappedVaultKeyNonce,
		RotatedByUserID:      c.UserID,
	})
	if err != nil {
		return 0, err
	}
	if err := audit.logRequired(ctx, "vault.rotate", "vault_tenant", nil, EvtRotate{NewGeneration: newGen}); err != nil {
		return newGen, fmt.Errorf("recording rotate audit: %w", err)
	}
	return newGen, nil
}

// NukeVault deletes the tenant's vault row and all entries. Admin-only;
// requires the caller to have re-confirmed by typing the tenant slug
// (the web handler checks this before calling here). Returns the count
// of entries destroyed for the briefing + audit.
//
// This is the escape hatch for "the team forgot the master password."
// There is no undo and no recovery: vault_key is gone, every entry's
// ciphertext is unreadable from this point forward.
func (s *Service) NukeVault(ctx context.Context, c *services.Caller, audit auditCtx) (int, error) {
	if !c.IsAdmin {
		return 0, services.ErrForbidden
	}
	count, err := models.NukeVaultTenant(ctx, s.pool, c.TenantID)
	if err != nil {
		return 0, err
	}
	if err := audit.logRequired(ctx, "vault.nuke", "vault_tenant", nil, EvtNuke{EntriesDestroyed: count}); err != nil {
		return count, fmt.Errorf("recording nuke audit: %w", err)
	}
	s.fireVaultNukedBriefing(ctx, c, count)
	return count, nil
}

// ===== card / briefing helpers =====

// fireFailedUnlockDecision creates a high-priority decision card on the
// affected user's swipe stack so they notice repeated wrong-password
// attempts. Best-effort — surfaces only on threshold transitions (the
// rate-limit allow() returns false), not on every miss.
func (s *Service) fireFailedUnlockDecision(ctx context.Context, c *services.Caller, reason string, window time.Duration) {
	if s.cards == nil || c == nil {
		return
	}
	body := fmt.Sprintf("**Failed vault unlock attempts (%s).** The vault is rate-limited for %s on this IP. "+
		"If this wasn't you, your Slack account may be compromised — rotate Slack credentials and check session activity.",
		reason, window)
	err := s.cards.CreateDecision(ctx, c.TenantID, CardCreateInput{
		Title:      "Failed vault unlock attempts",
		Body:       body,
		UserScopes: []uuid.UUID{c.UserID},
		Urgent:     true,
		Decision: &CardDecisionCreateInput{
			Priority:            "high",
			RecommendedOptionID: "was_me",
			Options: []CardDecisionOption{
				{OptionID: "was_me", Label: "It was me"},
				{OptionID: "wasnt_me", Label: "Not me — investigate"},
			},
		},
	})
	if err != nil {
		slog.Warn("vault: firing failed-unlock decision failed", "user_id", c.UserID, "error", err)
	}
}

// fireVaultNukedBriefing posts a tenant-wide briefing after a successful
// /nuke, so other members notice immediately rather than discovering it
// the next time they try to view a secret.
func (s *Service) fireVaultNukedBriefing(ctx context.Context, actor *services.Caller, entryCount int) {
	if s.cards == nil || actor == nil {
		return
	}
	actorName := actor.Identity
	user, _ := models.GetUserByID(ctx, s.pool, actor.TenantID, actor.UserID)
	if user != nil && user.DisplayName != nil && *user.DisplayName != "" {
		actorName = *user.DisplayName
	}
	actorName = sanitizeMarkdownInline(actorName)

	tenant, _ := models.GetTenantByID(ctx, s.pool, actor.TenantID)
	setupURL := "/apps/vault/setup"
	if tenant != nil {
		setupURL = fmt.Sprintf("/%s/apps/vault/setup", tenant.Slug)
	}

	body := fmt.Sprintf(
		"**The vault was reset by %s.** All %d stored secrets have been deleted and cannot be recovered. "+
			"An admin can [set up a fresh vault](%s) with a new master password.",
		actorName, entryCount, setupURL,
	)
	err := s.cards.CreateBriefing(ctx, actor.TenantID, CardCreateInput{
		Title:    "Vault was reset",
		Body:     body,
		Urgent:   true,
		Briefing: &CardBriefingCreateInput{Severity: "important"},
	})
	if err != nil {
		slog.Warn("vault: firing vault-nuked briefing failed", "tenant_id", actor.TenantID, "error", err)
	}
}

// sanitizeMarkdownInline removes characters that would terminate a code
// fence, inline-code span, or HTML tag if interpolated into a Markdown
// body. Keeps the content recognisable (no full HTML escape) — Slack
// display names rarely use these characters legitimately.
func sanitizeMarkdownInline(s string) string {
	return strings.NewReplacer(
		"`", "ʼ",
		"<", "‹",
		">", "›",
		"\n", " ",
		"\r", " ",
	).Replace(s)
}

// encodeTenantIDForAAD returns the tenant UUID as a 32-char lowercase
// hex string of its raw 16 bytes (no hyphens). The browser decodes this
// to a Uint8Array and passes it as the AAD when AES-GCM-decrypting the
// wrapped_vault_key. Hex (not the canonical hyphenated form) is the
// transport so JS doesn't have to parse UUID syntax just to recover
// bytes — that's the only purpose this representation serves.
func encodeTenantIDForAAD(id uuid.UUID) string {
	return hex.EncodeToString(id[:])
}
