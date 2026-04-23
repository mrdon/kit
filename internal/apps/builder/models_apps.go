// Package builder holds the admin-facing "scriptable app substrate" that lets
// admins build tenant-scoped apps via Claude Code + Kit's MCP with no engineer
// in the loop. This file contains the data models for the builder-app bundle
// and the MongoDB-shaped item store.
package builder

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// BuilderApp is a logical bundle (CRM, mug_club, review_triage, ...) owned by
// a tenant. Scripts, schedules, exposed tools, and collections all belong to
// a builder app; two apps in the same tenant can share a collection name
// because item scope is (tenant_id, builder_app_id, collection).
type BuilderApp struct {
	ID          uuid.UUID  `json:"id"          db:"id"`
	TenantID    uuid.UUID  `json:"tenant_id"   db:"tenant_id"`
	Name        string     `json:"name"        db:"name"`
	Description string     `json:"description,omitempty" db:"description"`
	CreatedBy   *uuid.UUID `json:"created_by,omitempty"  db:"created_by"`
	CreatedAt   time.Time  `json:"created_at"  db:"created_at"`
}

// AppItem is one document in the MongoDB-shaped item store. Collections are
// logical strings (admin-chosen) keyed under (tenant_id, builder_app_id). The
// document body lives in Data as JSONB; system fields (_id, _created_at,
// _updated_at) are injected by the runtime.
type AppItem struct {
	ID           uuid.UUID       `json:"id"              db:"id"`
	TenantID     uuid.UUID       `json:"tenant_id"       db:"tenant_id"`
	BuilderAppID uuid.UUID       `json:"builder_app_id"  db:"builder_app_id"`
	Collection   string          `json:"collection"      db:"collection"`
	Data         json.RawMessage `json:"data"            db:"data"`
	CreatedBy    *uuid.UUID      `json:"created_by,omitempty"     db:"created_by"`
	CreatedAt    time.Time       `json:"created_at"      db:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"      db:"updated_at"`
	ScriptRunID  *uuid.UUID      `json:"script_run_id,omitempty"  db:"script_run_id"`
	CallerUserID *uuid.UUID      `json:"caller_user_id,omitempty" db:"caller_user_id"`
}

// AppItemHistory is the temporal shadow row for AppItem. The PL/pgSQL trigger
// on app_items AFTER UPDATE OR DELETE inserts the prior state here with
// valid_from = max(OLD.created_at, OLD.updated_at) and valid_to = now().
// app_rollback_script_run replays these rows back into app_items.
type AppItemHistory struct {
	HistoryID    int64           `json:"history_id"     db:"history_id"`
	ID           uuid.UUID       `json:"id"             db:"id"`
	TenantID     uuid.UUID       `json:"tenant_id"      db:"tenant_id"`
	BuilderAppID uuid.UUID       `json:"builder_app_id" db:"builder_app_id"`
	Collection   string          `json:"collection"     db:"collection"`
	Data         json.RawMessage `json:"data"           db:"data"`
	Operation    string          `json:"operation"      db:"operation"` // 'UPDATE' | 'DELETE'
	ValidFrom    time.Time       `json:"valid_from"     db:"valid_from"`
	ValidTo      time.Time       `json:"valid_to"       db:"valid_to"`
	ScriptRunID  *uuid.UUID      `json:"script_run_id,omitempty"    db:"script_run_id"`
	CallerUserID *uuid.UUID      `json:"caller_user_id,omitempty"   db:"caller_user_id"`
}

// Operation values for AppItemHistory.Operation.
const (
	HistoryOpUpdate = "UPDATE"
	HistoryOpDelete = "DELETE"
)

// TenantBuilderConfig holds per-tenant knobs for the builder runtime.
// HistoryRetentionDays = nil means unlimited (v0.1 default); the prune job is
// v0.2.
type TenantBuilderConfig struct {
	TenantID             uuid.UUID `json:"tenant_id"               db:"tenant_id"`
	HistoryRetentionDays *int      `json:"history_retention_days,omitempty" db:"history_retention_days"`
	MaxDBCallsPerRun     int       `json:"max_db_calls_per_run"    db:"max_db_calls_per_run"`
	LLMDailyCentCap      int       `json:"llm_daily_cent_cap"      db:"llm_daily_cent_cap"`
	UpdatedAt            time.Time `json:"updated_at"              db:"updated_at"`
}
