package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/apps/cards/shared"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	kitslack "github.com/mrdon/kit/internal/slack"
)

// cardProvider surfaces tasks as stack items. Scope is explicit — only
// tasks the user is the assignee of, or holds the role_scope for. Tenant-
// wide rows are excluded so the stack doesn't flood with public tasks
// belonging to unrelated parts of the org.
type cardProvider struct {
	app *TaskApp
}

func (p *cardProvider) SourceApp() string { return "task" }

// stackTask bundles a Task with the human-readable scope info needed to
// render the swipe stack metadata (assignee name, role name).
type stackTask struct {
	Task
	AssigneeID    *uuid.UUID
	AssigneeName  string
	RoleScopeName string
}

func (p *cardProvider) StackItems(ctx context.Context, caller *services.Caller, cursor string, limit int) (shared.StackPage, error) {
	_ = cursor
	if limit <= 0 {
		limit = 50
	}
	tasks, err := listStackTasks(ctx, p.app.svc.pool, caller, limit, false)
	if err != nil {
		return shared.StackPage{}, err
	}
	items := make([]shared.StackItem, 0, len(tasks))
	for i := range tasks {
		it, err := taskToStackItem(&tasks[i])
		if err != nil {
			return shared.StackPage{}, err
		}
		items = append(items, it)
	}

	// Digest card: one footer item listing everything in the snoozed
	// pile so users have a way to see what they've deferred. Only
	// emitted when there's at least one snoozed row; otherwise the
	// feed ends cleanly on active cards (or the empty state).
	snoozed, err := listStackTasks(ctx, p.app.svc.pool, caller, limit, true)
	if err != nil {
		return shared.StackPage{}, err
	}
	if len(snoozed) > 0 {
		digest, err := buildSnoozedDigest(caller, snoozed)
		if err != nil {
			return shared.StackPage{}, err
		}
		items = append(items, digest)
	}
	return shared.StackPage{Items: items}, nil
}

func (p *cardProvider) GetItem(ctx context.Context, caller *services.Caller, kind, id string) (*shared.DetailResponse, error) {
	if kind == "snoozed_digest" {
		// Back-nav or deep-link to the digest after the pile has
		// changed (user woke the last task, etc.) — re-run the
		// query and return a fresh item. A 0-row digest is
		// friendlier than 404ing the user mid-navigation.
		snoozed, err := listStackTasks(ctx, p.app.svc.pool, caller, 100, true)
		if err != nil {
			return nil, err
		}
		item, err := buildSnoozedDigest(caller, snoozed)
		if err != nil {
			return nil, err
		}
		return &shared.DetailResponse{Item: item}, nil
	}
	if kind != "task" {
		return nil, services.ErrNotFound
	}
	taskID, err := uuid.Parse(id)
	if err != nil {
		return nil, services.ErrNotFound
	}
	t, events, err := p.app.svc.Get(ctx, caller, taskID)
	if err != nil {
		return nil, err
	}
	enriched, err := enrichOne(ctx, p.app.svc.pool, caller.TenantID, t)
	if err != nil {
		return nil, err
	}
	item, err := taskToStackItem(enriched)
	if err != nil {
		return nil, err
	}
	encodedEvents, err := json.Marshal(events)
	if err != nil {
		return nil, fmt.Errorf("encoding events: %w", err)
	}
	return &shared.DetailResponse{
		Item:   item,
		Extras: map[string]json.RawMessage{"events": encodedEvents},
	}, nil
}

// enrichOne joins a single task with role/assignee info to populate the
// human-readable metadata fields. Used by GetItem (single-row path).
// AssigneeID lives on the task row directly; the role comes from the
// scope. Anyone in the role can see the task regardless of assignee.
func enrichOne(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, t *Task) (*stackTask, error) {
	st := &stackTask{Task: *t}
	st.AssigneeID = t.AssigneeUserID
	var assigneeName, roleName *string
	err := pool.QueryRow(ctx, `
		SELECT u.display_name, r.name
		FROM scopes s
		LEFT JOIN roles r ON r.id = s.role_id
		LEFT JOIN users u ON u.id = $3
		WHERE s.id = $1 AND s.tenant_id = $2`,
		t.ScopeID, tenantID, t.AssigneeUserID,
	).Scan(&assigneeName, &roleName)
	if err != nil {
		return nil, fmt.Errorf("loading scope info: %w", err)
	}
	if assigneeName != nil {
		st.AssigneeName = *assigneeName
	}
	if roleName != nil {
		st.RoleScopeName = *roleName
	}
	return st, nil
}

