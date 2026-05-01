package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/agent"
	"github.com/mrdon/kit/internal/anthropic"
	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/mcpauth"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/scheduler"
	"github.com/mrdon/kit/internal/services"
	kitslack "github.com/mrdon/kit/internal/slack"
	"github.com/mrdon/kit/internal/tools"
)

func jobMCPHandler(name string, _ *pgxpool.Pool, svc *services.Services, llm *anthropic.Client) mcpserver.ToolHandlerFunc {
	switch name {
	case "create_job":
		return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
			return handleMCPCreateTask(ctx, req, caller, svc, llm)
		})
	case "list_jobs":
		return mcpauth.WithCaller(func(ctx context.Context, _ mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
			jobs, err := svc.Jobs.List(ctx, caller)
			if err != nil {
				return nil, err
			}
			if len(jobs) == 0 {
				return mcp.NewToolResultText("No scheduled jobs."), nil
			}
			var b strings.Builder
			b.WriteString("Scheduled jobs:\n")
			for _, t := range jobs {
				status := string(t.Status)
				if t.LastError != nil {
					status += " (error: " + *t.LastError + ")"
				}
				schedule := "cron: `" + t.CronExpr + "`"
				if t.RunOnce {
					schedule = "one-time"
				}
				fmt.Fprintf(&b, "- [%s] %s | %s | next: %s | status: %s",
					t.ID, t.Description, schedule, t.NextRunAt.In(caller.Location()).Format("Mon Jan 2 3:04 PM MST"), status)
				if policySummary := services.FormatTaskPolicySummary(t.Config); policySummary != "" {
					fmt.Fprintf(&b, " | %s", policySummary)
				}
				b.WriteByte('\n')
			}
			return mcp.NewToolResultText(b.String()), nil
		})
	case "update_job":
		return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
			idStr, _ := req.RequireString("id")
			jobID, err := uuid.Parse(idStr)
			if err != nil {
				return mcp.NewToolResultError("Invalid job id."), nil
			}
			if req.GetBool("delete", false) {
				err = svc.Jobs.Delete(ctx, caller, jobID)
				if errors.Is(err, services.ErrNotFound) {
					return mcp.NewToolResultError("Job not found."), nil
				}
				if err != nil {
					return nil, err
				}
				return mcp.NewToolResultText("Job deleted."), nil
			}
			desc := req.GetString("description", "")
			policy, perr := policyFromMCP(req)
			if perr != "" {
				return mcp.NewToolResultError(perr), nil
			}
			update := services.UpdateInput{}
			if desc != "" {
				update.Description = &desc
			}
			if raw, ok := req.GetArguments()["skill_name"]; ok {
				if s, ok := raw.(string); ok {
					update.SkillName = &s
				}
			}
			if policy != nil {
				update.Policy = policy
			}
			if update.Description == nil && update.SkillName == nil && update.Policy == nil {
				return mcp.NewToolResultError("Provide description, skill_name, policy, or delete=true."), nil
			}
			err = svc.Jobs.Update(ctx, caller, jobID, update)
			if errors.Is(err, services.ErrNotFound) {
				if update.SkillName != nil && *update.SkillName != "" {
					return mcp.NewToolResultError(fmt.Sprintf("Job not found, or skill %q not found.", *update.SkillName)), nil
				}
				return mcp.NewToolResultError("Job not found."), nil
			}
			if err != nil {
				return nil, err
			}
			return mcp.NewToolResultText("Job updated."), nil
		})
	default:
		return nil
	}
}

