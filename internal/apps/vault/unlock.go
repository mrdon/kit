package vault

import (
	"context"
	"crypto/subtle"
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

// ErrUnlockLocked is returned when locked_until is in the future or the
// per-IP rate limiter has emptied its bucket.
var ErrUnlockLocked = errors.New("unlock locked")

// ErrUnlockNotGranted is returned when the user has registered but not
// yet been granted access (wrapped_vault_key is NULL).
var ErrUnlockNotGranted = errors.New("not granted")

// ErrUnlockPending is returned when the user's row is pending=true (still
// in the self-unlock-test phase). They should retry the register flow.
var ErrUnlockPending = errors.New("registration pending")

// ErrStepUpRequired is returned by sensitive operations (grant,
// scope-widening) when the caller hasn't unlocked their vault recently
// enough. Plan §"Step-up auth on sensitive ops": 5-minute window.
var ErrStepUpRequired = errors.New("recent unlock required")

const (
	// stepUpWindow is how recently the caller must have unlocked before
	// a sensitive operation is allowed.
	stepUpWindow = 5 * time.Minute

	// unlockLockoutThreshold triggers a soft 15-minute lockout. Counter
	// resets on successful unlock.
	unlockLockoutThreshold = 5
	unlockLockoutDuration  = 15 * time.Minute

	// unlockHardLockoutThreshold promotes a soft lockout into a 24-hour
	// lockout. Plan §"Unlock attack surface".
	unlockHardLockoutThreshold = 20
	unlockHardLockoutDuration  = 24 * time.Hour

	// perIPCapacity / perIPRefillInterval define the secondary per-IP
	// throttle on /api/vault/unlock and /api/vault/self_unlock_test.
	perIPCapacity       = 20
	perIPRefillInterval = time.Minute
)

// Unlock validates auth_hash against the caller's vault_users row.
// Constant-time comparison; on miss, a dummy comparison runs against
// random bytes so the response timing doesn't leak row-existence.
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
		// No row: dummy compare for timing parity. Distinct action
		// string so the audit row doesn't leak "user has no
		// registration" to a privileged audit-log reader.
		_ = subtle.ConstantTimeCompare(authHash, dummyHash())
		audit.log(ctx, "vault.unlock_failed_no_row", "vault_user", &c.UserID, struct{}{})
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
		// Fire the user-facing alarm card ONLY on threshold transitions
		// (count == soft / hard threshold exactly), not on every miss
		// past threshold. Otherwise an attacker who knows the lockout
		// timing can spam the user's stack with a fresh card after each
		// 15-minute cycle.
		notifyUser := false
		switch count {
		case unlockLockoutThreshold:
			lockoutDuration = unlockLockoutDuration
			locked = true
			notifyUser = true
		case unlockHardLockoutThreshold:
			lockoutDuration = unlockHardLockoutDuration
			locked = true
			notifyUser = true
		default:
			// Past either threshold but not on the boundary: still
			// apply the appropriate lockout, just don't re-alarm.
			switch {
			case count > unlockHardLockoutThreshold:
				lockoutDuration = unlockHardLockoutDuration
				locked = true
			case count > unlockLockoutThreshold:
				lockoutDuration = unlockLockoutDuration
				locked = true
			}
		}
		if locked {
			_ = models.SetVaultUserLockedUntil(ctx, s.pool, c.TenantID, c.UserID, time.Now().Add(lockoutDuration))
			if notifyUser {
				s.fireFailedUnlockDecision(ctx, c, count, lockoutDuration)
			}
		}
		audit.log(ctx, "vault.unlock_failed", "vault_user", &c.UserID, EvtUnlockFailed{FailedCount: count, Locked: locked})
		return nil, ErrUnlockMismatch
	}

	if v.Pending {
		return nil, ErrUnlockPending
	}
	if v.WrappedVaultKey == nil {
		return nil, ErrUnlockNotGranted
	}

	if err := models.ResetFailedUnlocks(ctx, s.pool, c.TenantID, c.UserID); err != nil {
		slog.Warn("vault: resetting failed_unlocks", "error", err)
	}
	// vault.unlock is fail-closed: requireRecentUnlock queries this row
	// shortly after to authorize widening / grant operations.
	if err := audit.logRequired(ctx, "vault.unlock", "vault_user", &c.UserID, EvtUnlock{}); err != nil {
		return nil, fmt.Errorf("recording unlock: %w", err)
	}

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
//   - When no user in the tenant has been activated yet (the
//     `wrapped_vault_key NOT NULL AND pending=false` query returns 0
//     rows), the caller MUST be admin AND MUST supply a non-nil
//     WrappedVaultKey (self-issued tenant init).
//   - Otherwise WrappedVaultKey MUST be nil; a teammate will grant later.
//
// All new rows start pending=true; the browser flips it via SelfUnlockTest
// after a successful round-trip. Re-registration of a still-pending row
// is allowed and acts as an UPSERT (canary-failure recovery).
func (s *Service) Register(ctx context.Context, c *services.Caller, p RegisterParams, audit auditCtx) error {
	if err := validateRSAPubKey(p.UserPublicKey); err != nil {
		return fmt.Errorf("invalid public key: %w", err)
	}
	if len(p.AuthHash) != 32 {
		return errors.New("auth_hash must be 32 bytes")
	}
	if len(p.KDFParams) == 0 {
		return errors.New("kdf_params required")
	}
	if len(p.UserPrivateKeyNonce) != 12 {
		return errors.New("private key nonce must be 12 bytes")
	}
	// RSA-2048 PKCS#8 + AES-GCM tag is in a tight (~1200-1300) band;
	// outside of that range the upload is truncated or garbage. Plan
	// §"Input validation at registration & grant".
	const (
		minPrivCT = 1200
		maxPrivCT = 1400
	)
	if n := len(p.UserPrivateKeyCiphertext); n < minPrivCT || n > maxPrivCT {
		return fmt.Errorf("private key ciphertext outside RSA-2048 PKCS#8 size range (got %d, want %d-%d)", n, minPrivCT, maxPrivCT)
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
	// the prior pubkey *before* the write so the audit row records the
	// real diff (otherwise we'd read back the new key we just wrote).
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
		s.fireResetTriggeredBriefing(ctx, c)
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

// SelfUnlockTest flips pending=false after the browser has demonstrated
// the keys round-trip cleanly. Called immediately after Register, with
// the auth_hash the browser just derived. Constant-time comparison plus
// the same per-IP rate limit as Unlock so it can't be used as a faster
// brute-force oracle.
func (s *Service) SelfUnlockTest(ctx context.Context, c *services.Caller, authHash []byte, audit auditCtx) error {
	if audit.ip != nil && !s.rateLimit.allow(audit.ip.String()) {
		return ErrUnlockLocked
	}
	v, err := models.GetVaultUser(ctx, s.pool, c.TenantID, c.UserID)
	if err != nil {
		return err
	}
	if v == nil {
		_ = subtle.ConstantTimeCompare(authHash, dummyHash())
		return ErrUnlockMismatch
	}
	if subtle.ConstantTimeCompare(authHash, v.AuthHash) != 1 {
		return ErrUnlockMismatch
	}
	return models.MarkVaultUserActive(ctx, s.pool, c.TenantID, c.UserID)
}

// GrantParams is the input to /api/vault/grants/<target>. Granter must
// be a vault member with a recent unlock; the service enforces both.
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
		TargetPubKeyFingerprint: pubkeyFingerprint(target.UserPublicKey),
		DuringResetCooldown:     duringCooldown,
	})
	s.fireAccessGrantedBriefing(ctx, c, p.TargetUserID)
	return nil
}

