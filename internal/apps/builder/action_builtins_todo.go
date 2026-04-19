// Package builder: action_builtins_todo.go dispatches create_todo /
// update_todo / complete_todo / add_todo_comment into TodoService.
//
// Each dispatcher mirrors the shape of the agent-tool handler in
// internal/apps/todo/agent.go but returns a dict the script can inspect
// instead of the human-readable "Created todo […]" string.
package builder

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/builder/runtime"
	"github.com/mrdon/kit/internal/apps/todo"
	"github.com/mrdon/kit/internal/services"
)

// dispatchCreateTodo handles create_todo(title, description="",
// priority="medium", due_date=None, role_scope=None, assigned_to=None,
// private=None) → todo dict.
//
// The `private` kwarg maps onto the new Visibility field as follows:
//   - private=True  → Visibility="scoped" (default; scope-only visibility)
//   - private=False → Visibility="public" (everyone in tenant can read)
//   - omitted       → Visibility="" (service applies "scoped" default)
func dispatchCreateTodo(ctx context.Context, a *ActionBuiltins, deps *actionDeps, call *runtime.FunctionCall) (any, error) {
	title, err := argString(call.Args, "title")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	description, err := argOptionalString(call.Args, "description")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	priority, err := argOptionalString(call.Args, "priority")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	if priority == "" {
		priority = "medium"
	}
	dueDate, err := argOptionalDate(call.Args, "due_date")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	roleScope, err := argOptionalString(call.Args, "role_scope")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	assignedTo, err := argOptionalString(call.Args, "assigned_to")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}

	// Visibility: translate the legacy `private` bool. Only set it when
	// the script passed the kwarg explicitly — otherwise leave empty so
	// the service picks its own default ("scoped").
	var visibility string
	if _, present := call.Args["private"]; present {
		private, err := argOptionalBool(call.Args, "private")
		if err != nil {
			return nil, fmt.Errorf("%s: %w", call.Name, err)
		}
		if private {
			visibility = "scoped"
		} else {
			visibility = "public"
		}
	}

	c, err := deps.caller(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}

	in := todo.CreateInput{
		Title:       title,
		Description: description,
		Priority:    priority,
		RoleName:    roleScope,
		Visibility:  visibility,
		DueDate:     dueDate,
	}

	if assignedTo != "" {
		id, msg := deps.todoSvc.ResolveAssignee(ctx, c, assignedTo)
		if msg != "" {
			return nil, fmt.Errorf("%s: %s", call.Name, msg)
		}
		in.AssignedTo = id
	}

	t, err := deps.todoSvc.Create(ctx, c, in)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	a.insertCount++
	return todoToMap(t), nil
}

// dispatchUpdateTodo handles update_todo(todo_id, status=None,
// priority=None, due_date=None, role_scope=None, blocked_reason=None,
// assigned_to=None) → updated todo dict.
//
// `role_scope="none"` is forwarded as todo.ClearRoleScope so the service
// falls back to the caller's user-scope.
func dispatchUpdateTodo(ctx context.Context, a *ActionBuiltins, deps *actionDeps, call *runtime.FunctionCall) (any, error) {
	todoIDStr, err := argString(call.Args, "todo_id")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	todoID, err := uuid.Parse(todoIDStr)
	if err != nil {
		return nil, fmt.Errorf("%s: invalid todo_id: %w", call.Name, err)
	}

	u := todo.UpdateInput{}
	if s, err := argOptionalString(call.Args, "status"); err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	} else if s != "" {
		u.Status = &s
	}
	if s, err := argOptionalString(call.Args, "priority"); err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	} else if s != "" {
		u.Priority = &s
	}
	if s, err := argOptionalString(call.Args, "role_scope"); err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	} else if s != "" {
		// "none" is the sentinel for clearing back to caller's user-scope.
		// The service recognizes todo.ClearRoleScope for the same purpose.
		u.NewRoleName = &s
	}
	if s, err := argOptionalString(call.Args, "blocked_reason"); err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	} else if s != "" {
		u.BlockedReason = &s
	}
	if d, err := argOptionalDate(call.Args, "due_date"); err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	} else if d != nil {
		u.DueDate = d
	}

	c, err := deps.caller(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}

	if ref, err := argOptionalString(call.Args, "assigned_to"); err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	} else if ref != "" {
		id, msg := deps.todoSvc.ResolveAssignee(ctx, c, ref)
		if msg != "" {
			return nil, fmt.Errorf("%s: %s", call.Name, msg)
		}
		u.NewAssignee = id
	}

	t, err := deps.todoSvc.Update(ctx, c, todoID, u)
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			return nil, fmt.Errorf("%s: todo %s not found", call.Name, todoID)
		}
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	a.updateCount++
	return todoToMap(t), nil
}