// buildRunTaskTool creates the run_task MCP tool, which needs agent + encryptor
// beyond the standard handler signature.
func buildRunTaskTool(pool *pgxpool.Pool, svc *services.Services, a *agent.Agent, enc *crypto.Encryptor, sched *scheduler.Scheduler) mcpserver.ServerTool {
	schema := services.PropsReq(map[string]any{
		"id":      services.Field("string", "The job UUID to run"),
		"dry_run": services.Field("boolean", "If true, capture messages instead of posting to Slack"),
	}, "id")
	schemaJSON, _ := json.Marshal(schema)
	tool := mcp.NewToolWithRawSchema("run_job", "Run a job immediately for testing. In dry_run mode, messages are captured and returned instead of posted to Slack. You can only run jobs you created.", schemaJSON)

	handler := mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		idStr, _ := req.RequireString("id")
		jobID, err := uuid.Parse(idStr)
		if err != nil {
			return mcp.NewToolResultError("Invalid job id."), nil
		}
		dryRun := req.GetBool("dry_run", false)

		job, err := models.GetJob(ctx, pool, caller.TenantID, jobID)
		if err != nil {
			return nil, fmt.Errorf("getting job: %w", err)
		}
		if job == nil {
			return mcp.NewToolResultError("Job not found."), nil
		}

		// run_task acts as the job's creator — the scheduled agent loads
		// their integrations, memories, and email credentials. Admins don't
		// get to stand in for another user; cross-user debugging is an
		// operator/SRE concern, handled via DB/CLI, not the customer MCP.
		if job.CreatedBy != caller.UserID {
			return mcp.NewToolResultError("You can only run jobs you created."), nil
		}

		// Builtin jobs run native code, not the LLM agent
		if job.JobType == models.JobTypeBuiltin {
			if dryRun {
				return mcp.NewToolResultText(fmt.Sprintf("Dry run: builtin job %q would execute native handler.", job.Description)), nil
			}
			sched.ExecuteBuiltinTask(ctx, *job)
			return mcp.NewToolResultText(fmt.Sprintf("Builtin job %q executed.", job.Description)), nil
		}

		tenant, err := models.GetTenantByID(ctx, pool, job.TenantID)
		if err != nil || tenant == nil {
			return mcp.NewToolResultError("Tenant not found."), nil
		}

		botToken, err := enc.Decrypt(tenant.BotToken)
		if err != nil {
			return nil, fmt.Errorf("decrypting bot token: %w", err)
		}

		var slack *kitslack.Client
		if dryRun {
			slack = kitslack.NewDryRunClient(botToken)
		} else {
			slack = kitslack.NewClient(botToken)
		}

		user, err := models.GetUserByID(ctx, pool, tenant.ID, job.CreatedBy)
		if err != nil || user == nil {
			return mcp.NewToolResultError("Job author not found."), nil
		}

		authorName := user.SlackUserID
		if user.DisplayName != nil && *user.DisplayName != "" {
			authorName = *user.DisplayName
		}
		policy, perr := models.ParseConfigPolicy(job.Config)
		if perr != nil {
			return nil, fmt.Errorf("parsing job policy: %w", perr)
		}
		tc := &agent.JobContext{
			ID:            job.ID,
			Description:   job.Description,
			AuthorSlackID: user.SlackUserID,
			AuthorName:    authorName,
			Policy:        policy,
		}

		threadTS := fmt.Sprintf("job-%s-%d", job.ID, time.Now().UnixMilli())
		session, err := models.CreateSession(ctx, pool, tenant.ID, job.ChannelID, threadTS, user.ID, true)
		if err != nil {
			return nil, fmt.Errorf("creating session: %w", err)
		}

		userText := job.Description
		if job.SkillID != nil {
			skill, serr := models.GetSkill(ctx, pool, tenant.ID, *job.SkillID)
			if serr != nil {
				return nil, fmt.Errorf("loading skill %s: %w", *job.SkillID, serr)
			}
			if skill == nil {
				return mcp.NewToolResultError("Job's skill no longer exists."), nil
			}
			userText = fmt.Sprintf(
				"Load the skill named %q (call load_skill with skill_id=%q) and follow its instructions.",
				skill.Name, skill.Name,
			)
		}
		// The agent loop can run for tens of seconds (LLM calls + IMAP +
		// tool fan-out) — well past nginx's proxy_read_timeout and the MCP
		// transport's tolerance. Spawn it on a background ctx so the HTTP
		// request can return immediately with a run_id; the caller polls
		// get_job_status to read progress and dry-run captures off the
		// session_events log.
		runIn := agent.RunInput{
			Slack:    slack,
			Tenant:   tenant,
			User:     user,
			Session:  session,
			Channel:  job.ChannelID,
			UserText: userText,
			Job:      tc,
			Model:    job.Model,
		}
		go func() {
			runCtx := context.Background()
			runErr := a.Run(runCtx, runIn)
			if dryRun {
				_ = models.AppendSessionEvent(runCtx, pool, tenant.ID, session.ID,
					models.EventTypeDryRunCaptures, slack.Captured)
			}
			if runErr != nil {
				_ = models.AppendSessionEvent(runCtx, pool, tenant.ID, session.ID,
					models.EventTypeError, map[string]any{"error": runErr.Error()})
			}
		}()

		mode := "live"
		if dryRun {
			mode = "dry-run"
		}
		return mcp.NewToolResultText(fmt.Sprintf(
			"Job %q started (%s, run_id=%s).\n\nCall get_job_status with run_id=%s to check progress and read results.",
			job.Description, mode, session.ID, session.ID,
		)), nil
	})

	return mcpserver.ServerTool{Tool: tool, Handler: handler}
}