// CancelReset wipes the caller's vault_users row when it's currently in
// the 24h post-reset cooldown. This is the legitimate user's escape
// hatch for the Slack-account-takeover-then-trigger-reset attack: an
// attacker briefly hijacks Slack, resets the vault password, and waits
// for an admin to re-grant. The legitimate user — still logged in
// elsewhere — sees the reset-triggered briefing in their swipe stack
// and clicks Cancel; this endpoint nukes the attacker's keys before
// any teammate can wrap a vault key for them.
//
// Auth: session cookie only (no step-up — by definition the legitimate
// user can't unlock right now since the attacker just changed the
// master password). Idempotent: returns ErrNotFound if there's no row
// in cooldown to cancel.
func (s *Service) CancelReset(ctx context.Context, c *services.Caller, audit auditCtx) error {
	if err := models.CancelVaultReset(ctx, s.pool, c.TenantID, c.UserID); err != nil {
		return err
	}
	audit.log(ctx, "vault.master_password_reset_cancelled", "vault_user", &c.UserID, EvtMasterPasswordResetCancelled{})
	return nil
}

// DeclinePending deletes a vault_users row that hasn't been granted yet.
// Admin-only; refuses if the user already has access (those go through
// RevokeGrant).
func (s *Service) DeclinePending(ctx context.Context, c *services.Caller, targetUserID uuid.UUID, audit auditCtx) error {
	if !c.IsAdmin {
		return services.ErrForbidden
	}
	target, err := models.GetVaultUser(ctx, s.pool, c.TenantID, targetUserID)
	if err != nil {
		return err
	}
	if target == nil {
		return models.ErrNotFound
	}
	if target.WrappedVaultKey != nil {
		return errors.New("user already has access; use revoke instead")
	}
	if _, err := s.pool.Exec(ctx,
		`DELETE FROM app_vault_users WHERE tenant_id = $1 AND user_id = $2 AND wrapped_vault_key IS NULL`,
		c.TenantID, targetUserID,
	); err != nil {
		return fmt.Errorf("decline pending: %w", err)
	}
	audit.log(ctx, "vault.revoke_grant", "vault_user", &targetUserID, EvtRevokeGrant{})
	return nil
}

