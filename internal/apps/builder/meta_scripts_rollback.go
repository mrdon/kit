// Package builder: meta_scripts_rollback.go implements app_rollback_script_run
// — the meta-tool that undoes the mutations a script run made against
// app_items using the temporal history table populated by the
// app_items_history_record trigger (see migration 017_app_builder.sql).
//
// Trigger semantics recap (important for getting rollback right):
//
//	AFTER UPDATE OR DELETE: INSERT a history row using OLD.* values.
//	That means the history row's script_run_id is the row's PRIOR
//	script_run_id, NOT the one doing this mutation. The row's CURRENT
//	script_run_id (the one doing the write) lands in app_items.
//
// Consequence: a row updated by run R has app_items.script_run_id=R and
// a history row with operation='UPDATE' whose script_run_id is whatever
// the row had before R touched it (often NULL).
//
// DeleteOne() stamps script_run_id=R on the row before deleting it, so
// the DELETE history row DOES carry script_run_id=R (because the UPDATE
// to stamp + the DELETE both fire the trigger, and the post-stamp OLD is
// what the DELETE trigger sees).
//
// Rollback strategy, implemented as three phases inside one transaction:
//
//  1. Pure inserts: rows in app_items with script_run_id=R AND no history
//     at all for that id → DELETE them (they didn't exist before R).
//  2. Updates: rows in app_items with script_run_id=R AND at least one
//     history row for that id → restore to the OLDEST in-run history
//     state (valid_from ascending). That's the pre-mutation snapshot.
//     We use `app_items.script_run_id=R` as the "this run touched this
//     row" signal because history.script_run_id doesn't reflect R for
//     UPDATEs.
//  3. Deletes: rows present in history as operation='DELETE' with
//     script_run_id=R → INSERT back using the history data. These are
//     the rows the run deleted outright.
//
// Subtleties:
//   - The rollback itself emits history rows (as any UPDATE/DELETE does).
//     The caller can roll back the rollback by targeting the rollback's
//     own script_run_id. The rollback INSERTs (phase 3) do NOT emit
//     history — matching the original semantics (inserts don't trigger).
//   - We don't try to invoke business-level side effects (no re-firing
//     create_todo on rollback). Rollback is a data-level repair, not a
//     semantic undo.
package builder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/services"
)

// rollbackResponse is the JSON shape app_rollback_script_run returns. The
// three counters (deleted/restored/reinserted) are separate so the caller
// can tell "we deleted 3 inserts" apart from "we restored 3 updates".
type rollbackResponse struct {
	RolledBack int       `json:"rolled_back"`
	Deleted    int       `json:"deleted"`
	Restored   int       `json:"restored"`
	Reinserted int       `json:"reinserted"`
	RunID      uuid.UUID `json:"run_id"`
}

func handleRollbackScriptRun(ec *execContextLike, input json.RawMessage) (string, error) {
	m, err := parseInput(input)
	if err != nil {
		return "", err
	}
	if err := requireConfirm(m); err != nil {
		return "", err
	}
	runIDStr, err := argString(m, "run_id")
	if err != nil {
		return "", err
	}
	runID, err := uuid.Parse(runIDStr)
	if err != nil {
		return "", fmt.Errorf("run_id must be a UUID: %w", err)
	}

	resp, err := rollbackScriptRun(ec.Ctx, ec.Pool, ec.Caller, runID)
	if err != nil {
		return "", err
	}
	return formatToolResult(resp)
}

