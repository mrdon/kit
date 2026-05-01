// Package builder: action_builtins_task.go dispatches create_task /
// update_task / complete_task / add_task_comment into TaskService.
//
// Each dispatcher mirrors the shape of the agent-tool handler in
// internal/apps/task/agent.go but returns a dict the script can inspect
// instead of the human-readable "Created task […]" string.
package builder

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/builder/runtime"
	"github.com/mrdon/kit/internal/apps/task"
	"github.com/mrdon/kit/internal/services"
)

// dispatchCreateTask handles create_task(title, description="",
// priority="medium", due_date=None, role_scope=None, assignee=None) →
// task dict.
//
// The legacy `private` and `visibility` kwargs are silently ignored — the
// new model is role-only. Scripts that previously created private todos
// will land in the caller's primary role and see them via the assignee
// branch of the feed.
func dispatchCreateTask(ctx context.Context, a *ActionBuiltins, deps *actionDeps, call *runtime.FunctionCall) (any, error) {
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
	assigneeRef, err := argOptionalString(call.Args, "assignee")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}

	c, err := deps.caller(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}

	in := task.CreateInput{
		Title:       title,
		Description: description,
		Priority:    priority,
		RoleName:    roleScope,
		DueDate:     dueDate,
	}

	if assigneeRef != "" {
		id, msg := deps.todoSvc.ResolveAssignee(ctx, c, assigneeRef)
		if msg != "" {
			return nil, fmt.Errorf("%s: %s", call.Name, msg)
		}
		in.AssigneeUserID = id
	}

	t, err := deps.todoSvc.Create(ctx, c, in)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	a.insertCount++
	return taskToMap(t), nil
}

// dispatchUpdateTask handles update_task(task_id, status=None,
// priority=None, due_date=None, role_scope=None, blocked_reason=None,
// assignee=None, clear_assignee=False) → updated task dict.
func dispatchUpdateTask(ctx context.Context, a *ActionBuiltins, deps *actionDeps, call *runtime.FunctionCall) (any, error) {
	taskIDStr, err := argString(call.Args, "task_id")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	taskID, err := uuid.Parse(taskIDStr)
	if err != nil {
		return nil, fmt.Errorf("%s: invalid task_id: %w", call.Name, err)
	}

	u := task.UpdateInput{}
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

	if ref, err := argOptionalString(call.Args, "assignee"); err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	} else if ref != "" {
		id, msg := deps.todoSvc.ResolveAssignee(ctx, c, ref)
		if msg != "" {
			return nil, fmt.Errorf("%s: %s", call.Name, msg)
		}
		u.NewAssigneeUserID = id
	}
	if clear, err := argOptionalBool(call.Args, "clear_assignee"); err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	} else if clear {
		u.ClearAssignee = true
	}

	t, err := deps.todoSvc.Update(ctx, c, taskID, u)
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			return nil, fmt.Errorf("%s: task %s not found", call.Name, taskID)
		}
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	a.updateCount++
	return taskToMap(t), nil
}

// dispatchCompleteTask handles complete_task(task_id, note="") →
// {id, status}. note is recorded as a comment before the status flip so
// the activity log captures the reason alongside the close event.
func dispatchCompleteTask(ctx context.Context, a *ActionBuiltins, deps *actionDeps, call *runtime.FunctionCall) (any, error) {
	taskIDStr, err := argString(call.Args, "task_id")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	taskID, err := uuid.Parse(taskIDStr)
	if err != nil {
		return nil, fmt.Errorf("%s: invalid task_id: %w", call.Name, err)
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
		if err := deps.todoSvc.AddComment(ctx, c, taskID, note); err != nil {
			if errors.Is(err, services.ErrNotFound) {
				return nil, fmt.Errorf("%s: task %s not found", call.Name, taskID)
			}
			return nil, fmt.Errorf("%s: recording note: %w", call.Name, err)
		}
	}

	t, err := deps.todoSvc.Complete(ctx, c, taskID)
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			return nil, fmt.Errorf("%s: task %s not found", call.Name, taskID)
		}
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	a.updateCount++
	return map[string]any{
		"id":     t.ID.String(),
		"status": t.Status,
	}, nil
}

// dispatchAddTaskComment handles add_task_comment(task_id, content) →
// {id}. The service layer doesn't return the event row, so we synthesize
// a pseudo-id from the task for scripts that want to echo something back
// to the user.
func dispatchAddTaskComment(ctx context.Context, a *ActionBuiltins, deps *actionDeps, call *runtime.FunctionCall) (any, error) {
	taskIDStr, err := argString(call.Args, "task_id")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	taskID, err := uuid.Parse(taskIDStr)
	if err != nil {
		return nil, fmt.Errorf("%s: invalid task_id: %w", call.Name, err)
	}
	content, err := argString(call.Args, "content")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}

	c, err := deps.caller(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}

	if err := deps.todoSvc.AddComment(ctx, c, taskID, content); err != nil {
		if errors.Is(err, services.ErrNotFound) {
			return nil, fmt.Errorf("%s: task %s not found", call.Name, taskID)
		}
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	a.insertCount++
	return map[string]any{
		"id": taskID.String(),
	}, nil
}

// taskToMap flattens a *task.Task into a script-friendly map. UUIDs go
// out as strings; times as RFC3339 strings; nil pointers collapse to
// absent keys / empty strings so the Python side sees consistent shapes.
func taskToMap(t *task.Task) map[string]any {
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
	}
	if t.AssigneeUserID != nil {
		out["assignee_user_id"] = t.AssigneeUserID.String()
	}
	if t.DueDate != nil {
		out["due_date"] = t.DueDate.Format("2006-01-02")
	}
	if t.BlockedReason != "" {
		out["blocked_reason"] = t.BlockedReason
	}
	return out
}
