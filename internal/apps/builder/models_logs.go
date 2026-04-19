package builder

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Script log levels.
const (
	LogLevelDebug = "debug"
	LogLevelInfo  = "info"
	LogLevelWarn  = "warn"
	LogLevelError = "error"
)

// LLM fn values (classify/extract/summarize/generate).
const (
	LLMFnClassify  = "classify"
	LLMFnExtract   = "extract"
	LLMFnSummarize = "summarize"
	LLMFnGenerate  = "generate"
)

// ScriptLog is a single log line emitted during a script run via the runtime's
// log(level, msg, **fields) built-in. Queried by admins via the script_logs
// meta-tool.
type ScriptLog struct {
	ID          int64           `json:"id"             db:"id"`
	TenantID    uuid.UUID       `json:"tenant_id"      db:"tenant_id"`
	ScriptRunID uuid.UUID       `json:"script_run_id"  db:"script_run_id"`
	Level       string          `json:"level"          db:"level"`
	Message     string          `json:"message"        db:"message"`
	Fields      json.RawMessage `json:"fields,omitempty" db:"fields"`
	CreatedAt   time.Time       `json:"created_at"     db:"created_at"`
}

// LLMCallLog records every llm.* invocation: structured call log now, cache
// lookup target later (v0.2+). ScriptRunID is SET NULL on run delete so the
// cost record survives.
type LLMCallLog struct {
	ID          uuid.UUID       `json:"id"             db:"id"`
	TenantID    uuid.UUID       `json:"tenant_id"      db:"tenant_id"`
	ScriptRunID *uuid.UUID      `json:"script_run_id,omitempty" db:"script_run_id"`
	Fn          string          `json:"fn"             db:"fn"`
	ModelTier   string          `json:"model_tier"     db:"model_tier"`
	ArgsHash    string          `json:"args_hash"      db:"args_hash"`
	ArgsPayload json.RawMessage `json:"args_payload,omitempty"  db:"args_payload"`
	Result      json.RawMessage `json:"result,omitempty"        db:"result"`
	TokensIn    *int            `json:"tokens_in,omitempty"     db:"tokens_in"`
	TokensOut   *int            `json:"tokens_out,omitempty"    db:"tokens_out"`
	CostCents   *int            `json:"cost_cents,omitempty"    db:"cost_cents"`
	CreatedAt   time.Time       `json:"created_at"     db:"created_at"`
}