// RevokeGrant nulls the target user's wrapped_vault_key. Forward secrecy
// is a v2 item — existing browser caches are unaffected. Admin-only;
// the service enforces the check so MCP / agent callers can't bypass it.
func (s *Service) RevokeGrant(ctx context.Context, c *services.Caller, targetUserID uuid.UUID, audit auditCtx) error {
	if !c.IsAdmin {
		return services.ErrForbidden
	}
	if err := models.RevokeVaultGrant(ctx, s.pool, c.TenantID, targetUserID); err != nil {
		return err
	}
	audit.log(ctx, "vault.revoke_grant", "vault_user", &targetUserID, EvtRevokeGrant{})
	return nil
}

// AdminResetVaultUser wipes a user's vault_users row so they can register
// from scratch with a new master password. Called from the gated tool
// handler after an admin approves the reset request decision card.
//
// Defense-in-depth admin check — the gate's resolve path already runs
// with the approver's caller, but this method must be safe regardless
// of who calls it. Refuses self-reset to protect admins from locking
// themselves out (they would then have nobody to grant them back in).
//
// On success the caller's identity goes onto the audit row as actor;
// the row's actor + target_id pair is the source of truth for "who
// reset whom and when". A best-effort briefing fires on the target's
// stack with a register link.
func (s *Service) AdminResetVaultUser(ctx context.Context, c *services.Caller, targetUserID uuid.UUID, audit auditCtx) error {
	if !c.IsAdmin {
		return services.ErrForbidden
	}
	if c.UserID == targetUserID {
		return errors.New("admin cannot reset their own vault — ask another admin")
	}
	if err := models.AdminDeleteVaultUser(ctx, s.pool, c.TenantID, targetUserID); err != nil {
		return err
	}
	audit.log(ctx, "vault.admin_reset", "vault_user", &targetUserID, EvtAdminReset{
		TargetUserID: targetUserID,
	})
	s.fireAdminResetBriefing(ctx, c, targetUserID)
	return nil
}

// RequestVaultReset creates a decision card on the admin role's stack
// asking them to approve wiping the caller's vault registration. The
// approve option carries the gated `reset_vault_user` tool; admin
// approval routes through the existing decision-resolve flow which
// runs the tool as the approver and lands in AdminResetVaultUser.
//
// System-scoped (CardSurface.CreateDecision routes through
// CreateSystemDecision in cmd/kit/vault_cards.go) so the requesting
// non-admin user can scope the card to the admin role even though
// they don't hold it themselves.
func (s *Service) RequestVaultReset(ctx context.Context, c *services.Caller, audit auditCtx) error {
	if s.cards == nil {
		return errors.New("card surface not configured")
	}
	v, err := models.GetVaultUser(ctx, s.pool, c.TenantID, c.UserID)
	if err != nil {
		return err
	}
	if v == nil {
		return errors.New("you have no vault registration to reset — open the vault to set one up")
	}
	user, _ := models.GetUserByID(ctx, s.pool, c.TenantID, c.UserID)

	args, err := json.Marshal(map[string]string{"user_id": c.UserID.String()})
	if err != nil {
		return fmt.Errorf("marshalling reset args: %w", err)
	}

	if err := s.cards.CreateDecision(ctx, c.TenantID, CardCreateInput{
		Title:      "Reset " + resetRequesterDisplay(c, user) + "'s vault?",
		Body:       buildResetRequestBody(c, user),
		RoleScopes: []string{"admin"},
		Decision: &CardDecisionCreateInput{
			Priority:            "high",
			RecommendedOptionID: "approve",
			Options: []CardDecisionOption{
				{
					OptionID:  "approve",
					Label:     "Reset their vault",
					ToolName:  "reset_vault_user",
					Arguments: args,
				},
				{OptionID: "skip", Label: "Cancel"},
			},
		},
	}); err != nil {
		return fmt.Errorf("creating reset request card: %w", err)
	}

	audit.log(ctx, "vault.reset_requested", "vault_user", &c.UserID, EvtVaultResetRequested{})
	return nil
}

