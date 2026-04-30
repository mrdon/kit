package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AuditEvent is one row in the general-purpose audit_events log. The action
// string is dotted-namespaced (e.g. "vault.unlock_failed") so multiple apps
// can share the table without collision. Use typed constructors in each
// app's audit.go to populate Metadata; never write free-form text.
type AuditEvent struct {
	TenantID    uuid.UUID
	ActorUserID *uuid.UUID  // nil for system actions / unauthenticated probes
	Action      string      // "<app>.<verb>"
	TargetKind  string      // e.g. "vault_entry", "vault_user"; "" if no target
	TargetID    *uuid.UUID  // nil if no target
	Metadata    any         // marshalled to JSONB; use a typed struct from the app's audit.go
	IP          *netip.Addr // captured for security-relevant events
	UserAgent   string
}

// AppendAudit writes one row to audit_events. The table is append-only at
// the DB level (BEFORE UPDATE/DELETE triggers raise), so callers don't need
// to worry about accidental mutation. Errors are returned; callers may choose
// to log-and-continue (e.g., rate-limit accounting shouldn't fail a request)
// or fail-closed (e.g., grant operations).
func AppendAudit(ctx context.Context, pool *pgxpool.Pool, ev AuditEvent) error {
	if ev.TenantID == uuid.Nil {
		return errors.New("audit event missing tenant_id")
	}
	if ev.Action == "" {
		return errors.New("audit event missing action")
	}

	metaJSON, err := json.Marshal(ev.Metadata)
	if err != nil {
		return fmt.Errorf("marshalling audit metadata: %w", err)
	}
	if metaJSON == nil || string(metaJSON) == "null" {
		metaJSON = []byte("{}")
	}

	var ipArg any
	if ev.IP != nil && ev.IP.IsValid() {
		ipArg = ev.IP.String()
	}

	var targetKindArg, targetIDArg any
	if ev.TargetKind != "" {
		targetKindArg = ev.TargetKind
	}
	if ev.TargetID != nil {
		targetIDArg = *ev.TargetID
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO audit_events
			(tenant_id, actor_user_id, action, target_kind, target_id, metadata, ip, user_agent)
		VALUES
			($1, $2, $3, $4, $5, $6, $7, $8)
	`, ev.TenantID, ev.ActorUserID, ev.Action, targetKindArg, targetIDArg, metaJSON, ipArg, ev.UserAgent)
	if err != nil {
		return fmt.Errorf("inserting audit event: %w", err)
	}
	return nil
}
