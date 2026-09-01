package squaresales

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mrdon/kit/internal/models"
)

// Audit actions, namespaced under the app name so they share audit_events
// cleanly with every other app. Because this app creates no agent session,
// audit_events plus jobs.last_error are the ENTIRE debugging trail -- there
// are no session_events to fall back on.
const (
	actionSyncCompleted = "square_sales.sync_completed"
	actionSyncFailed    = "square_sales.sync_failed"
	actionScopeMissing  = "square_sales.scope_missing"
	actionCardPosted    = "square_sales.card_posted"
)

// auditMetadata is the typed audit payload (per the codebase rule: typed
// constructors, never free-form text).
type auditMetadata struct {
	TriggeredBy  string `json:"triggered_by,omitempty"`
	Days         int    `json:"days,omitempty"`
	Hours        int    `json:"hours,omitempty"`
	Items        int    `json:"items,omitempty"`
	BusinessDate string `json:"business_date,omitempty"`
	Severity     string `json:"severity,omitempty"`
	Findings     int    `json:"findings,omitempty"`
	DurationMS   int64  `json:"duration_ms,omitempty"`
	Error        string `json:"error,omitempty"`
}

func (a *App) auditSyncCompleted(ctx context.Context, tenantID uuid.UUID, triggeredBy string, sum SyncSummary, dur time.Duration) {
	a.appendAudit(ctx, tenantID, actionSyncCompleted, auditMetadata{
		TriggeredBy: triggeredBy,
		Days:        sum.Days,
		Hours:       sum.Hours,
		Items:       sum.Items,
		DurationMS:  dur.Milliseconds(),
	})
}

func (a *App) auditSyncFailed(ctx context.Context, tenantID uuid.UUID, triggeredBy string, syncErr error, dur time.Duration) {
	a.appendAudit(ctx, tenantID, actionSyncFailed, auditMetadata{
		TriggeredBy: triggeredBy,
		DurationMS:  dur.Milliseconds(),
		Error:       syncErr.Error(),
	})
}

func (a *App) auditCardPosted(ctx context.Context, tenantID uuid.UUID, date time.Time, severity string, findings int) {
	a.appendAudit(ctx, tenantID, actionCardPosted, auditMetadata{
		BusinessDate: date.Format(time.DateOnly),
		Severity:     severity,
		Findings:     findings,
	})
}

// auditScopeMissing records a token that can't read sales, at most once a
// day. The sync runs hourly, so an unguarded write would put 24 identical
// rows a day into audit_events and bury everything else.
func (a *App) auditScopeMissing(ctx context.Context, tenantID uuid.UUID, scopeErr error) {
	recent, err := a.scopeMissingLoggedRecently(ctx, tenantID)
	if err != nil || recent {
		return
	}
	a.appendAudit(ctx, tenantID, actionScopeMissing, auditMetadata{Error: scopeErr.Error()})
}

func (a *App) scopeMissingLoggedRecently(ctx context.Context, tenantID uuid.UUID) (bool, error) {
	var exists bool
	err := a.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM audit_events
			WHERE tenant_id = $1 AND action = $2 AND created_at > now() - interval '24 hours'
		)`, tenantID, actionScopeMissing).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

func (a *App) appendAudit(ctx context.Context, tenantID uuid.UUID, action string, meta auditMetadata) {
	if err := models.AppendAudit(ctx, a.pool, models.AuditEvent{
		TenantID: tenantID,
		Action:   action,
		Metadata: meta,
	}); err != nil {
		slog.Warn("squaresales: recording audit event failed", "tenant_id", tenantID, "action", action, "error", err)
	}
}

// lastRun is the most recent sync audit event, for the status tool.
type lastRun struct {
	Action    string
	Meta      auditMetadata
	CreatedAt time.Time
}

func getLastRun(ctx context.Context, a *App, tenantID uuid.UUID) (lastRun, bool, error) {
	var lr lastRun
	var metaJSON []byte
	err := a.pool.QueryRow(ctx, `
		SELECT action, metadata, created_at
		FROM audit_events
		WHERE tenant_id = $1 AND action IN ($2, $3)
		ORDER BY created_at DESC
		LIMIT 1`,
		tenantID, actionSyncCompleted, actionSyncFailed,
	).Scan(&lr.Action, &metaJSON, &lr.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return lastRun{}, false, nil
	}
	if err != nil {
		return lastRun{}, false, err
	}
	_ = json.Unmarshal(metaJSON, &lr.Meta)
	return lr, true, nil
}
