package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/tools"
)

func registerTaskAgentTools(r *tools.Registry, isAdmin bool, svc *TaskService) {
	for _, meta := range taskTools {
		if meta.AdminOnly && !isAdmin {
			continue
		}
		r.Register(tools.Def{
			Name:        meta.Name,
			Description: meta.Description,
			Schema:      meta.Schema,
			AdminOnly:   meta.AdminOnly,
			Handler:     taskAgentHandler(meta.Name, svc),
		})
	}
}

func taskAgentHandler(name string, svc *TaskService) tools.HandlerFunc {
	switch name {
	case "create_task":
		return handleCreateTask(svc)
	case "list_tasks":
		return handleListTasks(svc)
	case "get_task":
		return handleGetTask(svc)
	case "update_task":
		return handleUpdateTask(svc)
	case "add_task_comment":
		return handleAddTaskComment(svc)
	case "complete_task":
		return handleCompleteTask(svc)
	case "snooze_task":
		return handleSnoozeTask(svc)
	default:
		return func(_ *tools.ExecContext, _ json.RawMessage) (string, error) {
			return "", fmt.Errorf("unknown task tool: %s", name)
		}
	}
}

func handleCreateTask(svc *TaskService) tools.HandlerFunc {
	return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
		var inp struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			Priority    string `json:"priority"`
			Assignee    string `json:"assignee"`
			RoleScope   string `json:"role_scope"`
			DueDate     string `json:"due_date"`
		}
		if err := json.Unmarshal(input, &inp); err != nil {
			return "", fmt.Errorf("parsing input: %w", err)
		}

		in := CreateInput{
			Title:       inp.Title,
			Description: inp.Description,
			Priority:    inp.Priority,
			RoleName:    inp.RoleScope,
		}

		if inp.Assignee != "" {
			id, msg := svc.ResolveAssignee(ec.Ctx, ec.Caller(), inp.Assignee)
			if msg != "" {
				return msg, nil
			}
			in.AssigneeUserID = id
		}

		if inp.DueDate != "" {
			d, err := time.Parse("2006-01-02", inp.DueDate)
			if err != nil {
				return "Invalid due_date format. Use YYYY-MM-DD.", nil
			}
			in.DueDate = &d
		}

		caller := ec.Caller()
		t, err := svc.Create(ec.Ctx, caller, in)
		if err != nil {
			if errors.Is(err, ErrPrimaryRoleNotSet) {
				return primaryRoleNotSetMessage(caller), nil
			}
			if errors.Is(err, services.ErrForbidden) {
				return "You don't have permission to create a task with those settings.", nil
			}
			if errors.Is(err, ErrInvalidRole) {
				return fmt.Sprintf("Role %q does not exist. Use list_roles to see available roles.", inp.RoleScope), nil
			}
			return "", fmt.Errorf("creating task: %w", err)
		}

		return fmt.Sprintf("Created task [%s]: %s", t.ID, t.Title), nil
	}
}