func resetRequesterDisplay(c *services.Caller, user *models.User) string {
	if user != nil && user.DisplayName != nil && *user.DisplayName != "" {
		return sanitizeMarkdownInline(*user.DisplayName)
	}
	return sanitizeMarkdownInline(c.Identity)
}

func buildResetRequestBody(c *services.Caller, user *models.User) string {
	displayName := resetRequesterDisplay(c, user)
	slackID := c.Identity
	if user != nil && user.SlackUserID != "" {
		slackID = user.SlackUserID
	}
	slackID = sanitizeMarkdownInline(slackID)

	var b strings.Builder
	fmt.Fprintf(&b, "**%s** (<@%s>) forgot their vault master password and is asking for a reset.\n\n", displayName, slackID)
	b.WriteString("Approving wipes their vault registration. They'll then set a new master password and need their access re-granted — their stored secrets are not lost.\n\n")
	b.WriteString("**Verify out-of-band that this request is really from them** before approving. A compromised Slack account could trigger this to seed an attacker-controlled key for the next grant step.")
	return b.String()
}

// fireAdminResetBriefing posts an urgent briefing on the target's stack
// telling them their vault was reset and pointing them at /register.
// Urgent because they cannot use any stored secret until they re-register.
// Severity 'important' matches fireResetTriggeredBriefing for consistency.
func (s *Service) fireAdminResetBriefing(ctx context.Context, approver *services.Caller, targetUserID uuid.UUID) {
	if s.cards == nil {
		return
	}
	tenant, err := models.GetTenantByID(ctx, s.pool, approver.TenantID)
	if err != nil || tenant == nil {
		slog.Warn("vault: loading tenant for admin-reset briefing", "error", err)
		return
	}
	approverUser, _ := models.GetUserByID(ctx, s.pool, approver.TenantID, approver.UserID)
	approverName := approver.Identity
	if approverUser != nil && approverUser.DisplayName != nil && *approverUser.DisplayName != "" {
		approverName = *approverUser.DisplayName
	}
	approverName = sanitizeMarkdownInline(approverName)

	registerURL := fmt.Sprintf("/%s/apps/vault/register", tenant.Slug)
	body := fmt.Sprintf(
		"**Your vault was reset by %s.** Set a new master password to continue using the vault. "+
			"After you save, an admin will re-grant your access. Your stored secrets are not lost.\n\n"+
			"[Set a new master password →](%s)",
		approverName, registerURL,
	)
	err = s.cards.CreateBriefing(ctx, approver.TenantID, CardCreateInput{
		Title:      "Vault reset — set a new master password",
		Body:       body,
		UserScopes: []uuid.UUID{targetUserID},
		Urgent:     true,
		Briefing:   &CardBriefingCreateInput{Severity: "important"},
	})
	if err != nil {
		slog.Warn("vault: firing admin-reset briefing failed", "user_id", targetUserID, "error", err)
	}
}

// ===== card / briefing helpers =====