// rollbackScriptRun replays the run's mutations in reverse. Admin-only.
// All three phases execute inside one transaction so we never leave the
// DB half-rolled-back on error.
func rollbackScriptRun(
	ctx context.Context,
	pool *pgxpool.Pool,
	caller *services.Caller,
	runID uuid.UUID,
) (*rollbackResponse, error) {
	if err := guardAdmin(caller); err != nil {
		return nil, err
	}

	// Verify the run exists and belongs to this tenant up-front so the
	// error path is a clean "not found" rather than a silent zero-row
	// rollback.
	var status string
	err := pool.QueryRow(ctx, `
		SELECT status FROM script_runs WHERE tenant_id = $1 AND id = $2
	`, caller.TenantID, runID).Scan(&status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("script_run %s not found", runID)
		}
		return nil, fmt.Errorf("loading script_run: %w", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("starting tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Capture the set of ids this run mutated-or-inserted BEFORE we start
	// mucking with app_items. We need it to split "pure insert" from
	// "updated an existing row" before the UPDATE-restore phase rewrites
	// script_run_id.
	touchedIDs, err := loadTouchedIDs(ctx, tx, caller.TenantID, runID)
	if err != nil {
		return nil, err
	}

	// Phase 1: delete rows the run inserted. A pure insert is a row with
	// script_run_id=runID AND zero history entries — an insert never
	// fires the trigger, so subsequent updates to that row within the
	// same run would have produced history. If there's no history, it
	// was a clean insert.
	deleted, err := rollbackDeleteInserts(ctx, tx, caller.TenantID, touchedIDs)
	if err != nil {
		return nil, err
	}

	// Phase 2: restore updated rows to their pre-run state using the
	// OLDEST in-run history entry. The history row for an UPDATE carries
	// the OLD row's data — i.e. the state BEFORE that update. So the
	// oldest UPDATE history with script_run_id=<prior, typically NULL>
	// that was produced DURING this run (same transaction view) is the
	// pre-run state.
	//
	// We can't filter history rows by script_run_id=runID for updates
	// (see module comment — they carry the PRIOR script_run_id), so we
	// join through app_items.script_run_id=runID to find the ids, then
	// pick the oldest history entry per id regardless of the history
	// row's own script_run_id.
	restored, err := rollbackRestoreUpdates(ctx, tx, caller.TenantID, runID)
	if err != nil {
		return nil, err
	}

	// Phase 3: re-insert rows the run deleted. Those ARE tagged
	// script_run_id=runID in history (DeleteOne stamps the row before
	// the DELETE fires) so a simple filter works.
	reinserted, err := rollbackReinsertDeletes(ctx, tx, caller.TenantID, runID)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing rollback: %w", err)
	}

	return &rollbackResponse{
		RolledBack: deleted + restored + reinserted,
		Deleted:    deleted,
		Restored:   restored,
		Reinserted: reinserted,
		RunID:      runID,
	}, nil
}

// loadTouchedIDs returns the ids of rows currently in app_items whose
// script_run_id matches this run. We read this BEFORE modifying anything
// so phase 1's "had no history before the run" check can distinguish
// pure inserts from updated rows.
func loadTouchedIDs(
	ctx context.Context,
	tx pgx.Tx,
	tenantID, runID uuid.UUID,
) (map[uuid.UUID]bool, error) {
	rows, err := tx.Query(ctx, `
		SELECT id FROM app_items
		WHERE tenant_id = $1 AND script_run_id = $2
	`, tenantID, runID)
	if err != nil {
		return nil, fmt.Errorf("loading touched ids: %w", err)
	}
	defer rows.Close()
	out := map[uuid.UUID]bool{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scanning touched id: %w", err)
		}
		out[id] = true
	}
	return out, rows.Err()
}

// rollbackDeleteInserts deletes every app_items row this run inserted
// (script_run_id=runID AND no prior history). The NOT IN subquery is the
// cheap way to express "id has no history rows of any kind" — because
// inserts don't emit history, a row with no history entries is by
// definition a pure insert.
func rollbackDeleteInserts(
	ctx context.Context,
	tx pgx.Tx,
	tenantID uuid.UUID,
	touchedIDs map[uuid.UUID]bool,
) (int, error) {
	if len(touchedIDs) == 0 {
		return 0, nil
	}
	ids := make([]uuid.UUID, 0, len(touchedIDs))
	for id := range touchedIDs {
		ids = append(ids, id)
	}
	ct, err := tx.Exec(ctx, `
		DELETE FROM app_items
		WHERE tenant_id = $1
		  AND id = ANY($2::uuid[])
		  AND NOT EXISTS (
		      SELECT 1 FROM app_items_history h
		      WHERE h.tenant_id = $1 AND h.id = app_items.id
		  )
	`, tenantID, ids)
	if err != nil {
		return 0, fmt.Errorf("rolling back inserts: %w", err)
	}
	return int(ct.RowsAffected()), nil
}

