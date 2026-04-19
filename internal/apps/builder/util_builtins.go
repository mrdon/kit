// Package builder: util_builtins.go wires utility host functions (date/time
// helpers and structured logging) into the Monty runtime, following the
// same FuncDef/dispatcher pattern as db_builtins.go.
//
// The rationale for ISO8601 strings everywhere on the date/time side:
//   - Round-trip through the WASM/JSON boundary as strings (Monty's bridge
//     has no Python datetime type).
//   - Sort lexically (YYYY-MM-DDTHH:MM:SS is monotonic).
//   - Admins can slice dt[:10] for the date, dt[:4] for the year.
//
// The exposed surface:
//
//	now()                                               # UTC RFC3339Nano
//	today()                                             # YYYY-MM-DD in caller tz
//	date_add(dt, days=, hours=, minutes=, seconds=)     # shifted RFC3339Nano
//	date_diff(a, b)                                     # a-b seconds, float
//	log(level, message, **fields)                       # INSERT into script_logs
package builder

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/apps/builder/runtime"
)

// Canonical utility builtin names.
const (
	FnNow      = "now"
	FnToday    = "today"
	FnDateAdd  = "date_add"
	FnDateDiff = "date_diff"
	FnLog      = "log"
)

// UtilBuiltins bundles the FuncDefs, dispatcher, and Capabilities-ready maps
// for the utility host functions. Mirrors DBBuiltins but has no mutation
// counters — utilities do not mutate business data.
type UtilBuiltins struct {
	// Funcs is the ordered list of FuncDefs, ready to hand to
	// runtime.WithExternalFunc for raw Runner use.
	Funcs []runtime.FuncDef

	// Handler dispatches every utility call to its implementation.
	Handler runtime.ExternalFunc

	// Params maps each function name to its positional parameter names.
	// Feed this into runtime.Capabilities.BuiltInParams.
	Params map[string][]string

	// BuiltIns maps each function name to a thin wrapper around Handler.
	// Feed this into runtime.Capabilities.BuiltIns.
	BuiltIns map[string]runtime.GoFunc
}

// BuildUtilBuiltins wires the utility host functions for one script run.
//
// callerTimezone is the IANA tz used by today(); invalid/empty names fall
// back to UTC. runID is nil outside of a tracked run; in that case log()
// writes to slog (no DB write) so utility output isn't lost but doesn't
// break scripts that the caller hasn't wired a run for.
func BuildUtilBuiltins(
	pool *pgxpool.Pool,
	tenantID uuid.UUID,
	runID *uuid.UUID,
	callerTimezone string,
) *UtilBuiltins {
	// Resolve the caller's tz once up front. time.LoadLocation on "" or an
	// unknown name returns (nil, err); we fall back to UTC so today() is
	// never a script-killing error.
	loc := resolveLocation(callerTimezone)

	handler := func(ctx context.Context, call *runtime.FunctionCall) (any, error) {
		switch call.Name {
		case FnNow:
			return dispatchNow(), nil
		case FnToday:
			return dispatchToday(loc), nil
		case FnDateAdd:
			return dispatchDateAdd(call)
		case FnDateDiff:
			return dispatchDateDiff(call)
		case FnLog:
			return dispatchLog(ctx, pool, tenantID, runID, call)
		default:
			return nil, fmt.Errorf("util_builtins: unknown function %q", call.Name)
		}
	}

	params := map[string][]string{
		FnNow:      {},
		FnToday:    {},
		FnDateAdd:  {"dt", "days", "hours", "minutes", "seconds"},
		FnDateDiff: {"a", "b"},
		FnLog:      {"level", "message"},
	}

	// FuncDefs in stable order for deterministic registration logs.
	funcs := []runtime.FuncDef{
		runtime.Func(FnDateAdd, params[FnDateAdd]...),
		runtime.Func(FnDateDiff, params[FnDateDiff]...),
		runtime.Func(FnLog, params[FnLog]...),
		runtime.Func(FnNow, params[FnNow]...),
		runtime.Func(FnToday, params[FnToday]...),
	}

	builtIns := map[string]runtime.GoFunc{}
	for name := range params {
		builtIns[name] = func(ctx context.Context, call *runtime.FunctionCall) (any, error) {
			if call.Name != name {
				return nil, fmt.Errorf("util_builtins: name mismatch %q != %q", call.Name, name)
			}
			return handler(ctx, call)
		}
	}

	return &UtilBuiltins{
		Funcs:    funcs,
		Handler:  handler,
		Params:   params,
		BuiltIns: builtIns,
	}
}

// resolveLocation parses tz via time.LoadLocation, returning UTC on empty
// or unknown names.
func resolveLocation(tz string) *time.Location {
	if tz == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.UTC
	}
	return loc
}