func handleListTasks(svc *TaskService) tools.HandlerFunc {
	return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
		var inp struct {
			Status       string `json:"status"`
			Priority     string `json:"priority"`
			AssignedToMe bool   `json:"assigned_to_me"`
			Assignee     string `json:"assignee"`
			Unassigned   bool   `json:"unassigned"`
			RoleScope    string `json:"role_scope"`
			Search       string `json:"search"`
			Overdue      bool   `json:"overdue"`
			ClosedSince  string `json:"closed_since"`
		}
		if err := json.Unmarshal(input, &inp); err != nil {
			return "", fmt.Errorf("parsing input: %w", err)
		}

		f := TaskFilters{
			Status:       inp.Status,
			Priority:     inp.Priority,
			AssignedToMe: inp.AssignedToMe,
			Unassigned:   inp.Unassigned,
			RoleName:     inp.RoleScope,
			Search:       inp.Search,
			Overdue:      inp.Overdue,
		}

		if inp.Assignee != "" {
			id, msg := svc.ResolveAssignee(ec.Ctx, ec.Caller(), inp.Assignee)
			if msg != "" {
				return msg, nil
			}
			f.AssigneeUserID = id
		}

		if inp.ClosedSince != "" {
			t, err := time.Parse("2006-01-02", inp.ClosedSince)
			if err != nil {
				return "Invalid closed_since format. Use YYYY-MM-DD.", nil
			}
			f.ClosedSince = &t
		}

		caller := ec.Caller()
		tasks, err := svc.List(ec.Ctx, caller, f)
		if err != nil {
			return "", fmt.Errorf("listing tasks: %w", err)
		}

		if len(tasks) == 0 {
			return "No tasks found matching your filters.", nil
		}

		var b strings.Builder
		fmt.Fprintf(&b, "Found %d task(s):\n\n", len(tasks))
		for _, t := range tasks {
			b.WriteString(FormatTask(&t))
			b.WriteString("\n\n")
		}
		return b.String(), nil
	}
}

func handleGetTask(svc *TaskService) tools.HandlerFunc {
	return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
		var inp struct {
			TaskID string `json:"task_id"`
		}
		if err := json.Unmarshal(input, &inp); err != nil {
			return "", fmt.Errorf("parsing input: %w", err)
		}

		taskID, err := uuid.Parse(inp.TaskID)
		if err != nil {
			return "Invalid task_id UUID.", nil
		}

		caller := ec.Caller()
		t, events, err := svc.Get(ec.Ctx, caller, taskID)
		if err != nil {
			if errors.Is(err, services.ErrNotFound) {
				return "Task not found.", nil
			}
			return "", fmt.Errorf("getting task: %w", err)
		}

		return FormatTaskDetailed(t, events), nil
	}
}

func handleUpdateTask(svc *TaskService) tools.HandlerFunc {
	return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
		var inp struct {
			TaskID        string  `json:"task_id"`
			Title         string  `json:"title"`
			Description   string  `json:"description"`
			Status        string  `json:"status"`
			Priority      string  `json:"priority"`
			BlockedReason string  `json:"blocked_reason"`
			Assignee      string  `json:"assignee"`
			ClearAssignee bool    `json:"clear_assignee"`
			RoleScope     *string `json:"role_scope"`
			DueDate       string  `json:"due_date"`
			ClearDueDate  bool    `json:"clear_due_date"`
		}
		if err := json.Unmarshal(input, &inp); err != nil {
			return "", fmt.Errorf("parsing input: %w", err)
		}

		taskID, err := uuid.Parse(inp.TaskID)
		if err != nil {
			return "Invalid task_id UUID.", nil
		}

		u := UpdateInput{}
		if inp.Title != "" {
			u.Title = &inp.Title
		}
		if inp.Description != "" {
			u.Description = &inp.Description
		}
		if inp.Status != "" {
			u.Status = &inp.Status
		}
		if inp.Priority != "" {
			u.Priority = &inp.Priority
		}
		if inp.BlockedReason != "" {
			u.BlockedReason = &inp.BlockedReason
		}
		if inp.Assignee != "" {
			id, msg := svc.ResolveAssignee(ec.Ctx, ec.Caller(), inp.Assignee)
			if msg != "" {
				return msg, nil
			}
			u.NewAssigneeUserID = id
		}
		if inp.ClearAssignee {
			u.ClearAssignee = true
		}
		if inp.RoleScope != nil {
			val := *inp.RoleScope
			u.NewRoleName = &val
		}
		if inp.DueDate != "" {
			d, err := time.Parse("2006-01-02", inp.DueDate)
			if err != nil {
				return "Invalid due_date format. Use YYYY-MM-DD.", nil
			}
			u.DueDate = &d
		}
		if inp.ClearDueDate {
			u.ClearDueDate = true
		}

		caller := ec.Caller()
		t, err := svc.Update(ec.Ctx, caller, taskID, u)
		if err != nil {
			if errors.Is(err, services.ErrNotFound) {
				return "Task not found.", nil
			}
			if errors.Is(err, services.ErrForbidden) {
				return "You don't have permission to update this task.", nil
			}
			if errors.Is(err, ErrInvalidRole) {
				name := ""
				if inp.RoleScope != nil {
					name = *inp.RoleScope
				}
				return fmt.Sprintf("Role %q does not exist. Use list_roles to see available roles.", name), nil
			}
			return "", fmt.Errorf("updating task: %w", err)
		}

		return "Updated task:\n" + FormatTask(t), nil
	}
}