// buildGetJobStatusTool creates the get_job_status MCP tool, which reads
// the session log for a run started by run_task and renders status,
// tool calls so far, errors, and dry-run captured messages.
func buildGetJobStatusTool(pool *pgxpool.Pool, svc *services.Services) mcpserver.ServerTool {
	schema := services.PropsReq(map[string]any{
		"run_id": services.Field("string", "The run_id returned by run_task (a session UUID)."),
	}, "run_id")
	schemaJSON, _ := json.Marshal(schema)
	tool := mcp.NewToolWithRawSchema("get_job_status", "Check the status of a job run started via run_job. Returns whether the run is still going, tool calls executed, dry-run captured messages, and any errors.", schemaJSON)

	handler := mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		idStr, _ := req.RequireString("run_id")
		sessionID, err := uuid.Parse(idStr)
		if err != nil {
			return mcp.NewToolResultError("Invalid run_id."), nil
		}
		events, err := svc.Sessions.GetEvents(ctx, caller, sessionID)
		if errors.Is(err, services.ErrNotFound) {
			return mcp.NewToolResultError("Run not found."), nil
		}
		if err != nil {
			return nil, fmt.Errorf("loading run events: %w", err)
		}
		return mcp.NewToolResultText(formatTaskRunStatus(sessionID, events)), nil
	})

	return mcpserver.ServerTool{Tool: tool, Handler: handler}
}