func (p *cardProvider) DoAction(ctx context.Context, caller *services.Caller, kind, id, actionID string, params json.RawMessage) (*shared.ActionResult, error) {
	if kind != "task" {
		return nil, services.ErrNotFound
	}
	taskID, err := uuid.Parse(id)
	if err != nil {
		return nil, services.ErrNotFound
	}
	switch actionID {
	case "complete":
		t, err := p.app.svc.Complete(ctx, caller, taskID)
		if err != nil {
			// Idempotent: a second complete on an already-done task still
			// removes it from the client's stack without an error.
			if errors.Is(err, services.ErrNotFound) {
				return nil, err
			}
		}
		_ = t
		return &shared.ActionResult{RemovedIDs: []string{shared.Key("task", "task", id)}}, nil
	case "snooze":
		var body struct {
			Days int `json:"days"`
		}
		if len(params) > 0 {
			if err := json.Unmarshal(params, &body); err != nil {
				return nil, fmt.Errorf("invalid snooze params: %w", err)
			}
		}
		if _, err := p.app.svc.SnoozeDays(ctx, caller, taskID, body.Days); err != nil {
			return nil, err
		}
		return &shared.ActionResult{RemovedIDs: []string{shared.Key("task", "task", id)}}, nil
	case "snooze_until_monday":
		if _, err := p.app.svc.SnoozeUntilNextMonday(ctx, caller, taskID); err != nil {
			return nil, err
		}
		return &shared.ActionResult{RemovedIDs: []string{shared.Key("task", "task", id)}}, nil
	case "wake":
		// Clear the snooze so the task reappears in the active feed.
		// Surface affordance: only shown on cards whose snoozed_until
		// is in the future, so the client never fires this on an
		// already-active task. No RemovedIDs — the client navigates
		// back to / and refetches, which surfaces the now-awake row.
		clear := UpdateInput{ClearSnooze: true}
		if _, err := p.app.svc.Update(ctx, caller, taskID, clear); err != nil {
			return nil, err
		}
		return &shared.ActionResult{}, nil
	case "assign_to_me":
		// Quick action from the unassigned-in-my-roles feed branch:
		// claim a task so it becomes "on my desk" without leaving the
		// stack. The card stays — the client refetches to update the
		// assignee chip.
		me := caller.UserID
		if _, err := p.app.svc.Update(ctx, caller, taskID, UpdateInput{NewAssigneeUserID: &me}); err != nil {
			return nil, err
		}
		return &shared.ActionResult{}, nil
	case "delete":
		if _, err := p.app.svc.Cancel(ctx, caller, taskID); err != nil {
			return nil, err
		}
		return &shared.ActionResult{RemovedIDs: []string{shared.Key("task", "task", id)}}, nil
	case "resolve":
		var body struct {
			ResolutionID string `json:"resolution_id"`
		}
		if len(params) > 0 {
			if err := json.Unmarshal(params, &body); err != nil {
				return nil, fmt.Errorf("invalid resolve params: %w", err)
			}
		}
		if body.ResolutionID == "" {
			return nil, errors.New("resolution_id is required")
		}
		return p.acceptResolution(ctx, caller, taskID, body.ResolutionID)
	case "regenerate_resolutions":
		if p.app.llm == nil {
			return nil, errors.New("task app not configured with an LLM client")
		}
		task, _, err := p.app.svc.Get(ctx, caller, taskID)
		if err != nil {
			return nil, err
		}
		// Fire-and-forget: the suggester goroutine writes new
		// resolutions to the row when Haiku returns. The client polls
		// getItem until it sees a change (new resolution ids) and
		// swaps the UI out of the spinning state.
		go runResolutionSuggester(p.app.svc.pool, p.app.llm, *caller, *task)
		return &shared.ActionResult{}, nil
	}
	return nil, fmt.Errorf("unknown task action %q", actionID)
}

