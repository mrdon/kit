package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/models"
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
			Handler:     taskHandler(meta.Name, r),
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

func taskHandler(name string, r *Registry) HandlerFunc {
	switch name {
	case "create_task":
		return func(ec *ExecContext, input json.RawMessage) (string, error) {
			return handleCreateTask(ec, input, r)
		}
	case "list_tasks":
		return handleListTasks
	case "update_task":
		return func(ec *ExecContext, input json.RawMessage) (string, error) {
			return handleUpdateTask(ec, input, r)
		}
	default:
		return func(_ *ExecContext, _ json.RawMessage) (string, error) {
			return "", fmt.Errorf("unknown task tool: %s", name)
		}
	}
}

// parseAndValidatePolicy extracts the optional "policy" field from the
// tool input and validates it against the caller's registry. Returns
// (nil, "", nil) when no policy was supplied — the task gets today's
// behaviour. Returns (nil, userMessage, nil) on a user-correctable error
// (unknown key, unknown tool, wrong type). Only returns (nil, "", err)
// on internal failures.
func parseAndValidatePolicy(rawPolicy json.RawMessage, reg *Registry) (*models.Policy, string, error) {
	if len(rawPolicy) == 0 || string(rawPolicy) == "null" {
		return nil, "", nil
	}
	dec := json.NewDecoder(strings.NewReader(string(rawPolicy)))
	dec.DisallowUnknownFields()
	var p models.Policy
	if err := dec.Decode(&p); err != nil {
		return nil, "Invalid policy: " + err.Error(), nil
	}
	if p.AllowedTools != nil {
		for _, tname := range *p.AllowedTools {
			if !registryHasTool(reg, tname) && !models.InfrastructureTools[tname] {
				return nil, fmt.Sprintf("Policy allowed_tools: tool %q is not available to you.", tname), nil
			}
		}
	}
	for _, tname := range p.ForceGate {
		if !registryHasTool(reg, tname) && !models.InfrastructureTools[tname] {
			return nil, fmt.Sprintf("Policy force_gate: tool %q is not available to you.", tname), nil
		}
	}
	for tname, args := range p.PinnedArgs {
		if !registryHasTool(reg, tname) && !models.InfrastructureTools[tname] {
			return nil, fmt.Sprintf("Policy pinned_args: tool %q is not available to you.", tname), nil
		}
		for k, v := range args {
			switch v.(type) {
			case string, bool, float64, nil, []any, map[string]any:
				// OK — JSON-native types.
			default:
				return nil, fmt.Sprintf("Policy pinned_args[%s][%s]: unsupported value type %T.", tname, k, v), nil
			}
		}
	}
	return &p, "", nil
}

// registryHasTool reports whether the caller's registry includes a tool
// by that name. Used at create_task time so a non-admin can't stash an
// admin-only tool in allowed_tools / force_gate / pinned_args and have
// it silently fail at fire time.
func registryHasTool(reg *Registry, name string) bool {
	if reg == nil {
		return true // tests without a registry; defer to fire-time
	}
	_, ok := reg.defByName(name)
	return ok
}

func handleCreateTask(ec *ExecContext, input json.RawMessage, reg *Registry) (string, error) {
	var inp struct {
		Description string          `json:"description"`
		CronExpr    string          `json:"cron_expr"`
		RunAt       string          `json:"run_at"`
		ChannelID   string          `json:"channel_id"`
		Scope       string          `json:"scope"`
		Policy      json.RawMessage `json:"policy"`
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	policy, msg, err := parseAndValidatePolicy(inp.Policy, reg)
	if err != nil {
		return "", err
	}
	if msg != "" {
		return msg, nil
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

	model := ClassifyTaskModel(ec.Ctx, ec.LLM, inp.Description)
	task, err := ec.Svc.Tasks.Create(ec.Ctx, ec.Caller(), services.CreateInput{
		Description: inp.Description,
		CronExpr:    inp.CronExpr,
		Timezone:    tz,
		ChannelID:   inp.ChannelID,
		Scope:       inp.Scope,
		Model:       model,
		RunOnce:     runOnce,
		RunAt:       runAt,
		Policy:      policy,
	})
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
	return fmt.Sprintf("Task created (ID: %s, model: %s). %s: %s (%s)",
		task.ID, task.Model, label, task.NextRunAt.Format("Mon Jan 2 3:04 PM"), tz), nil
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
		fmt.Fprintf(&b, "- [%s] %s | %s | next: %s | status: %s",
			t.ID, t.Description, schedule, next, status)
		if policySummary := summarizePolicy(t.Config); policySummary != "" {
			fmt.Fprintf(&b, " | %s", policySummary)
		}
		b.WriteByte('\n')
	}
	return b.String(), nil
}

// summarizePolicy renders a compact description of a task's policy
// for list_tasks output, e.g. "policy: allow-list(4), force-gate(post_to_channel), pinned(channel)".
// Returns "" when the task has no policy.
func summarizePolicy(cfg []byte) string {
	policy, err := models.ParseConfigPolicy(cfg)
	if err != nil || policy == nil {
		return ""
	}
	var parts []string
	if policy.AllowedTools != nil {
		parts = append(parts, fmt.Sprintf("allow-list(%d)", len(*policy.AllowedTools)))
	}
	if len(policy.ForceGate) > 0 {
		parts = append(parts, "force-gate("+strings.Join(policy.ForceGate, ",")+")")
	}
	if len(policy.PinnedArgs) > 0 {
		var keys []string
		for tool, args := range policy.PinnedArgs {
			for k := range args {
				keys = append(keys, tool+"."+k)
			}
		}
		parts = append(parts, "pinned("+strings.Join(keys, ",")+")")
	}
	if len(parts) == 0 {
		return ""
	}
	return "policy: " + strings.Join(parts, ", ")
}

func handleUpdateTask(ec *ExecContext, input json.RawMessage, reg *Registry) (string, error) {
	var inp struct {
		TaskID      string          `json:"task_id"`
		Description string          `json:"description"`
		Policy      json.RawMessage `json:"policy"`
		Delete      bool            `json:"delete"`
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

	policy, msg, err := parseAndValidatePolicy(inp.Policy, reg)
	if err != nil {
		return "", err
	}
	if msg != "" {
		return msg, nil
	}

	update := services.UpdateInput{}
	if inp.Description != "" {
		update.Description = &inp.Description
	}
	if policy != nil {
		update.Policy = policy
	}
	if update.Description == nil && update.Policy == nil {
		return "Provide description, policy, or delete=true.", nil
	}

	err = ec.Svc.Tasks.Update(ec.Ctx, ec.Caller(), taskID, update)
	if errors.Is(err, services.ErrNotFound) {
		return "Task not found or you don't have permission to update it.", nil
	}
	if err != nil {
		return "", err
	}
	return "Task updated.", nil
}