// formatTaskRunStatus renders a session's events as a job-run status
// report. Designed for the get_job_status tool — concise enough that the
// caller doesn't drown in raw event JSON, but complete enough to debug a
// failed run without reaching for get_session_events.
func formatTaskRunStatus(sessionID uuid.UUID, events []models.SessionEvent) string {
	if len(events) == 0 {
		return fmt.Sprintf("Run %s: no events recorded yet — the run may still be initializing.", sessionID)
	}

	var (
		started      = events[0].CreatedAt
		lastEvt      = events[len(events)-1].CreatedAt
		completed    bool
		completedAt  time.Time
		toolCalls    []string
		errorMsgs    []string
		dryRunMsgs   []kitslack.CapturedMessage
		hasDryRunEvt bool
	)
	for _, e := range events {
		switch e.EventType {
		case models.EventTypeAssistantTurn:
			toolCalls = append(toolCalls, extractToolNames(e.Data)...)
		case models.EventTypeError:
			var payload map[string]any
			if json.Unmarshal(e.Data, &payload) == nil {
				if msg, ok := payload["error"].(string); ok && msg != "" {
					errorMsgs = append(errorMsgs, msg)
				}
			}
		case models.EventTypeSessionComplete:
			completed = true
			completedAt = e.CreatedAt
		case models.EventTypeDryRunCaptures:
			hasDryRunEvt = true
			_ = json.Unmarshal(e.Data, &dryRunMsgs)
		case models.EventTypeMessageReceived,
			models.EventTypeMessageSent,
			models.EventTypeLLMRequest,
			models.EventTypeLLMResponse,
			models.EventTypeToolResults,
			models.EventTypeDecisionResolved,
			models.EventTypePolicyEnforced:
			// Not surfaced in the run-status report.
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Run %s\n", sessionID)
	switch {
	case len(errorMsgs) > 0 && completed:
		fmt.Fprintf(&b, "Status: completed with errors (%s)\n", completedAt.Sub(started).Round(time.Second))
	case completed:
		fmt.Fprintf(&b, "Status: completed (%s)\n", completedAt.Sub(started).Round(time.Second))
	case len(errorMsgs) > 0:
		fmt.Fprintf(&b, "Status: failed (%s elapsed)\n", lastEvt.Sub(started).Round(time.Second))
	default:
		fmt.Fprintf(&b, "Status: running (%s elapsed)\n", time.Since(started).Round(time.Second))
	}

	if len(toolCalls) > 0 {
		fmt.Fprintf(&b, "\nTool calls (%d):\n", len(toolCalls))
		for _, name := range toolCalls {
			fmt.Fprintf(&b, "  - %s\n", name)
		}
	}

	if hasDryRunEvt {
		fmt.Fprintf(&b, "\nDry-run captured messages (%d):\n", len(dryRunMsgs))
		for i, msg := range dryRunMsgs {
			fmt.Fprintf(&b, "--- Message %d ---\n", i+1)
			fmt.Fprintf(&b, "Channel: %s\n", msg.Channel)
			if msg.ThreadTS != "" {
				fmt.Fprintf(&b, "Thread: %s\n", msg.ThreadTS)
			}
			fmt.Fprintf(&b, "Text:\n%s\n\n", msg.Text)
		}
	}

	if len(errorMsgs) > 0 {
		b.WriteString("\nErrors:\n")
		for _, msg := range errorMsgs {
			fmt.Fprintf(&b, "  - %s\n", msg)
		}
	}

	return b.String()
}

// extractToolNames pulls the names of tool_use blocks out of an
// assistant_turn event payload (which mirrors the LLM's response Content
// array). Best-effort — unrecognised shapes silently contribute nothing.
func extractToolNames(data json.RawMessage) []string {
	var payload struct {
		Content []struct {
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"content"`
	}
	if json.Unmarshal(data, &payload) != nil {
		return nil
	}
	var names []string
	for _, c := range payload.Content {
		if c.Type == "tool_use" && c.Name != "" {
			names = append(names, c.Name)
		}
	}
	return names
}

// handleMCPCreateTask factors the create_task body out of jobMCPHandler
// to keep the dispatcher's cyclomatic complexity in check. Mirrors
// internal/tools/jobs.go's handleCreateTask — per the CLAUDE.md
// shared-tool-parity rule, validation and return shape must match.
func handleMCPCreateTask(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller, svc *services.Services, llm *anthropic.Client) (*mcp.CallToolResult, error) {
	desc, _ := req.RequireString("description")
	skillName := req.GetString("skill_name", "")
	cronExpr := req.GetString("cron_expr", "")
	runAtStr := req.GetString("run_at", "")
	channelID := req.GetString("channel_id", "")
	scope := req.GetString("scope", "user")

	if cronExpr == "" && runAtStr == "" {
		return mcp.NewToolResultError("Provide either cron_expr (recurring) or run_at (one-time)."), nil
	}
	if cronExpr != "" && runAtStr != "" {
		return mcp.NewToolResultError("Provide cron_expr or run_at, not both."), nil
	}

	runOnce := runAtStr != ""
	var runAt *time.Time
	if runOnce {
		parsed, msg := parseMCPRunAt(runAtStr, caller.Timezone)
		if msg != "" {
			return mcp.NewToolResultError(msg), nil
		}
		runAt = parsed
	}

	policy, perr := policyFromMCP(req)
	if perr != "" {
		return mcp.NewToolResultError(perr), nil
	}

	model := tools.ClassifyTaskModel(ctx, llm, desc)
	job, err := svc.Jobs.Create(ctx, caller, services.CreateInput{
		Description: desc,
		SkillName:   skillName,
		CronExpr:    cronExpr,
		Timezone:    caller.Timezone,
		ChannelID:   channelID,
		Scope:       scope,
		Model:       model,
		RunOnce:     runOnce,
		RunAt:       runAt,
		Policy:      policy,
	})
	if errors.Is(err, services.ErrForbidden) {
		return mcp.NewToolResultError("Insufficient permissions for this scope."), nil
	}
	if errors.Is(err, services.ErrNotFound) {
		return mcp.NewToolResultError(fmt.Sprintf("Skill %q not found.", skillName)), nil
	}
	if err != nil {
		return nil, err
	}
	label := "Next run"
	if runOnce {
		label = "Runs at"
	}
	return mcp.NewToolResultText(fmt.Sprintf("Job created (ID: %s, model: %s). %s: %s",
		job.ID, job.Model, label, job.NextRunAt.In(caller.Location()).Format("Mon Jan 2 3:04 PM MST"))), nil
}

// parseMCPRunAt parses the run_at string in the caller's timezone.
// Returns (parsed, "") on success, (nil, userMessage) on parse failure
// or past-date. Mirrors the agent-side ISO-8601 format acceptance.
func parseMCPRunAt(runAtStr, timezone string) (*time.Time, string) {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, fmt.Sprintf("Invalid timezone %q.", timezone)
	}
	t, err := time.ParseInLocation("2006-01-02T15:04:05", runAtStr, loc)
	if err != nil {
		t, err = time.ParseInLocation("2006-01-02T15:04", runAtStr, loc)
	}
	if err != nil {
		return nil, "Invalid run_at format. Use ISO 8601: 2026-04-05T21:20:00"
	}
	if t.Before(time.Now()) {
		return nil, "run_at must be in the future."
	}
	return &t, ""
}

// policyFromMCP extracts and parses the optional "policy" argument from
// an MCP create_task / update_task request. Returns (nil, "") when no
// policy was supplied. Returns (nil, userMessage) on validation failure
// (syntactic only — tool-name validation is deferred to fire time at
// the MCP surface since this code path doesn't have a per-caller
// tools.Registry handy).
func policyFromMCP(req mcp.CallToolRequest) (*models.Policy, string) {
	raw, ok := req.GetArguments()["policy"]
	if !ok || raw == nil {
		return nil, ""
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, "Invalid policy: " + err.Error()
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	var p models.Policy
	if err := dec.Decode(&p); err != nil {
		return nil, "Invalid policy: " + err.Error()
	}
	return &p, ""
}