// rollbackRestoreUpdates walks every row currently stamped
// script_run_id=runID that ALSO has history (i.e. it existed before the
// run and this run updated it), and restores its data to the oldest
// in-run history entry. "In-run" is identified by
// history.valid_from BETWEEN started_at AND finished_at of the run.
//
// Why not filter history rows by script_run_id=runID? Because for plain
// UPDATEs the history row carries the PRIOR script_run_id (often NULL),
// not the runID that did the update. The valid_from range check is the
// right discriminator.
func rollbackRestoreUpdates(
	ctx context.Context,
	tx pgx.Tx,
	tenantID, runID uuid.UUID,
) (int, error) {
	// Use valid_to (when the mutation happened) instead of valid_from
	// (when the OLD state started). valid_from reflects the row's
	// created_at or updated_at BEFORE the run touched it, so it's
	// always before the run started for rows that existed pre-run.
	// valid_to is the mutation timestamp set by the trigger's DEFAULT
	// now() clause.
	rows, err := tx.Query(ctx, `
		WITH run_window AS (
		    SELECT started_at,
		           COALESCE(finished_at, now()) AS finished_at
		    FROM script_runs
		    WHERE tenant_id = $1 AND id = $2
		),
		in_run_history AS (
		    SELECT h.id, h.data, h.valid_from, h.valid_to
		    FROM app_items_history h
		    JOIN run_window rw ON h.valid_to >= rw.started_at
		                      AND h.valid_to <= rw.finished_at
		    JOIN app_items ai ON ai.id = h.id AND ai.tenant_id = h.tenant_id
		    WHERE h.tenant_id = $1
		      AND ai.script_run_id = $2
		),
		oldest_per_id AS (
		    SELECT DISTINCT ON (id) id, data
		    FROM in_run_history
		    ORDER BY id, valid_to ASC
		)
		SELECT id, data FROM oldest_per_id
	`, tenantID, runID)
	if err != nil {
		return 0, fmt.Errorf("loading updates to restore: %w", err)
	}
	defer rows.Close()

	type restore struct {
		id   uuid.UUID
		data []byte
	}
	var pending []restore
	for rows.Next() {
		var r restore
		if err := rows.Scan(&r.id, &r.data); err != nil {
			return 0, fmt.Errorf("scanning update: %w", err)
		}
		pending = append(pending, r)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterating updates: %w", err)
	}

	restored := 0
	for _, r := range pending {
		ct, err := tx.Exec(ctx, `
			UPDATE app_items
			SET data = $1, updated_at = now(), script_run_id = NULL
			WHERE tenant_id = $2 AND id = $3
		`, r.data, tenantID, r.id)
		if err != nil {
			return 0, fmt.Errorf("restoring id=%s: %w", r.id, err)
		}
		if ct.RowsAffected() > 0 {
			restored++
		}
	}
	return restored, nil
}

// rollbackReinsertDeletes re-inserts every row the run deleted, using the
// DELETE-operation history rows this run produced. DeleteOne() stamps
// script_run_id=runID on the row BEFORE firing the DELETE, so the DELETE
// trigger captures OLD.script_run_id=runID — which is how we find them.
//
// If the history has multiple DELETE rows for the same id (shouldn't
// happen — a deleted row can't be deleted again without first being
// re-inserted), we pick the newest so the latest state wins.
func rollbackReinsertDeletes(
	ctx context.Context,
	tx pgx.Tx,
	tenantID, runID uuid.UUID,
) (int, error) {
	// Constrain the DELETE history rows to valid_to within the run's
	// execution window. Otherwise phase 1's own in-transaction DELETE
	// of pure-insert rows (gamma/delta) — which fires the trigger and
	// produces a DELETE history row whose OLD.script_run_id=$runID —
	// would be caught here and immediately "re-inserted", undoing
	// phase 1's work.
	rows, err := tx.Query(ctx, `
		WITH run_window AS (
		    SELECT started_at,
		           COALESCE(finished_at, now()) AS finished_at
		    FROM script_runs
		    WHERE tenant_id = $1 AND id = $2
		)
		SELECT DISTINCT ON (h.id)
		    h.id, h.builder_app_id, h.collection, h.data, h.caller_user_id
		FROM app_items_history h
		JOIN run_window rw ON h.valid_to >= rw.started_at
		                   AND h.valid_to <= rw.finished_at
		WHERE h.tenant_id = $1
		  AND h.script_run_id = $2
		  AND h.operation = 'DELETE'
		ORDER BY h.id, h.valid_to DESC
	`, tenantID, runID)
	if err != nil {
		return 0, fmt.Errorf("loading deletes: %w", err)
	}
	defer rows.Close()

	type reinsert struct {
		id           uuid.UUID
		builderAppID uuid.UUID
		collection   string
		data         []byte
		callerUserID *uuid.UUID
	}
	var pending []reinsert
	for rows.Next() {
		var r reinsert
		if err := rows.Scan(&r.id, &r.builderAppID, &r.collection, &r.data, &r.callerUserID); err != nil {
			return 0, fmt.Errorf("scanning delete: %w", err)
		}
		pending = append(pending, r)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterating deletes: %w", err)
	}

	reinserted := 0
	for _, r := range pending {
		_, err := tx.Exec(ctx, `
			INSERT INTO app_items (
			    id, tenant_id, builder_app_id, collection, data,
			    created_by, script_run_id
			) VALUES ($1, $2, $3, $4, $5, $6, NULL)
			ON CONFLICT (id) DO NOTHING
		`, r.id, tenantID, r.builderAppID, r.collection, r.data, r.callerUserID)
		if err != nil {
			return 0, fmt.Errorf("reinserting id=%s: %w", r.id, err)
		}
		reinserted++
	}
	return reinserted, nil
}
