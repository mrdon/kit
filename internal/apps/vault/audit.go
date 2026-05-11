// Package vault implements Kit's password-vault feature: per-tenant
// encrypted vault locked behind one shared master password (shared
// out-of-band among the team). All encryption and decryption happen in
// the browser; the server stores only ciphertext, the auth_hash for
// password validation, and metadata.
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

// EvtSetup is written when an admin initializes the tenant vault for the
// first time. Actor is the admin; target_kind=vault_tenant, target_id nil.
type EvtSetup struct{}

// EvtUnlock is written on a successful unlock (auth_hash match).
type EvtUnlock struct{}

// EvtUnlockFailed is written on a missed unlock or rate-limited request.
// At most one of NotSetUp / Locked / RateLimited is true at a time;
// none being true means "auth_hash mismatch on a healthy tenant row."
type EvtUnlockFailed struct {
	NotSetUp    bool `json:"not_set_up,omitempty"`
	Locked      bool `json:"locked,omitempty"`
	RateLimited bool `json:"rate_limited,omitempty"`
}

// EvtRotate is written when an admin rotates the master password. The
// vault_key is unchanged (existing entries still decrypt); only the
// password-derived material is replaced.
type EvtRotate struct {
	NewGeneration int `json:"new_generation"`
}

// EvtNuke is written when an admin destroys the tenant vault (escape
// hatch for "we lost the password"). All entries are gone along with
// the wrap.
type EvtNuke struct {
	EntriesDestroyed int `json:"entries_destroyed"`
}

// EvtEntryCreate / EvtEntryView / EvtEntryUpdate / EvtEntryDelete capture
// the entry id; never log titles or other content fields.
type EvtEntryCreate struct{}
type EvtEntryView struct{}
type EvtEntryUpdate struct{}
type EvtEntryDelete struct{}

// EvtScopeChange logs a role-scope change as the from/to role pair.
// nil means "everyone in the tenant"; set means a specific role.
type EvtScopeChange struct {
	FromRoleID *uuid.UUID `json:"from_role_id,omitempty"`
	ToRoleID   *uuid.UUID `json:"to_role_id,omitempty"`
}

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
