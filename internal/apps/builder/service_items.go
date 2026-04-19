// Package builder: service_items.go exposes a MongoDB-shaped collection API
// backed by the app_items table. Phase 2f wires these methods into Monty
// scripts via host functions; today they are the Go-side seam callers reach
// from Go directly.
//
// Every method takes a Scope that carries tenant + builder_app + collection
// plus provenance (caller user, optional script run). Scripts never see
// these fields; the runtime populates them before calling us.
package builder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/apps/builder/runtime"
)

// System field names injected into the JSONB data payload. The outer columns
// created_at/updated_at are set by Postgres DEFAULTs; the in-payload copies
// are what scripts actually see.
const (
	sysFieldID        = "_id"
	sysFieldCreatedAt = "_created_at"
	sysFieldUpdatedAt = "_updated_at"
)

// Scope carries the tenant + builder_app + collection triple plus provenance.
// Every ItemService call sets it. Scripts never see these fields — the
// runtime populates them before dispatch.
type Scope struct {
	TenantID     uuid.UUID
	BuilderAppID uuid.UUID
	Collection   string
	CallerUserID uuid.UUID
	// ScriptRunID is nullable; set only when the call comes from inside a
	// running script so history rows can be attributed to that run.
	ScriptRunID *uuid.UUID
}

// validate fails fast when any required field is zero. Collection is a text
// column and the only string identity; tenant + app ids must not be zero.
func (s Scope) validate() error {
	if s.TenantID == uuid.Nil {
		return errors.New("scope.TenantID is required")
	}
	if s.BuilderAppID == uuid.Nil {
		return errors.New("scope.BuilderAppID is required")
	}
	if s.Collection == "" {
		return errors.New("scope.Collection is required")
	}
	if s.CallerUserID == uuid.Nil {
		return errors.New("scope.CallerUserID is required")
	}
	return nil
}

// ItemService exposes the collection API. One instance per process is fine;
// the pool is goroutine-safe.
type ItemService struct {
	pool *pgxpool.Pool
}

// NewItemService binds the service to a pgx pool. Callers typically hold
// this service on the App struct once and share it across goroutines.
func NewItemService(pool *pgxpool.Pool) *ItemService {
	return &ItemService{pool: pool}
}

