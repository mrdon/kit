package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mrdon/kit/internal/models"
)

// Audit actions, namespaced under the app name so they share audit_events
// cleanly with every other app.
const (
	actionSyncCompleted = "events.sync_completed"
	actionSyncFailed    = "events.sync_failed"
)

// syncMetadata is the typed audit payload for a run. Typed rather than
// free-form text so the status view can render it without parsing prose.
type syncMetadata struct {
	TriggeredBy string `json:"triggered_by"`
	Created     int    `json:"created"`
	Updated     int    `json:"updated"`
	Deleted     int    `json:"deleted"`
	Skipped     int    `json:"skipped"`
	DurationMS  int64  `json:"duration_ms"`
	Error       string `json:"error,omitempty"`
}

func (a *App) auditCompleted(ctx context.Context, tenantID uuid.UUID, triggeredBy string, sum Summary, dur time.Duration) {
	a.appendAudit(ctx, tenantID, actionSyncCompleted, syncMetadata{
		TriggeredBy: triggeredBy,
		Created:     sum.Created,
		Updated:     sum.Updated,
		Deleted:     sum.Deleted,
		Skipped:     sum.Skipped,
		DurationMS:  dur.Milliseconds(),
	})
}

func (a *App) auditFailed(ctx context.Context, tenantID uuid.UUID, triggeredBy string, runErr error, dur time.Duration) {
	a.appendAudit(ctx, tenantID, actionSyncFailed, syncMetadata{
		TriggeredBy: triggeredBy,
		DurationMS:  dur.Milliseconds(),
		Error:       runErr.Error(),
	})
}

// appendAudit records an event. Audit failures are logged, never propagated --
// losing a log line must not fail the sync that produced it.
func (a *App) appendAudit(ctx context.Context, tenantID uuid.UUID, action string, meta syncMetadata) {
	if a.pool == nil {
		return
	}
	if err := models.AppendAudit(ctx, a.pool, models.AuditEvent{
		TenantID: tenantID,
		Action:   action,
		Metadata: meta,
	}); err != nil {
		slog.Warn("events: recording audit event failed",
			"tenant_id", tenantID, "action", action, "error", err)
	}
}

// Run is one recorded sync attempt, for the status view.
type Run struct {
	Action    string
	Meta      syncMetadata
	CreatedAt time.Time
}

// Succeeded reports whether this run completed.
func (r Run) Succeeded() bool { return r.Action == actionSyncCompleted }

// ListRecentRuns returns the newest sync audit events first.
func (a *App) ListRecentRuns(ctx context.Context, tenantID uuid.UUID, limit int) ([]Run, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	rows, err := a.pool.Query(ctx, `
		SELECT action, metadata, created_at
		FROM audit_events
		WHERE tenant_id = $1 AND action IN ($2, $3)
		ORDER BY created_at DESC
		LIMIT $4`,
		tenantID, actionSyncCompleted, actionSyncFailed, limit)
	if err != nil {
		return nil, fmt.Errorf("listing event sync runs: %w", err)
	}
	defer rows.Close()

	var out []Run
	for rows.Next() {
		var r Run
		var metaJSON []byte
		if err := rows.Scan(&r.Action, &metaJSON, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning sync run: %w", err)
		}
		_ = json.Unmarshal(metaJSON, &r.Meta)
		out = append(out, r)
	}
	return out, rows.Err()
}

// LastRun returns the most recent run, if there has been one.
func (a *App) LastRun(ctx context.Context, tenantID uuid.UUID) (Run, bool, error) {
	runs, err := a.ListRecentRuns(ctx, tenantID, 1)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Run{}, false, nil
		}
		return Run{}, false, err
	}
	if len(runs) == 0 {
		return Run{}, false, nil
	}
	return runs[0], true, nil
}

// FormatRuns renders recent sync activity for the status tool.
func FormatRuns(runs []Run) string {
	if len(runs) == 0 {
		return "  calendar sync: no runs recorded yet\n"
	}
	var b strings.Builder
	b.WriteString("  recent calendar syncs:\n")
	for _, r := range runs {
		when := r.CreatedAt.Format("2 Jan 15:04")
		if r.Succeeded() {
			fmt.Fprintf(&b, "    %s (%s): %d created, %d updated, %d removed\n",
				when, r.Meta.TriggeredBy, r.Meta.Created, r.Meta.Updated, r.Meta.Deleted)
		} else {
			fmt.Fprintf(&b, "    %s (%s): failed — %s\n", when, r.Meta.TriggeredBy, r.Meta.Error)
		}
	}
	return b.String()
}