// dispatchCompleteTodo handles complete_todo(todo_id, note="") →
// {id, status}. note is recorded as a comment before the status flip so
// the activity log captures the reason alongside the close event.
func dispatchCompleteTodo(ctx context.Context, a *ActionBuiltins, deps *actionDeps, call *runtime.FunctionCall) (any, error) {
	todoIDStr, err := argString(call.Args, "todo_id")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	todoID, err := uuid.Parse(todoIDStr)
	if err != nil {
		return nil, fmt.Errorf("%s: invalid todo_id: %w", call.Name, err)
	}
	note, err := argOptionalString(call.Args, "note")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}

	c, err := deps.caller(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}

	if note != "" {
		if err := deps.todoSvc.AddComment(ctx, c, todoID, note); err != nil {
			if errors.Is(err, services.ErrNotFound) {
				return nil, fmt.Errorf("%s: todo %s not found", call.Name, todoID)
			}
			return nil, fmt.Errorf("%s: recording note: %w", call.Name, err)
		}
	}

	t, err := deps.todoSvc.Complete(ctx, c, todoID)
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			return nil, fmt.Errorf("%s: todo %s not found", call.Name, todoID)
		}
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	a.updateCount++
	return map[string]any{
		"id":     t.ID.String(),
		"status": t.Status,
	}, nil
}

// dispatchAddTodoComment handles add_todo_comment(todo_id, content) →
// {id}. The service layer doesn't return the event row, so we synthesize
// a pseudo-id from the todo and timestamp for scripts that want to echo
// something back to the user.
func dispatchAddTodoComment(ctx context.Context, a *ActionBuiltins, deps *actionDeps, call *runtime.FunctionCall) (any, error) {
	todoIDStr, err := argString(call.Args, "todo_id")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	todoID, err := uuid.Parse(todoIDStr)
	if err != nil {
		return nil, fmt.Errorf("%s: invalid todo_id: %w", call.Name, err)
	}
	content, err := argString(call.Args, "content")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}

	c, err := deps.caller(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}

	if err := deps.todoSvc.AddComment(ctx, c, todoID, content); err != nil {
		if errors.Is(err, services.ErrNotFound) {
			return nil, fmt.Errorf("%s: todo %s not found", call.Name, todoID)
		}
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	a.insertCount++
	// Comment row ids aren't returned by the service. Use a deterministic
	// synthetic id (todo_id) so callers can still dedupe against the todo.
	return map[string]any{
		"id": todoID.String(),
	}, nil
}

// todoToMap flattens a *todo.Todo into a script-friendly map. UUIDs go
// out as strings; times as RFC3339 strings; nil pointers collapse to
// absent keys / empty strings so the Python side sees consistent shapes.
//
// The new scope model carries assignee/role-scope indirectly via
// scope_id; scripts that need the specific role/user can resolve it via
// a scope lookup. We expose scope_id + visibility here and drop the old
// assigned_to / role_scope / private / created_by keys.
func todoToMap(t *todo.Todo) map[string]any {
	if t == nil {
		return nil
	}
	out := map[string]any{
		"id":          t.ID.String(),
		"tenant_id":   t.TenantID.String(),
		"title":       t.Title,
		"description": t.Description,
		"status":      t.Status,
		"priority":    t.Priority,
		"scope_id":    t.ScopeID.String(),
		"visibility":  t.Visibility,
	}
	if t.DueDate != nil {
		out["due_date"] = t.DueDate.Format("2006-01-02")
	}
	if t.BlockedReason != "" {
		out["blocked_reason"] = t.BlockedReason
	}
	return out
}
