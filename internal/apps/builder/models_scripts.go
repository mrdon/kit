package builder

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// ScriptRun status values.
const (
	RunStatusRunning       = "running"
	RunStatusCompleted     = "completed"
	RunStatusError         = "error"
	RunStatusLimitExceeded = "limit_exceeded"
	RunStatusCancelled     = "cancelled"
)

// ScriptRun triggered_by values.
const (
	TriggerManual      = "manual"
	TriggerSchedule    = "schedule"
	TriggerExposedTool = "exposed_tool"
	TriggerToolsCall   = "tools_call"
)

// Script is a named script owned by a builder app. CurrentRevID points at the
// active revision and is initially NULL on creation (resolved once the first
// ScriptRevision row exists). Identity is (tenant_id, builder_app_id, name).
type Script struct {
	ID           uuid.UUID  `json:"id"              db:"id"`
	TenantID     uuid.UUID  `json:"tenant_id"       db:"tenant_id"`
	BuilderAppID uuid.UUID  `json:"builder_app_id"  db:"builder_app_id"`
	Name         string     `json:"name"            db:"name"`
	Description  string     `json:"description,omitempty"    db:"description"`
	CurrentRevID *uuid.UUID `json:"current_rev_id,omitempty" db:"current_rev_id"`
	CreatedBy    *uuid.UUID `json:"created_by,omitempty"     db:"created_by"`
	CreatedAt    time.Time  `json:"created_at"      db:"created_at"`
}

// ScriptRevision is an immutable snapshot of a script's body. Every
// app_create_script / app_update_script appends one. Runs record the exact revision
// they executed so rollback + audit are exact.
type ScriptRevision struct {
	ID        uuid.UUID  `json:"id"         db:"id"`
	ScriptID  uuid.UUID  `json:"script_id"  db:"script_id"`
	Body      string     `json:"body"       db:"body"`
	CreatedBy *uuid.UUID `json:"created_by,omitempty" db:"created_by"`
	CreatedAt time.Time  `json:"created_at" db:"created_at"`
}

// MutationSummary is stored in ScriptRun.MutationSummary so "what did that
// script do" is visible without scanning history rows.
type MutationSummary struct {
	Inserts int `json:"inserts"`
	Updates int `json:"updates"`
	Deletes int `json:"deletes"`
}

// ScriptRun records one invocation of a script function. Chained calls via
// tools.call populate ParentRunID so the full audit chain is reconstructable.
type ScriptRun struct {
	ID              uuid.UUID        `json:"id"                db:"id"`
	TenantID        uuid.UUID        `json:"tenant_id"         db:"tenant_id"`
	ScriptID        uuid.UUID        `json:"script_id"         db:"script_id"`
	RevisionID      uuid.UUID        `json:"revision_id"       db:"revision_id"`
	FnCalled        string           `json:"fn_called,omitempty"         db:"fn_called"`
	Args            json.RawMessage  `json:"args,omitempty"              db:"args"`
	Result          json.RawMessage  `json:"result,omitempty"            db:"result"`
	Status          string           `json:"status"            db:"status"`
	StartedAt       time.Time        `json:"started_at"        db:"started_at"`
	FinishedAt      *time.Time       `json:"finished_at,omitempty"       db:"finished_at"`
	DurationMS      *int64           `json:"duration_ms,omitempty"       db:"duration_ms"`
	TokensUsed      *int             `json:"tokens_used,omitempty"       db:"tokens_used"`
	CostCents       *int             `json:"cost_cents,omitempty"        db:"cost_cents"`
	Error           string           `json:"error,omitempty"             db:"error"`
	TriggeredBy     string           `json:"triggered_by,omitempty"      db:"triggered_by"`
	ParentRunID     *uuid.UUID       `json:"parent_run_id,omitempty"     db:"parent_run_id"`
	MutationSummary *MutationSummary `json:"mutation_summary,omitempty"  db:"mutation_summary"`
	CallerUserID    *uuid.UUID       `json:"caller_user_id,omitempty"    db:"caller_user_id"`
}

// ScheduledScript is a cron-scheduled script function. The scheduler branch
// resolves the script's current revision at claim time and re-checks the
// creator's admin status (demoted admin's scripts lose privilege immediately).
type ScheduledScript struct {
	ID        uuid.UUID `json:"id"           db:"id"`
	TenantID  uuid.UUID `json:"tenant_id"    db:"tenant_id"`
	ScriptID  uuid.UUID `json:"script_id"    db:"script_id"`
	FnName    string    `json:"fn_name"      db:"fn_name"`
	Cron      string    `json:"cron"         db:"cron"`
	Timezone  string    `json:"timezone"     db:"timezone"`
	NextRunAt time.Time `json:"next_run_at"  db:"next_run_at"`
	Active    bool      `json:"active"       db:"active"`
}

// ExposedTool is an admin-published script function surfaced as a regular
// LLM/MCP tool. Tenant-scoped (not app-scoped) so composition via tools.call
// works across apps. IsStale is set at invocation time if the backing script
// or fn has vanished; audit rows survive.
type ExposedTool struct {
	ID             uuid.UUID       `json:"id"               db:"id"`
	TenantID       uuid.UUID       `json:"tenant_id"        db:"tenant_id"`
	ToolName       string          `json:"tool_name"        db:"tool_name"`
	ScriptID       uuid.UUID       `json:"script_id"        db:"script_id"`
	FnName         string          `json:"fn_name"          db:"fn_name"`
	Description    string          `json:"description,omitempty"       db:"description"`
	ArgsSchema     json.RawMessage `json:"args_schema,omitempty"       db:"args_schema"`
	VisibleToRoles []string        `json:"visible_to_roles,omitempty"  db:"visible_to_roles"`
	IsStale        bool            `json:"is_stale"         db:"is_stale"`
	CreatedBy      *uuid.UUID      `json:"created_by,omitempty"        db:"created_by"`
	CreatedAt      time.Time       `json:"created_at"       db:"created_at"`
}