// InsertOne inserts a single document. Auto-populates _id, _created_at,
// _updated_at inside the data JSONB if absent. Returns the inserted document
// including _id.
func (s *ItemService) InsertOne(ctx context.Context, scope Scope, doc map[string]any) (map[string]any, error) {
	if err := scope.validate(); err != nil {
		return nil, fmt.Errorf("insert: %w", err)
	}
	if doc == nil {
		doc = map[string]any{}
	}

	// Decide the row id: if admin provided _id, honor it (after parsing as a
	// UUID when possible). Otherwise mint a fresh UUID. We store the same
	// value in both the id column and data._id so translator filters on _id
	// resolve against the id column while the returned doc's _id matches.
	rowID, err := resolveInsertID(doc[sysFieldID])
	if err != nil {
		return nil, fmt.Errorf("insert: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	// Copy so we don't mutate the caller's map in surprising ways.
	out := make(map[string]any, len(doc)+3)
	maps.Copy(out, doc)
	out[sysFieldID] = rowID.String()
	if _, ok := out[sysFieldCreatedAt]; !ok {
		out[sysFieldCreatedAt] = now
	}
	if _, ok := out[sysFieldUpdatedAt]; !ok {
		out[sysFieldUpdatedAt] = now
	}

	blob, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("insert: marshalling data: %w", err)
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO app_items
			(id, tenant_id, builder_app_id, collection, data,
			 script_run_id, caller_user_id)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7)
	`, rowID, scope.TenantID, scope.BuilderAppID, scope.Collection, string(blob),
		scope.ScriptRunID, scope.CallerUserID)
	if err != nil {
		return nil, fmt.Errorf("insert: db query: %w", err)
	}
	return out, nil
}

// FindOne returns the first match for the filter, or nil if none. Delegates
// to Find with limit=1 for a single code path.
func (s *ItemService) FindOne(ctx context.Context, scope Scope, filter map[string]any) (map[string]any, error) {
	rows, err := s.Find(ctx, scope, filter, nil, 1, 0)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil //nolint:nilnil // not found is not an error
	}
	return rows[0], nil
}

// Find returns matches. sort is a MongoDB-style sort spec ([]any of
// [field,dir] tuples). limit and skip are optional — pass 0 for no limit and
// no skip. The returned docs have _id / _created_at / _updated_at as they
// were stored in data JSONB.
func (s *ItemService) Find(
	ctx context.Context,
	scope Scope,
	filter map[string]any,
	sort []any,
	limit, skip int,
) ([]map[string]any, error) {
	if err := scope.validate(); err != nil {
		return nil, fmt.Errorf("find: %w", err)
	}
	q, params, err := buildFindQuery(scope, filter, sort, limit, skip)
	if err != nil {
		return nil, fmt.Errorf("find: %w", err)
	}
	rows, err := s.pool.Query(ctx, q, params...)
	if err != nil {
		return nil, fmt.Errorf("find: db query: %w", err)
	}
	defer rows.Close()

	out := make([]map[string]any, 0)
	for rows.Next() {
		var id uuid.UUID
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, fmt.Errorf("find: scan: %w", err)
		}
		doc, err := unpackDoc(id, raw)
		if err != nil {
			return nil, fmt.Errorf("find: %w", err)
		}
		out = append(out, doc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("find: rows: %w", err)
	}
	return out, nil
}

// UpdateOne updates the first row matching filter. Returns the number of
// rows updated (0 or 1). The translator produces a single atomic SET clause,
// so concurrent $push / $inc / $addToSet calls compose correctly under MVCC.
//
// Transaction boundary: we wrap one UPDATE ... WHERE id = (SELECT id ... FOR
// UPDATE LIMIT 1) in a single statement so row-picking + applying the audit
// columns + the mutation all happen atomically. No explicit txn is needed
// because everything is one statement.
func (s *ItemService) UpdateOne(
	ctx context.Context,
	scope Scope,
	filter, update map[string]any,
) (int, error) {
	if err := scope.validate(); err != nil {
		return 0, fmt.Errorf("update: %w", err)
	}

	setClause, updateParams, err := runtime.TranslateUpdate(update, 1)
	if err != nil {
		return 0, fmt.Errorf("update: %w", err)
	}

	q, params, err := buildUpdateOneQuery(scope, filter, setClause, updateParams)
	if err != nil {
		return 0, fmt.Errorf("update: %w", err)
	}

	ct, err := s.pool.Exec(ctx, q, params...)
	if err != nil {
		return 0, fmt.Errorf("update: db query: %w", err)
	}
	return int(ct.RowsAffected()), nil
}

// DeleteOne deletes the first row matching filter. Returns number deleted
// (0 or 1). Two-step: inside a single txn, UPDATE the row's script_run_id
// and caller_user_id to the current scope's values, then DELETE the row.
// That way the history row the trigger writes carries the deleter's
// provenance rather than whoever last mutated the row.
func (s *ItemService) DeleteOne(ctx context.Context, scope Scope, filter map[string]any) (int, error) {
	if err := scope.validate(); err != nil {
		return 0, fmt.Errorf("delete: %w", err)
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("delete: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Resolve the target id under our scope + filter. We do the lookup
	// inside the txn so the UPDATE and DELETE stay consistent.
	rowID, found, err := pickOneID(ctx, tx, scope, filter)
	if err != nil {
		return 0, fmt.Errorf("delete: %w", err)
	}
	if !found {
		// No row matched; commit the empty txn and report 0 affected.
		if err := tx.Commit(ctx); err != nil {
			return 0, fmt.Errorf("delete: commit: %w", err)
		}
		return 0, nil
	}

	// Stamp the deleter's provenance onto the row so the AFTER-DELETE
	// trigger's OLD.* carries it into app_items_history.
	_, err = tx.Exec(ctx, `
		UPDATE app_items
		SET script_run_id = $1, caller_user_id = $2
		WHERE tenant_id = $3 AND builder_app_id = $4 AND collection = $5 AND id = $6
	`, scope.ScriptRunID, scope.CallerUserID, scope.TenantID, scope.BuilderAppID, scope.Collection, rowID)
	if err != nil {
		return 0, fmt.Errorf("delete: stamp audit: %w", err)
	}

	ct, err := tx.Exec(ctx, `
		DELETE FROM app_items
		WHERE tenant_id = $1 AND builder_app_id = $2 AND collection = $3 AND id = $4
	`, scope.TenantID, scope.BuilderAppID, scope.Collection, rowID)
	if err != nil {
		return 0, fmt.Errorf("delete: db query: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("delete: commit: %w", err)
	}
	return int(ct.RowsAffected()), nil
}

// CountDocuments returns the count of matching rows in the scope.
func (s *ItemService) CountDocuments(ctx context.Context, scope Scope, filter map[string]any) (int64, error) {
	if err := scope.validate(); err != nil {
		return 0, fmt.Errorf("count: %w", err)
	}

	where, params, err := buildScopedWhere(scope, filter)
	if err != nil {
		return 0, fmt.Errorf("count: %w", err)
	}

	q := "SELECT COUNT(*) FROM app_items WHERE " + where
	var n int64
	if err := s.pool.QueryRow(ctx, q, params...).Scan(&n); err != nil {
		return 0, fmt.Errorf("count: db query: %w", err)
	}
	return n, nil
}