// dispatchNow returns UTC ISO8601 down to the nanosecond.
func dispatchNow() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

// dispatchToday returns a YYYY-MM-DD string in the caller's timezone.
func dispatchToday(loc *time.Location) string {
	return time.Now().In(loc).Format("2006-01-02")
}

// dispatchDateAdd parses dt, adds the supplied offsets, and returns the
// shifted timestamp in the same timezone the input was expressed in.
func dispatchDateAdd(call *runtime.FunctionCall) (any, error) {
	dtStr, err := argString(call.Args, "dt")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	base, err := parseRFC3339(dtStr)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}

	days, err := argOptionalFloat(call.Args, "days")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	hours, err := argOptionalFloat(call.Args, "hours")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	minutes, err := argOptionalFloat(call.Args, "minutes")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	seconds, err := argOptionalFloat(call.Args, "seconds")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}

	// Build the delta with float precision, then convert to a Duration. We
	// keep the computation in float64 seconds so fractional days/hours work.
	deltaSecs := days*86400 + hours*3600 + minutes*60 + seconds
	d := time.Duration(deltaSecs * float64(time.Second))
	return base.Add(d).Format(time.RFC3339Nano), nil
}

// dispatchDateDiff returns (a - b) in seconds as float64.
func dispatchDateDiff(call *runtime.FunctionCall) (any, error) {
	aStr, err := argString(call.Args, "a")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	bStr, err := argString(call.Args, "b")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	a, err := parseRFC3339(aStr)
	if err != nil {
		return nil, fmt.Errorf("%s: parsing a: %w", call.Name, err)
	}
	b, err := parseRFC3339(bStr)
	if err != nil {
		return nil, fmt.Errorf("%s: parsing b: %w", call.Name, err)
	}
	return a.Sub(b).Seconds(), nil
}

// dispatchLog inserts a single row into script_logs when runID is set; when
// runID is nil it drops the line onto slog so we don't lose the signal but
// don't violate the script_logs NOT NULL constraint on script_run_id.
func dispatchLog(
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID uuid.UUID,
	runID *uuid.UUID,
	call *runtime.FunctionCall,
) (any, error) {
	level, err := argString(call.Args, "level")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	if !isValidLogLevel(level) {
		return nil, fmt.Errorf("%s: invalid level %q, want one of debug/info/warn/error", call.Name, level)
	}
	message, err := argString(call.Args, "message")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}

	// Anything that isn't the two named params is a structured field. Copy
	// so we don't mutate the caller's map.
	fields := map[string]any{}
	for k, v := range call.Args {
		if k == "level" || k == "message" {
			continue
		}
		fields[k] = v
	}

	if runID == nil {
		// Fallback path: no script_run to attribute the log to, so the DB
		// INSERT would violate the NOT NULL on script_run_id. Writing to
		// slog keeps the line visible to operators without making log()
		// a script-killing error.
		slog.Info("builder.script.log (no run)",
			"tenant_id", tenantID,
			"level", level,
			"message", message,
			"fields", fields,
		)
		return nil, nil //nolint:nilnil // log() returns None
	}

	var fieldsJSON []byte
	if len(fields) > 0 {
		fieldsJSON, err = json.Marshal(fields)
		if err != nil {
			return nil, fmt.Errorf("%s: marshalling fields: %w", call.Name, err)
		}
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO script_logs (tenant_id, script_run_id, level, message, fields)
		VALUES ($1, $2, $3, $4, $5)
	`, tenantID, *runID, level, message, fieldsJSON)
	if err != nil {
		return nil, fmt.Errorf("%s: db query: %w", call.Name, err)
	}
	return nil, nil //nolint:nilnil // log() returns None
}

// isValidLogLevel mirrors the CHECK constraint on script_logs.level.
func isValidLogLevel(level string) bool {
	switch level {
	case LogLevelDebug, LogLevelInfo, LogLevelWarn, LogLevelError:
		return true
	}
	return false
}

// parseRFC3339 accepts both RFC3339Nano and RFC3339, returning a clear error
// when neither layout matches.
func parseRFC3339(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("cannot parse %q as RFC3339", s)
}

// argOptionalFloat coerces a numeric arg to float64. Absent or None is 0.
// JSON numbers always arrive as float64 from the Monty bridge.
func argOptionalFloat(args map[string]any, name string) (float64, error) {
	raw, ok := args[name]
	if !ok || raw == nil {
		return 0, nil
	}
	f, ok := raw.(float64)
	if !ok {
		return 0, fmt.Errorf("argument %q must be a number or None, got %T", name, raw)
	}
	return f, nil
}
