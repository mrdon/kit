package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/services"
)

func registerTaskTools(r *Registry, isAdmin bool) {
	for _, meta := range services.TaskTools {
		if meta.AdminOnly && !isAdmin {
			continue
		}
		def := Def{
			Name:        meta.Name,
			Description: meta.Description,
			Schema:      meta.Schema,
			AdminOnly:   meta.AdminOnly,
			Handler:     taskHandler(meta.Name),
		}
		if meta.Name == "create_task" {
			def.GateCardPreview = createTaskGatePreview
		}
		r.Register(def)
	}
}

// createTaskGatePreview customises the approval card when the agent sets
// require_approval on create_task. The description + schedule are what
// the user mostly wants to review; the card body preview component
// renders the full arguments.
func createTaskGatePreview(_ *ExecContext, input json.RawMessage) GateCardPreview {
	var args struct {
		Description string `json:"description"`
	}
	_ = json.Unmarshal(input, &args)
	title := "Schedule task?"
	if args.Description != "" {
		title = "Schedule: " + truncateRunes(args.Description, 70)
	}
	return GateCardPreview{
		Title:        title,
		ApproveLabel: "Create task",
		SkipLabel:    "Don't create",
	}
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func taskHandler(name string) HandlerFunc {
	switch name {
	case "create_task":
		return handleCreateTask
	case "list_tasks":
		return handleListTasks
	case "update_task":
		return handleUpdateTask
	default:
		return func(_ *ExecContext, _ json.RawMessage) (string, error) {
			return "", fmt.Errorf("unknown task tool: %s", name)
		}
	}
}

func handleCreateTask(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		Description string `json:"description"`
		CronExpr    string `json:"cron_expr"`
		RunAt       string `json:"run_at"`
		ChannelID   string `json:"channel_id"`
		Scope       string `json:"scope"`
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}

	if inp.ChannelID == "" {
		inp.ChannelID = ec.Channel
	}

	if inp.CronExpr == "" && inp.RunAt == "" {
		return "Provide either cron_expr (recurring) or run_at (one-time).", nil
	}
	if inp.CronExpr != "" && inp.RunAt != "" {
		return "Provide cron_expr or run_at, not both.", nil
	}

	tz := resolveTimezone(ec)
	runOnce := inp.RunAt != ""

	var runAt *time.Time
	if runOnce {
		loc, err := time.LoadLocation(tz)
		if err != nil {
			return fmt.Sprintf("Invalid timezone %q.", tz), nil
		}
		t, err := time.ParseInLocation("2006-01-02T15:04:05", inp.RunAt, loc)
		if err != nil {
			t, err = time.ParseInLocation("2006-01-02T15:04", inp.RunAt, loc)
		}
		if err != nil {
			return "Invalid run_at format. Use ISO 8601: 2026-04-05T21:20:00", nil
		}
		if t.Before(time.Now()) {
			return "run_at must be in the future.", nil
		}
		runAt = &t
	}

	task, err := ec.Svc.Tasks.Create(ec.Ctx, ec.Caller(), inp.Description, inp.CronExpr, tz, inp.ChannelID, inp.Scope, runOnce, runAt)
	if errors.Is(err, services.ErrForbidden) {
		return "Only admins can create tenant-scoped tasks.", nil
	}
	if err != nil {
		return "", err
	}

	label := "Next run"
	if runOnce {
		label = "Runs at"
	}
	return fmt.Sprintf("Task created (ID: %s). %s: %s (%s)",
		task.ID, label, task.NextRunAt.Format("Mon Jan 2 3:04 PM"), tz), nil
}

func resolveTimezone(ec *ExecContext) string {
	if ec.User.Timezone != "" {
		return ec.User.Timezone
	}
	info, err := ec.Slack.GetUserInfo(ec.Ctx, ec.User.SlackUserID)
	if err == nil && info.Timezone != "" {
		return info.Timezone
	}
	if ec.Tenant.Timezone != "" {
		return ec.Tenant.Timezone
	}
	return "UTC"
}

func handleListTasks(ec *ExecContext, _ json.RawMessage) (string, error) {
	tasks, err := ec.Svc.Tasks.List(ec.Ctx, ec.Caller())
	if err != nil {
		return "", err
	}
	if len(tasks) == 0 {
		return "No scheduled tasks.", nil
	}

	var b strings.Builder
	b.WriteString("Scheduled tasks:\n")
	for _, t := range tasks {
		status := string(t.Status)
		if t.LastError != nil {
			status += " (last error: " + *t.LastError + ")"
		}
		next := t.NextRunAt.Format("Mon Jan 2 3:04 PM")
		schedule := "cron: `" + t.CronExpr + "`"
		if t.RunOnce {
			schedule = "one-time"
		}
		fmt.Fprintf(&b, "- [%s] %s | %s | next: %s | status: %s\n",
			t.ID, t.Description, schedule, next, status)
	}
	return b.String(), nil
}

func handleUpdateTask(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		TaskID      string `json:"task_id"`
		Description string `json:"description"`
		Delete      bool   `json:"delete"`
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	taskID, err := uuid.Parse(inp.TaskID)
	if err != nil {
		return "Invalid task ID.", nil
	}

	if inp.Delete {
		err = ec.Svc.Tasks.Delete(ec.Ctx, ec.Caller(), taskID)
		if errors.Is(err, services.ErrNotFound) {
			return "Task not found or you don't have permission to delete it.", nil
		}
		if err != nil {
			return "", err
		}
		return "Task deleted.", nil
	}

	if inp.Description == "" {
		return "Provide description to update, or delete=true to remove.", nil
	}

	err = ec.Svc.Tasks.Update(ec.Ctx, ec.Caller(), taskID, inp.Description)
	if errors.Is(err, services.ErrNotFound) {
		return "Task not found or you don't have permission to update it.", nil
	}
	if err != nil {
		return "", err
	}
	return "Task updated.", nil
}
