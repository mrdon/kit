// Package builder: identity_builtins.go exposes the caller's identity as
// the host function current_user(). Without this, per-user apps would
// have to accept user_id as a tool argument — which a client could
// spoof, so we never want to rely on it. The runtime already threads
// CallerUserID / CallerRoles / CallerTimezone through ScriptRunParams;
// this file is just the Python-side surface.
//
// The exposed surface:
//
//	current_user()
//	# → {"id": "<uuid>", "display_name": "...", "timezone": "...",
//	#    "roles": ["admin", "manager"]}
//
// "admin" and "member" are builtin roles. To check admin status, test
// `"admin" in current_user()["roles"]`.
//
// display_name is looked up from the users table on each call, which is
// a one-row query per invocation. Most scripts never call current_user()
// more than once per run, so we haven't preloaded it into Capabilities.
package builder

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/apps/builder/runtime"
	"github.com/mrdon/kit/internal/models"
)

// Canonical identity builtin names.
const (
	FnCurrentUser = "current_user"
)

// IdentityBuiltins bundles the FuncDefs, dispatcher, and
// Capabilities-ready maps for the identity host functions. Mirrors
// UtilBuiltins — no mutation counters, identity reads never mutate.
type IdentityBuiltins struct {
	Funcs    []runtime.FuncDef
	Handler  runtime.ExternalFunc
	Params   map[string][]string
	BuiltIns map[string]runtime.GoFunc
}

// BuildIdentityBuiltins wires identity host functions for one script run.
//
// The caller's fields (UserID, Roles, Timezone) are captured at assembly
// time and returned verbatim from current_user(); display_name is looked
// up on invocation so we don't pay the query for scripts that never touch
// identity.
func BuildIdentityBuiltins(
	pool *pgxpool.Pool,
	tenantID uuid.UUID,
	callerUserID uuid.UUID,
	callerRoles []string,
	callerTimezone string,
) *IdentityBuiltins {
	handler := func(ctx context.Context, call *runtime.FunctionCall) (any, error) {
		switch call.Name {
		case FnCurrentUser:
			return dispatchCurrentUser(ctx, pool, tenantID, callerUserID, callerRoles, callerTimezone)
		default:
			return nil, fmt.Errorf("identity_builtins: unknown function %q", call.Name)
		}
	}

	params := map[string][]string{
		FnCurrentUser: {},
	}

	funcs := []runtime.FuncDef{
		runtime.Func(FnCurrentUser, params[FnCurrentUser]...),
	}

	builtIns := map[string]runtime.GoFunc{}
	for name := range params {
		builtIns[name] = func(ctx context.Context, call *runtime.FunctionCall) (any, error) {
			if call.Name != name {
				return nil, fmt.Errorf("identity_builtins: name mismatch %q != %q", call.Name, name)
			}
			return handler(ctx, call)
		}
	}

	return &IdentityBuiltins{
		Funcs:    funcs,
		Handler:  handler,
		Params:   params,
		BuiltIns: builtIns,
	}
}

// dispatchCurrentUser returns the caller's identity as a Python dict. The
// returned map always includes a non-nil "roles" list so scripts can do
// `"admin" in current_user()["roles"]` without None-checking.
//
// A nil pool or a zero callerUserID yields display_name "" rather than an
// error — keeps tests that don't wire a DB working, and degrades gracefully
// if the users row was deleted between auth and invocation.
func dispatchCurrentUser(
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID uuid.UUID,
	callerUserID uuid.UUID,
	callerRoles []string,
	callerTimezone string,
) (any, error) {
	displayName := ""
	if pool != nil && callerUserID != uuid.Nil {
		user, err := models.GetUserByID(ctx, pool, tenantID, callerUserID)
		if err != nil {
			return nil, fmt.Errorf("%s: loading caller: %w", FnCurrentUser, err)
		}
		if user != nil && user.DisplayName != nil {
			displayName = *user.DisplayName
		}
	}

	rolesCopy := make([]string, len(callerRoles))
	copy(rolesCopy, callerRoles)

	return map[string]any{
		"id":           callerUserID.String(),
		"display_name": displayName,
		"timezone":     callerTimezone,
		"roles":        rolesCopy,
	}, nil
}
