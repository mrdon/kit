// Package vault implements Kit's password-vault feature: per-tenant
// encrypted vault distributed via per-user RSA-OAEP wrapping (Bitwarden-Org
// / 1Password-Shared-Vault model). All encryption and decryption happen in
// the browser; the server stores only ciphertext, public keys, and metadata.
//
// Audit events go through the general audit_events table (Kit-wide). The
// constructors here pin the metadata shape per action so log readers can
// rely on the schema without a free-form string detour.
package vault

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
)

// auditCtx bundles the request-time bits every audit row captures.
type auditCtx struct {
	pool      *pgxpool.Pool
	tenantID  uuid.UUID
	actorID   *uuid.UUID
	ip        *netip.Addr
	userAgent string
}

// newAuditCtx constructs an auditCtx from an HTTP request. actor may be
// nil for unauthenticated probes; tenant must already be resolved.
func newAuditCtx(pool *pgxpool.Pool, tenantID uuid.UUID, actorID *uuid.UUID, r *http.Request) auditCtx {
	return auditCtx{
		pool:      pool,
		tenantID:  tenantID,
		actorID:   actorID,
		ip:        clientIP(r),
		userAgent: clientUA(r),
	}
}

// log writes one audit row with action+target+metadata, attaching the
// request-context's IP/UA. Errors are logged but not returned — audit
// writes are best-effort and must not fail the user's operation.
func (a auditCtx) log(ctx context.Context, action, targetKind string, targetID *uuid.UUID, metadata any) {
	err := models.AppendAudit(ctx, a.pool, models.AuditEvent{
		TenantID:    a.tenantID,
		ActorUserID: a.actorID,
		Action:      action,
		TargetKind:  targetKind,
		TargetID:    targetID,
		Metadata:    metadata,
		IP:          a.ip,
		UserAgent:   a.userAgent,
	})
	if err != nil {
		slog.Warn("vault: appending audit event failed", "action", action, "error", err)
	}
}

// logRequired is the fail-closed counterpart of log — used for events
// where downstream logic (e.g. step-up auth's recent-unlock lookup)
// relies on the audit row actually being there. Currently used only
// for vault.unlock so a silently-dropped success doesn't lock out a
// legitimate user from sensitive ops.
func (a auditCtx) logRequired(ctx context.Context, action, targetKind string, targetID *uuid.UUID, metadata any) error {
	return models.AppendAudit(ctx, a.pool, models.AuditEvent{
		TenantID:    a.tenantID,
		ActorUserID: a.actorID,
		Action:      action,
		TargetKind:  targetKind,
		TargetID:    targetID,
		Metadata:    metadata,
		IP:          a.ip,
		UserAgent:   a.userAgent,
	})
}

// ===== Pinned metadata shapes per action =====

// EvtRegister is written when a user creates or replaces their vault_users row.
type EvtRegister struct {
	Replace           bool   `json:"replace"`             // true on master-password reset
	IsTenantInitiator bool   `json:"is_tenant_initiator"` // first user in the tenant
	PubKeyFingerprint string `json:"pubkey_fingerprint"`
}

// EvtUnlock is written on a successful unlock (auth_hash match).
type EvtUnlock struct{}

// EvtUnlockFailed is written on a missed unlock.
type EvtUnlockFailed struct {
	FailedCount int  `json:"failed_count"`
	Locked      bool `json:"locked"` // true once threshold crosses into locked_until
}

// EvtGrant is written when an existing vault user wraps the vault key for
// a teammate. The granter is the row's actor_user_id; the target is its
// target_id. The fingerprint stays in metadata because it's the
// out-of-band-verifiable identity the granter relied on.
type EvtGrant struct {
	TargetPubKeyFingerprint string `json:"target_pubkey_fingerprint"`
	DuringResetCooldown     bool   `json:"during_reset_cooldown"`
}

// EvtRevokeGrant is written when an admin nulls out a teammate's wrapped
// key (or declines a pending registration). All identity lives on the
// row itself; metadata is empty.
type EvtRevokeGrant struct{}

// EvtEntryCreate / EvtEntryView / EvtEntryUpdate / EvtEntryDelete capture
// the entry id; never log titles or other content fields.
type EvtEntryCreate struct{}
type EvtEntryView struct{}
type EvtEntryUpdate struct{}
type EvtEntryDelete struct{}

// EvtScopeChange logs the diff (added/removed scope rows) by ID. Free-form
// names are deliberately omitted — IDs are the canonical reference.
type EvtScopeChange struct {
	Added   []ScopeRef `json:"added"`
	Removed []ScopeRef `json:"removed"`
}

// ScopeRef identifies one scope row by kind + id (id is nil for tenant scope).
type ScopeRef struct {
	Kind string     `json:"kind"` // "user" | "role" | "tenant"
	ID   *uuid.UUID `json:"id,omitempty"`
}

// EvtMasterPasswordReset is written when a user uses the replace=true path.
type EvtMasterPasswordReset struct {
	OldPubKeyFingerprint string `json:"old_pubkey_fingerprint"`
	NewPubKeyFingerprint string `json:"new_pubkey_fingerprint"`
}

// EvtMasterPasswordResetCancelled is written when the reset target wipes
// their pending-reset row (Slack-account-takeover defense path).
type EvtMasterPasswordResetCancelled struct{}

// ===== HTTP helpers =====

// clientIP returns the request's remote IP as a netip.Addr, preferring the
// first entry in X-Forwarded-For when running behind a proxy. Returns nil
// when nothing parseable is available.
func clientIP(r *http.Request) *netip.Addr {
	if r == nil {
		return nil
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first := strings.TrimSpace(strings.SplitN(xff, ",", 2)[0])
		if addr, err := netip.ParseAddr(first); err == nil {
			return &addr
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		return &addr
	}
	return nil
}

func clientUA(r *http.Request) string {
	if r == nil {
		return ""
	}
	ua := r.Header.Get("User-Agent")
	if len(ua) > 512 {
		ua = ua[:512]
	}
	return ua
}
