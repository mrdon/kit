package todo

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

func registerTodoAgentTools(r *tools.Registry, isAdmin bool, svc *TodoService) {
	for _, meta := range todoTools {
		if meta.AdminOnly && !isAdmin {
			continue
		}
		r.Register(tools.Def{
			Name:        meta.Name,
			Description: meta.Description,
			Schema:      meta.Schema,
			AdminOnly:   meta.AdminOnly,
			Handler:     todoAgentHandler(meta.Name, svc),
		})
	}
}

func todoAgentHandler(name string, svc *TodoService) tools.HandlerFunc {
	switch name {
	case "create_todo":
		return handleCreateTodo(svc)
	case "list_todos":
		return handleListTodos(svc)
	case "get_todo":
		return handleGetTodo(svc)
	case "update_todo":
		return handleUpdateTodo(svc)
	case "add_todo_comment":
		return handleAddTodoComment(svc)
	case "complete_todo":
		return handleCompleteTodo(svc)
	case "snooze_todo":
		return handleSnoozeTodo(svc)
	default:
		return func(_ *tools.ExecContext, _ json.RawMessage) (string, error) {
			return "", fmt.Errorf("unknown todo tool: %s", name)
		}
	}
}

func handleCreateTodo(svc *TodoService) tools.HandlerFunc {
	return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
		var inp struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			Priority    string `json:"priority"`
			AssignedTo  string `json:"assigned_to"`
			RoleScope   string `json:"role_scope"`
			DueDate     string `json:"due_date"`
			Visibility  string `json:"visibility"`
		}
		if err := json.Unmarshal(input, &inp); err != nil {
			return "", fmt.Errorf("parsing input: %w", err)
		}

		in := CreateInput{
			Title:       inp.Title,
			Description: inp.Description,
			Priority:    inp.Priority,
			RoleName:    inp.RoleScope,
			Visibility:  inp.Visibility,
		}

		if inp.AssignedTo != "" {
			id, msg := svc.ResolveAssignee(ec.Ctx, ec.Caller(), inp.AssignedTo)
			if msg != "" {
				return msg, nil
			}
			in.AssignedTo = id
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
			if errors.Is(err, services.ErrForbidden) {
				return "You don't have permission to create a todo with those settings.", nil
			}
			if errors.Is(err, ErrInvalidRole) {
				return fmt.Sprintf("Role %q does not exist. Use list_roles to see available roles.", inp.RoleScope), nil
			}
			return "", fmt.Errorf("creating todo: %w", err)
		}

		return fmt.Sprintf("Created todo [%s]: %s", t.ID, t.Title), nil
	}
}

func handleListTodos(svc *TodoService) tools.HandlerFunc {
	return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
		var inp struct {
			Status       string `json:"status"`
			Priority     string `json:"priority"`
			AssignedToMe bool   `json:"assigned_to_me"`
			RoleScope    string `json:"role_scope"`
			Search       string `json:"search"`
			Overdue      bool   `json:"overdue"`
			ClosedSince  string `json:"closed_since"`
		}
		if err := json.Unmarshal(input, &inp); err != nil {
			return "", fmt.Errorf("parsing input: %w", err)
		}

		f := TodoFilters{
			Status:       inp.Status,
			Priority:     inp.Priority,
			AssignedToMe: inp.AssignedToMe,
			RoleName:     inp.RoleScope,
			Search:       inp.Search,
			Overdue:      inp.Overdue,
		}

		if inp.ClosedSince != "" {
			t, err := time.Parse("2006-01-02", inp.ClosedSince)
			if err != nil {
				return "Invalid closed_since format. Use YYYY-MM-DD.", nil
			}
			f.ClosedSince = &t
		}

		caller := ec.Caller()
		todos, err := svc.List(ec.Ctx, caller, f)
		if err != nil {
			return "", fmt.Errorf("listing todos: %w", err)
		}

		if len(todos) == 0 {
			return "No todos found matching your filters.", nil
		}

		var b strings.Builder
		fmt.Fprintf(&b, "Found %d todo(s):\n\n", len(todos))
		for _, t := range todos {
			b.WriteString(FormatTodo(&t))
			b.WriteString("\n\n")
		}
		return b.String(), nil
	}
}

func handleGetTodo(svc *TodoService) tools.HandlerFunc {
	return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
		var inp struct {
			TodoID string `json:"todo_id"`
		}
		if err := json.Unmarshal(input, &inp); err != nil {
			return "", fmt.Errorf("parsing input: %w", err)
		}

		todoID, err := uuid.Parse(inp.TodoID)
		if err != nil {
			return "Invalid todo_id UUID.", nil
		}

		caller := ec.Caller()
		t, events, err := svc.Get(ec.Ctx, caller, todoID)
		if err != nil {
			if errors.Is(err, services.ErrNotFound) {
				return "Todo not found.", nil
			}
			return "", fmt.Errorf("getting todo: %w", err)
		}

		return FormatTodoDetailed(t, events), nil
	}
}