// acceptResolution turns a tapped resolution chip into a run-once or
// recurring job. The job posts its output to the caller's DM, mirroring
// how card decisions resolve. Removes the accepted chip from the stored
// array and returns the patched item so the client drops the chip
// without refetching.
func (p *cardProvider) acceptResolution(ctx context.Context, caller *services.Caller, taskID uuid.UUID, resolutionID string) (*shared.ActionResult, error) {
	if p.app.taskSvc == nil || p.app.enc == nil {
		return nil, errors.New("task app not fully configured (missing job service or encryptor)")
	}

	task, _, err := p.app.svc.Get(ctx, caller, taskID)
	if err != nil {
		return nil, err
	}

	var chosen *Resolution
	for i := range task.Resolutions {
		if task.Resolutions[i].ID == resolutionID {
			chosen = &task.Resolutions[i]
			break
		}
	}
	if chosen == nil {
		return nil, services.ErrNotFound
	}

	dmChannel, err := openCallerDM(ctx, p.app.svc.pool, p.app.enc, caller)
	if err != nil {
		return nil, fmt.Errorf("opening DM: %w", err)
	}

	in := services.CreateInput{
		Description: chosen.Prompt,
		Timezone:    "UTC",
		ChannelID:   dmChannel,
		Scope:       "user",
		RunOnce:     chosen.Shape == "once",
	}
	if chosen.Shape == "once" {
		now := time.Now()
		in.RunAt = &now
	} else {
		in.CronExpr = chosen.Cron
	}
	if _, err := p.app.taskSvc.Create(ctx, caller, in); err != nil {
		return nil, fmt.Errorf("creating job: %w", err)
	}

	if err := removeTaskResolution(ctx, p.app.svc.pool, caller.TenantID, taskID, resolutionID); err != nil {
		return nil, err
	}

	// Rebuild the patched stack item so the client drops the accepted
	// chip without a refetch.
	updated, _, err := p.app.svc.Get(ctx, caller, taskID)
	if err != nil {
		return nil, err
	}
	enriched, err := enrichOne(ctx, p.app.svc.pool, caller.TenantID, updated)
	if err != nil {
		return nil, err
	}
	item, err := taskToStackItem(enriched)
	if err != nil {
		return nil, err
	}
	return &shared.ActionResult{Item: &item}, nil
}

// openCallerDM looks up the tenant's bot token, decrypts it, and opens
// a DM channel to the caller. Mirrors cards/mcp.go:slackClientForCaller
// so tapped-chip output lands exactly where resolved decisions do.
func openCallerDM(ctx context.Context, pool *pgxpool.Pool, enc cryptoDecryptor, caller *services.Caller) (string, error) {
	tenant, err := models.GetTenantByID(ctx, pool, caller.TenantID)
	if err != nil {
		return "", fmt.Errorf("looking up tenant: %w", err)
	}
	if tenant == nil {
		return "", errors.New("tenant not found")
	}
	token, err := enc.Decrypt(tenant.BotToken)
	if err != nil {
		return "", fmt.Errorf("decrypting bot token: %w", err)
	}
	client := kitslack.NewClient(token)
	return client.OpenConversation(ctx, caller.Identity)
}

// cryptoDecryptor is the narrow slice of *crypto.Encryptor this file
// needs. Declared as an interface so the accept path can be exercised
// with a stub in tests without hauling in the real encryptor.
type cryptoDecryptor interface {
	Decrypt(ciphertext string) (string, error)
}