// fireGrantRequestCard creates an admin-scoped decision card asking a
// teammate to grant (or re-grant after reset) vault access to the caller.
// Body includes the user's display name + Slack handle + the fingerprint
// for out-of-band verification.
func (s *Service) fireGrantRequestCard(ctx context.Context, target *services.Caller, isReset bool, fingerprint string) error {
	if s.cards == nil {
		return nil
	}
	title := "Grant vault access"
	if isReset {
		title = "Re-grant vault access (password reset)"
	}
	user, _ := models.GetUserByID(ctx, s.pool, target.TenantID, target.UserID)
	body := buildGrantCardBody(target, user, isReset, fingerprint)

	return s.cards.CreateDecision(ctx, target.TenantID, CardCreateInput{
		Title:      title,
		Body:       body,
		RoleScopes: []string{"admin"},
		Decision: &CardDecisionCreateInput{
			Priority:            "high",
			RecommendedOptionID: "open_grant_page",
			Options: []CardDecisionOption{
				{OptionID: "open_grant_page", Label: "Review and grant"},
				{OptionID: "decline", Label: "Decline"},
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
	// Display names come from Slack profiles; a malicious user could
	// set theirs to inject Markdown / HTML into the admin's swipe card.
	// Strip the syntactically dangerous characters before interpolation.
	displayName = sanitizeMarkdownInline(displayName)
	slackID = sanitizeMarkdownInline(slackID)

	var b strings.Builder
	if isReset {
		fmt.Fprintf(&b, "**%s** (<@%s>) reset their vault password. Their public-key fingerprint has changed.\n\n", displayName, slackID)
	} else {
		fmt.Fprintf(&b, "**%s** (<@%s>) registered for the vault and needs access.\n\n", displayName, slackID)
	}
	b.WriteString("Public-key fingerprint:\n\n```\n")
	b.WriteString(fingerprint)
	b.WriteString("\n```\n\n")
	b.WriteString("**Verify this fingerprint with them out-of-band** before granting. ")
	b.WriteString("Open the grant page to complete the action.")
	return b.String()
}

// sanitizeMarkdownInline removes characters that would terminate a code
// fence, inline-code span, or HTML tag if interpolated into a Markdown
// body. Keeps the content recognisable (no full HTML escape) — Slack
// display names rarely use these characters legitimately.
func sanitizeMarkdownInline(s string) string {
	r := strings.NewReplacer(
		"`", "ʼ",
		"<", "‹",
		">", "›",
		"\n", " ",
		"\r", " ",
	)
	return r.Replace(s)
}

// fireFailedUnlockDecision creates a high-priority decision card on the
// affected user's swipe stack: "Was this you? / Lock my account."
// Decision (not briefing) because the user genuinely needs to choose,
// and decisions support action buttons today while briefings don't.
func (s *Service) fireFailedUnlockDecision(ctx context.Context, c *services.Caller, count int, lockoutDuration time.Duration) {
	if s.cards == nil {
		return
	}
	body := fmt.Sprintf("**%d failed unlock attempts on your Kit vault.** Your vault is locked for %s.\n\n"+
		"If this wasn't you, your Slack account may be compromised — rotate your Slack credentials and re-OAuth before approving any vault grant.",
		count, lockoutDuration)
	err := s.cards.CreateDecision(ctx, c.TenantID, CardCreateInput{
		Title:      "Failed unlock attempts on your vault",
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

// fireResetTriggeredBriefing posts a briefing on the resetting user's
// stack so a Slack-account-takeover attacker can't silently complete a
// reset without the legitimate user noticing.
func (s *Service) fireResetTriggeredBriefing(ctx context.Context, c *services.Caller) {
	if s.cards == nil {
		return
	}
	cancelURL := ""
	if tenant, err := models.GetTenantByID(ctx, s.pool, c.TenantID); err == nil && tenant != nil {
		cancelURL = fmt.Sprintf("/%s/apps/vault/cancel_reset", tenant.Slug)
	}
	body := "**Your Kit vault password was just reset.** If this wasn't you, your Slack account " +
		"may be compromised — rotate your Slack credentials immediately. Until you do, do not " +
		"approve any incoming grant request for your account."
	if cancelURL != "" {
		body += fmt.Sprintf("\n\n[**Cancel the reset**](%s) to wipe the pending keys before a teammate grants access. You will need to re-register afterward.", cancelURL)
	}
	err := s.cards.CreateBriefing(ctx, c.TenantID, CardCreateInput{
		Title:      "Vault password reset",
		Body:       body,
		UserScopes: []uuid.UUID{c.UserID},
		Urgent:     true,
		Briefing:   &CardBriefingCreateInput{Severity: "important"},
	})
	if err != nil {
		slog.Warn("vault: firing reset-triggered briefing failed", "user_id", c.UserID, "error", err)
	}
}

// fireAccessGrantedBriefing posts a briefing on the newly-granted user's
// stack so they know their access is ready (no polling required).
func (s *Service) fireAccessGrantedBriefing(ctx context.Context, c *services.Caller, targetUserID uuid.UUID) {
	if s.cards == nil {
		return
	}
	body := "Your Kit vault access is now active. Open the vault to add or look up secrets."
	err := s.cards.CreateBriefing(ctx, c.TenantID, CardCreateInput{
		Title:      "Vault access granted",
		Body:       body,
		UserScopes: []uuid.UUID{targetUserID},
		Briefing:   &CardBriefingCreateInput{Severity: "info"},
	})
	if err != nil {
		slog.Warn("vault: firing access-granted briefing failed", "user_id", targetUserID, "error", err)
	}
}