func handleAddTaskComment(svc *TaskService) tools.HandlerFunc {
	return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
		var inp struct {
			TaskID  string `json:"task_id"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(input, &inp); err != nil {
			return "", fmt.Errorf("parsing input: %w", err)
		}

		taskID, err := uuid.Parse(inp.TaskID)
		if err != nil {
			return "Invalid task_id UUID.", nil
		}

		caller := ec.Caller()
		if err := svc.AddComment(ec.Ctx, caller, taskID, inp.Content); err != nil {
			if errors.Is(err, services.ErrNotFound) {
				return "Task not found.", nil
			}
			return "", fmt.Errorf("adding comment: %w", err)
		}

		return "Comment added.", nil
	}
}

func handleCompleteTask(svc *TaskService) tools.HandlerFunc {
	return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
		var inp struct {
			TaskID string `json:"task_id"`
		}
		if err := json.Unmarshal(input, &inp); err != nil {
			return "", fmt.Errorf("parsing input: %w", err)
		}

		taskID, err := uuid.Parse(inp.TaskID)
		if err != nil {
			return "Invalid task_id UUID.", nil
		}

		caller := ec.Caller()
		t, err := svc.Complete(ec.Ctx, caller, taskID)
		if err != nil {
			if errors.Is(err, services.ErrNotFound) {
				return "Task not found.", nil
			}
			if errors.Is(err, services.ErrForbidden) {
				return "You don't have permission to complete this task.", nil
			}
			return "", fmt.Errorf("completing task: %w", err)
		}

		return "Completed: " + t.Title, nil
	}
}

func handleSnoozeTask(svc *TaskService) tools.HandlerFunc {
	return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
		var inp struct {
			TaskID string `json:"task_id"`
			Days   int    `json:"days"`
		}
		if err := json.Unmarshal(input, &inp); err != nil {
			return "", fmt.Errorf("parsing input: %w", err)
		}

		taskID, err := uuid.Parse(inp.TaskID)
		if err != nil {
			return "Invalid task_id UUID.", nil
		}
		caller := ec.Caller()
		t, err := svc.SnoozeDays(ec.Ctx, caller, taskID, inp.Days)
		if err != nil {
			if errors.Is(err, services.ErrNotFound) {
				return "Task not found.", nil
			}
			if errors.Is(err, services.ErrForbidden) {
				return "You don't have permission to snooze this task.", nil
			}
			if strings.Contains(err.Error(), "snooze days must be") {
				return err.Error(), nil
			}
			return "", fmt.Errorf("snoozing task: %w", err)
		}

		return fmt.Sprintf("Snoozed %q for %d day(s). Visible again after %s.", t.Title, inp.Days, t.SnoozedUntil.Format("2006-01-02 15:04 MST")), nil
	}
}

// primaryRoleNotSetMessage builds the agent-facing error when the resolver
// can't pick a default role. Lists the caller's roles so the agent can
// prompt the user to choose.
func primaryRoleNotSetMessage(c *services.Caller) string {
	if len(c.Roles) == 0 {
		return "You're not in any roles, so you can't create tasks. Ask an admin to add you to a role."
	}
	return fmt.Sprintf("You're in multiple roles. Either pass `role_scope` explicitly or set a primary role. Your roles: %s.", strings.Join(c.Roles, ", "))
}