func handleUpdateTodo(svc *TodoService) tools.HandlerFunc {
	return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
		var inp struct {
			TodoID        string  `json:"todo_id"`
			Title         string  `json:"title"`
			Description   string  `json:"description"`
			Status        string  `json:"status"`
			Priority      string  `json:"priority"`
			BlockedReason string  `json:"blocked_reason"`
			AssignedTo    string  `json:"assigned_to"`
			RoleScope     *string `json:"role_scope"`
			DueDate       string  `json:"due_date"`
			Visibility    string  `json:"visibility"`
		}
		if err := json.Unmarshal(input, &inp); err != nil {
			return "", fmt.Errorf("parsing input: %w", err)
		}

		todoID, err := uuid.Parse(inp.TodoID)
		if err != nil {
			return "Invalid todo_id UUID.", nil
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
		if inp.AssignedTo != "" {
			id, msg := svc.ResolveAssignee(ec.Ctx, ec.Caller(), inp.AssignedTo)
			if msg != "" {
				return msg, nil
			}
			u.NewAssignee = id
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
		if inp.Visibility != "" {
			u.Visibility = &inp.Visibility
		}

		caller := ec.Caller()
		t, err := svc.Update(ec.Ctx, caller, todoID, u)
		if err != nil {
			if errors.Is(err, services.ErrNotFound) {
				return "Todo not found.", nil
			}
			if errors.Is(err, services.ErrForbidden) {
				return "You don't have permission to update this todo.", nil
			}
			if errors.Is(err, ErrInvalidRole) {
				name := ""
				if inp.RoleScope != nil {
					name = *inp.RoleScope
				}
				return fmt.Sprintf("Role %q does not exist. Use list_roles to see available roles.", name), nil
			}
			return "", fmt.Errorf("updating todo: %w", err)
		}

		return "Updated todo:\n" + FormatTodo(t), nil
	}
}

func handleAddTodoComment(svc *TodoService) tools.HandlerFunc {
	return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
		var inp struct {
			TodoID  string `json:"todo_id"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(input, &inp); err != nil {
			return "", fmt.Errorf("parsing input: %w", err)
		}

		todoID, err := uuid.Parse(inp.TodoID)
		if err != nil {
			return "Invalid todo_id UUID.", nil
		}

		caller := ec.Caller()
		if err := svc.AddComment(ec.Ctx, caller, todoID, inp.Content); err != nil {
			if errors.Is(err, services.ErrNotFound) {
				return "Todo not found.", nil
			}
			return "", fmt.Errorf("adding comment: %w", err)
		}

		return "Comment added.", nil
	}
}

func handleCompleteTodo(svc *TodoService) tools.HandlerFunc {
	return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
		var inp struct {
			TodoID string `json:"todo_id"`
		}
		if err := json.Unmarshal(input, &inp); err != nil {
			return "", fmt.Errorf("parsing input: %w", err)
		}

		todoID, err := uuid.Parse(inp.TodoID)
		if err != nil {
			return "Invalid todo_id UUID.", nil
		}

		caller := ec.Caller()
		t, err := svc.Complete(ec.Ctx, caller, todoID)
		if err != nil {
			if errors.Is(err, services.ErrNotFound) {
				return "Todo not found.", nil
			}
			if errors.Is(err, services.ErrForbidden) {
				return "You don't have permission to complete this todo.", nil
			}
			return "", fmt.Errorf("completing todo: %w", err)
		}

		return "Completed: " + t.Title, nil
	}
}

func handleSnoozeTodo(svc *TodoService) tools.HandlerFunc {
	return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
		var inp struct {
			TodoID string `json:"todo_id"`
			Days   int    `json:"days"`
		}
		if err := json.Unmarshal(input, &inp); err != nil {
			return "", fmt.Errorf("parsing input: %w", err)
		}

		todoID, err := uuid.Parse(inp.TodoID)
		if err != nil {
			return "Invalid todo_id UUID.", nil
		}
		until, err := SnoozeDaysToUntil(inp.Days)
		if err != nil {
			return err.Error(), nil
		}

		caller := ec.Caller()
		t, err := svc.Snooze(ec.Ctx, caller, todoID, until)
		if err != nil {
			if errors.Is(err, services.ErrNotFound) {
				return "Todo not found.", nil
			}
			if errors.Is(err, services.ErrForbidden) {
				return "You don't have permission to snooze this todo.", nil
			}
			return "", fmt.Errorf("snoozing todo: %w", err)
		}

		return fmt.Sprintf("Snoozed %q for %d day(s). Visible again after %s.", t.Title, inp.Days, until.Format("2006-01-02 15:04")), nil
	}
}
