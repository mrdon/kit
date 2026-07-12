package squareshifts

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

// Audit actions for the shift sync. Namespaced under the app name so they
// share audit_events cleanly with every other app.
const (
	actionSyncCompleted = "square_shifts.sync_completed"
	actionSyncFailed    = "square_shifts.sync_failed"
)

// syncMetadata is the typed audit payload for a sync run (per the codebase
// rule: typed constructors, never free-form text).
type syncMetadata struct {
	TriggeredBy string `json:"triggered_by"`
	Created     int    `json:"created"`
	Updated     int    `json:"updated"`
	Deleted     int    `json:"deleted"`
	DurationMS  int64  `json:"duration_ms"`
	Error       string `json:"error,omitempty"`
}

// auditCompleted records a successful run. Audit failures are logged, not
// propagated — they must never fail the sync itself.
func (a *App) auditCompleted(ctx context.Context, tenantID uuid.UUID, triggeredBy string, sum SyncSummary, dur time.Duration) {
	a.appendAudit(ctx, tenantID, actionSyncCompleted, syncMetadata{
		TriggeredBy: triggeredBy,
		Created:     sum.Created,
		Updated:     sum.Updated,
		Deleted:     sum.Deleted,
		DurationMS:  dur.Milliseconds(),
	})
}

// auditFailed records a failed run with the error message.
func (a *App) auditFailed(ctx context.Context, tenantID uuid.UUID, triggeredBy string, syncErr error, dur time.Duration) {
	a.appendAudit(ctx, tenantID, actionSyncFailed, syncMetadata{
		TriggeredBy: triggeredBy,
		DurationMS:  dur.Milliseconds(),
		Error:       syncErr.Error(),
	})
}

func (a *App) appendAudit(ctx context.Context, tenantID uuid.UUID, action string, meta syncMetadata) {
	if err := models.AppendAudit(ctx, a.pool, models.AuditEvent{
		TenantID: tenantID,
		Action:   action,
		Metadata: meta,
	}); err != nil {
		slog.Warn("squareshifts: recording audit event failed", "tenant_id", tenantID, "action", action, "error", err)
	}
}

// lastRun is the most recent sync audit event for a tenant, for the status
// tool. ok is false when there's been no run yet.
type lastRun struct {
	Action    string
	Meta      syncMetadata
	CreatedAt time.Time
}

// getLastRun reads the newest square_shifts.sync_* audit event for a tenant.
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

// listRecentRuns returns the most recent sync audit events (newest first),
// up to limit, for the Manage page's run history.
func listRecentRuns(ctx context.Context, a *App, tenantID uuid.UUID, limit int) ([]lastRun, error) {
	rows, err := a.pool.Query(ctx, `
		SELECT action, metadata, created_at
		FROM audit_events
		WHERE tenant_id = $1 AND action IN ($2, $3)
		ORDER BY created_at DESC
		LIMIT $4`,
		tenantID, actionSyncCompleted, actionSyncFailed, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []lastRun
	for rows.Next() {
		var lr lastRun
		var metaJSON []byte
		if err := rows.Scan(&lr.Action, &metaJSON, &lr.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(metaJSON, &lr.Meta)
		out = append(out, lr)
	}
	return out, rows.Err()
}