// listStackTasks restricts to the caller's personal feed: tasks assigned
// to them, plus unassigned tasks in roles they hold. Without this filter
// every team member's stack would balloon with every team task. Admins
// still hit this filter — the swipe feed is personal even for admins; for
// auditing across users, list_tasks via MCP is the path.
//
// snoozedOnly=false returns the active feed (currently visible); snoozedOnly=
// true returns the snoozed pile (hidden from the feed, surfaced via the
// digest card).
func listStackTasks(ctx context.Context, pool *pgxpool.Pool, c *services.Caller, limit int, snoozedOnly bool) ([]stackTask, error) {
	var b strings.Builder
	args := []any{c.TenantID}

	b.WriteString(`SELECT t.id, t.tenant_id, t.title, t.description, t.status, t.priority, t.blocked_reason,
		t.scope_id, t.assignee_user_id, t.due_date, t.snoozed_until, t.resolutions, t.created_at, t.updated_at, t.closed_at,
		u.display_name, r.name
		FROM app_tasks t
		JOIN scopes s ON s.id = t.scope_id
		LEFT JOIN roles r ON r.id = s.role_id
		LEFT JOIN users u ON u.id = t.assignee_user_id
		WHERE t.tenant_id = $1
		  AND t.status NOT IN ('done','cancelled')`)
	if snoozedOnly {
		b.WriteString(`
		  AND t.snoozed_until IS NOT NULL AND t.snoozed_until > now()`)
	} else {
		b.WriteString(`
		  AND (t.snoozed_until IS NULL OR t.snoozed_until <= now())`)
	}

	// Personal feed: assigned to me OR unassigned in a role I hold.
	args = append(args, c.UserID)
	userParam := len(args)
	b.WriteString(fmt.Sprintf(` AND (t.assignee_user_id = $%d`, userParam))
	if len(c.RoleIDs) > 0 {
		args = append(args, c.RoleIDs)
		roleParam := len(args)
		b.WriteString(fmt.Sprintf(` OR (t.assignee_user_id IS NULL AND s.role_id = ANY($%d))`, roleParam))
	}
	b.WriteString(`)`)

	if snoozedOnly {
		// Snoozed pile: order by priority then due, with nearest wake
		// time last — the user scans the digest for what's coming back.
		b.WriteString(`
		ORDER BY
			CASE t.priority
				WHEN 'urgent' THEN 0
				WHEN 'high'   THEN 1
				WHEN 'medium' THEN 2
				WHEN 'low'    THEN 3
			END,
			t.due_date ASC NULLS LAST,
			t.snoozed_until ASC
		LIMIT `)
	} else {
		b.WriteString(`
		ORDER BY
			CASE
				WHEN t.due_date < CURRENT_DATE THEN 0
				WHEN t.due_date <= CURRENT_DATE + INTERVAL '3 days' THEN 1
				ELSE 2
			END,
			CASE t.priority
				WHEN 'urgent' THEN 0
				WHEN 'high'   THEN 1
				WHEN 'medium' THEN 2
				WHEN 'low'    THEN 3
			END,
			t.due_date ASC NULLS LAST,
			t.created_at DESC
		LIMIT `)
	}
	fmt.Fprintf(&b, "%d", limit)

	rows, err := pool.Query(ctx, b.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("listing stack tasks: %w", err)
	}
	defer rows.Close()

	var out []stackTask
	for rows.Next() {
		var st stackTask
		var description, blockedReason, assigneeName, roleName *string
		var dueDate, snoozedUntil *time.Time
		var resolutionsJSON []byte
		if err := rows.Scan(
			&st.ID, &st.TenantID, &st.Title, &description,
			&st.Status, &st.Priority, &blockedReason,
			&st.ScopeID, &st.AssigneeUserID, &dueDate, &snoozedUntil,
			&resolutionsJSON,
			&st.CreatedAt, &st.UpdatedAt, &st.ClosedAt,
			&assigneeName, &roleName,
		); err != nil {
			return nil, fmt.Errorf("scanning stack task: %w", err)
		}
		st.AssigneeID = st.AssigneeUserID
		if description != nil {
			st.Description = *description
		}
		if blockedReason != nil {
			st.BlockedReason = *blockedReason
		}
		st.DueDate = dueDate
		st.SnoozedUntil = snoozedUntil
		if len(resolutionsJSON) > 0 {
			if err := json.Unmarshal(resolutionsJSON, &st.Resolutions); err != nil {
				return nil, fmt.Errorf("decoding stack task resolutions: %w", err)
			}
		}
		if assigneeName != nil {
			st.AssigneeName = *assigneeName
		}
		if roleName != nil {
			st.RoleScopeName = *roleName
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func taskToStackItem(t *stackTask) (shared.StackItem, error) {
	it := shared.StackItem{
		SourceApp:    "task",
		Kind:         "task",
		KindLabel:    "Task",
		Icon:         "📋",
		ID:           t.ID.String(),
		Title:        t.Title,
		Body:         t.Description,
		KindWeight:   2,
		PriorityTier: taskTier(&t.Task),
		CreatedAt:    t.CreatedAt,
		Actions: []shared.StackAction{
			{ID: "complete", Direction: "right", Label: "Complete", Emoji: "✅"},
			{ID: "snooze", Direction: "left", Label: "Snooze 1 day", Emoji: "😴", Params: json.RawMessage(`{"days":1}`)},
		},
	}
	for _, r := range t.Resolutions {
		if r.Kind != ResolutionKindTask {
			continue
		}
		params, err := json.Marshal(map[string]string{"resolution_id": r.ID})
		if err != nil {
			return it, fmt.Errorf("encoding resolution params: %w", err)
		}
		it.Actions = append(it.Actions, shared.StackAction{
			ID:        "resolve",
			Direction: "tap",
			Label:     r.Label,
			Emoji:     "✨",
			Params:    params,
		})
	}
	if len(t.Resolutions) > 0 {
		first := t.Resolutions[0]
		it.RecommendedNextStep = &shared.RecommendedNextStep{
			Kind:  first.Kind,
			Label: first.Label,
			Body:  first.Body,
		}
	}
	if badge, ok := dueBadge(t.DueDate); ok {
		it.Badges = append(it.Badges, badge)
	}
	// Wake action is only reachable from the digest-linked detail,
	// where the task is always snoozed. Gate server-side too so the
	// action never appears on an already-active card.
	if t.SnoozedUntil != nil && t.SnoozedUntil.After(time.Now()) {
		it.Actions = append(it.Actions, shared.StackAction{
			ID:        "wake",
			Direction: "tap",
			Label:     "Wake now",
			Emoji:     "⏰",
		})
	}
	meta, err := json.Marshal(map[string]any{
		"due_date":         t.DueDate,
		"priority":         t.Priority,
		"status":           t.Status,
		"assignee_user_id": t.AssigneeID,
		"assignee_name":    t.AssigneeName,
		"role_scope":       t.RoleScopeName,
		"snoozed_until":    t.SnoozedUntil,
	})
	if err != nil {
		return it, fmt.Errorf("encoding task metadata: %w", err)
	}
	it.Metadata = meta
	return it, nil
}

// buildSnoozedDigest assembles the footer card that lists the caller's
// snoozed tasks. Not paginated — the pile is small and users want to
// scan it at a glance; limit is set at the query layer.
func buildSnoozedDigest(c *services.Caller, snoozed []stackTask) (shared.StackItem, error) {
	type digestRow struct {
		ID           string     `json:"id"`
		Title        string     `json:"title"`
		Priority     string     `json:"priority"`
		DueDate      *time.Time `json:"due_date,omitempty"`
		SnoozedUntil *time.Time `json:"snoozed_until"`
	}
	rows := make([]digestRow, 0, len(snoozed))
	for i := range snoozed {
		rows = append(rows, digestRow{
			ID:           snoozed[i].ID.String(),
			Title:        snoozed[i].Title,
			Priority:     snoozed[i].Priority,
			DueDate:      snoozed[i].DueDate,
			SnoozedUntil: snoozed[i].SnoozedUntil,
		})
	}
	meta, err := json.Marshal(map[string]any{"items": rows})
	if err != nil {
		return shared.StackItem{}, fmt.Errorf("encoding digest metadata: %w", err)
	}
	title := fmt.Sprintf("Snoozed (%d)", len(rows))
	return shared.StackItem{
		SourceApp: "task",
		Kind:      "snoozed_digest",
		KindLabel: "Snoozed",
		Icon:      "😴",
		ID:        c.UserID.String(),
		Title:     title,
		Body:      "Tap to see what you've put off.",
		// Minimal tier + high KindWeight keeps the digest at the very
		// bottom of the feed, after any TierMinimal briefings (weight 1).
		PriorityTier: shared.TierMinimal,
		KindWeight:   10,
		CreatedAt:    time.Now(),
		Actions:      []shared.StackAction{},
		Metadata:     meta,
	}, nil
}

// taskTier maps a task to one of the shared priority tiers. Due today or
// earlier goes to critical; due within 3 days OR priority=urgent goes to
// high; priority=high or due within 7 days goes to medium; else low.
func taskTier(t *Task) shared.PriorityTier {
	if days, ok := daysUntilDue(t.DueDate); ok {
		switch {
		case days <= 0:
			return shared.TierCritical
		case days <= 3:
			return shared.TierHigh
		case days <= 7:
			return shared.TierMedium
		}
	}
	switch t.Priority {
	case "urgent":
		return shared.TierHigh
	case "high":
		return shared.TierMedium
	}
	return shared.TierLow
}

// dueBadge builds a human-friendly badge from a due date. Returns ok=false
// when there is no due date.
func dueBadge(due *time.Time) (shared.StackBadge, bool) {
	days, ok := daysUntilDue(due)
	if !ok {
		return shared.StackBadge{}, false
	}
	switch {
	case days < 0:
		return shared.StackBadge{Label: fmt.Sprintf("Overdue %dd", -days), Tone: "urgent"}, true
	case days == 0:
		return shared.StackBadge{Label: "Due today", Tone: "urgent"}, true
	case days == 1:
		return shared.StackBadge{Label: "Due tomorrow", Tone: "warn"}, true
	case days <= 7:
		return shared.StackBadge{Label: fmt.Sprintf("Due in %dd", days), Tone: "warn"}, true
	default:
		return shared.StackBadge{Label: fmt.Sprintf("Due in %dd", days), Tone: "info"}, true
	}
}

// daysUntilDue returns the calendar-day delta (due - today) using each
// date's own year/month/day — no timezone conversion. Postgres DATE
// values arrive as UTC-midnight time.Time values; converting them to the
// server's local zone would shift the day backwards for any server west
// of UTC. We treat the due date as a calendar date (semantically
// timezone-less) and compare against today's calendar date in UTC so both
// sides share a frame.
func daysUntilDue(due *time.Time) (int, bool) {
	if due == nil {
		return 0, false
	}
	today := time.Now().UTC()
	todayDay := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	dueDay := time.Date(due.Year(), due.Month(), due.Day(), 0, 0, 0, 0, time.UTC)
	return int(dueDay.Sub(todayDay).Hours() / 24), true
}
