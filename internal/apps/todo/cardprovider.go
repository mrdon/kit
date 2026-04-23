package todo

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

// cardProvider surfaces todos as stack items. Scope is explicit — only
// todos the user is the assignee of, or holds the role_scope for. Tenant-
// wide rows are excluded so the stack doesn't flood with public todos
// belonging to unrelated parts of the org.
type cardProvider struct {
	app *TodoApp
}

func (p *cardProvider) SourceApp() string { return "todo" }

// stackTodo bundles a Todo with the human-readable scope info needed to
// render the swipe stack metadata (assignee name, role name).
type stackTodo struct {
	Todo
	AssigneeID    *uuid.UUID
	AssigneeName  string
	RoleScopeName string
}

func (p *cardProvider) StackItems(ctx context.Context, caller *services.Caller, cursor string, limit int) (shared.StackPage, error) {
	_ = cursor
	if limit <= 0 {
		limit = 50
	}
	todos, err := listStackTodos(ctx, p.app.svc.pool, caller, limit)
	if err != nil {
		return shared.StackPage{}, err
	}
	items := make([]shared.StackItem, 0, len(todos))
	for i := range todos {
		it, err := todoToStackItem(&todos[i])
		if err != nil {
			return shared.StackPage{}, err
		}
		items = append(items, it)
	}
	return shared.StackPage{Items: items}, nil
}

func (p *cardProvider) GetItem(ctx context.Context, caller *services.Caller, kind, id string) (*shared.DetailResponse, error) {
	if kind != "todo" {
		return nil, services.ErrNotFound
	}
	todoID, err := uuid.Parse(id)
	if err != nil {
		return nil, services.ErrNotFound
	}
	t, events, err := p.app.svc.Get(ctx, caller, todoID)
	if err != nil {
		return nil, err
	}
	enriched, err := enrichOne(ctx, p.app.svc.pool, caller.TenantID, t)
	if err != nil {
		return nil, err
	}
	item, err := todoToStackItem(enriched)
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

// enrichOne joins a single todo with scope/role/user info to populate the
// human-readable metadata fields. Used by GetItem (single-row path).
func enrichOne(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, t *Todo) (*stackTodo, error) {
	st := &stackTodo{Todo: *t}
	var assigneeID *uuid.UUID
	var assigneeName, roleName *string
	err := pool.QueryRow(ctx, `
		SELECT s.user_id, u.display_name, r.name
		FROM scopes s
		LEFT JOIN users u ON u.id = s.user_id
		LEFT JOIN roles r ON r.id = s.role_id
		WHERE s.id = $1 AND s.tenant_id = $2`,
		t.ScopeID, tenantID,
	).Scan(&assigneeID, &assigneeName, &roleName)
	if err != nil {
		return nil, fmt.Errorf("loading scope info: %w", err)
	}
	st.AssigneeID = assigneeID
	if assigneeName != nil {
		st.AssigneeName = *assigneeName
	}
	if roleName != nil {
		st.RoleScopeName = *roleName
	}
	return st, nil
}

func (p *cardProvider) DoAction(ctx context.Context, caller *services.Caller, kind, id, actionID string, params json.RawMessage) (*shared.ActionResult, error) {
	if kind != "todo" {
		return nil, services.ErrNotFound
	}
	todoID, err := uuid.Parse(id)
	if err != nil {
		return nil, services.ErrNotFound
	}
	switch actionID {
	case "complete":
		t, err := p.app.svc.Complete(ctx, caller, todoID)
		if err != nil {
			// Idempotent: a second complete on an already-done todo still
			// removes it from the client's stack without an error.
			if errors.Is(err, services.ErrNotFound) {
				return nil, err
			}
		}
		_ = t
		return &shared.ActionResult{RemovedIDs: []string{shared.Key("todo", "todo", id)}}, nil
	case "snooze":
		var body struct {
			Days int `json:"days"`
		}
		if len(params) > 0 {
			if err := json.Unmarshal(params, &body); err != nil {
				return nil, fmt.Errorf("invalid snooze params: %w", err)
			}
		}
		if _, err := p.app.svc.SnoozeDays(ctx, caller, todoID, body.Days); err != nil {
			return nil, err
		}
		return &shared.ActionResult{RemovedIDs: []string{shared.Key("todo", "todo", id)}}, nil
	case "delete":
		if _, err := p.app.svc.Cancel(ctx, caller, todoID); err != nil {
			return nil, err
		}
		return &shared.ActionResult{RemovedIDs: []string{shared.Key("todo", "todo", id)}}, nil
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
		return p.acceptResolution(ctx, caller, todoID, body.ResolutionID)
	}
	return nil, fmt.Errorf("unknown todo action %q", actionID)
}

// acceptResolution turns a tapped resolution chip into a run-once or
// recurring task. The task posts its output to the caller's DM, mirroring
// how card decisions resolve. Removes the accepted chip from the stored
// array and returns the patched item so the client drops the chip
// without refetching.
func (p *cardProvider) acceptResolution(ctx context.Context, caller *services.Caller, todoID uuid.UUID, resolutionID string) (*shared.ActionResult, error) {
	if p.app.taskSvc == nil || p.app.enc == nil {
		return nil, errors.New("todo app not fully configured (missing task service or encryptor)")
	}

	todo, _, err := p.app.svc.Get(ctx, caller, todoID)
	if err != nil {
		return nil, err
	}

	var chosen *Resolution
	for i := range todo.Resolutions {
		if todo.Resolutions[i].ID == resolutionID {
			chosen = &todo.Resolutions[i]
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
		return nil, fmt.Errorf("creating task: %w", err)
	}

	if err := removeTodoResolution(ctx, p.app.svc.pool, caller.TenantID, todoID, resolutionID); err != nil {
		return nil, err
	}

	// Rebuild the patched stack item so the client drops the accepted
	// chip without a refetch.
	updated, _, err := p.app.svc.Get(ctx, caller, todoID)
	if err != nil {
		return nil, err
	}
	enriched, err := enrichOne(ctx, p.app.svc.pool, caller.TenantID, updated)
	if err != nil {
		return nil, err
	}
	item, err := todoToStackItem(enriched)
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

// listStackTodos restricts to the caller's personal surface (scopes that
// belong to them, by user or role membership) — tenant-wide public todos are
// excluded so the stack stays personal. JOINs roles/users for display fields.
func listStackTodos(ctx context.Context, pool *pgxpool.Pool, c *services.Caller, limit int) ([]stackTodo, error) {
	var b strings.Builder
	args := []any{c.TenantID}

	b.WriteString(`SELECT t.id, t.tenant_id, t.title, t.description, t.status, t.priority, t.blocked_reason,
		t.scope_id, t.visibility, t.due_date, t.snoozed_until, t.resolutions, t.created_at, t.updated_at, t.closed_at,
		s.user_id, u.display_name, r.name
		FROM app_todos t
		JOIN scopes s ON s.id = t.scope_id
		LEFT JOIN users u ON u.id = s.user_id
		LEFT JOIN roles r ON r.id = s.role_id
		WHERE t.tenant_id = $1
		  AND t.status NOT IN ('done','cancelled')
		  AND (t.snoozed_until IS NULL OR t.snoozed_until <= now())`)

	if !c.IsAdmin {
		// Personal surface: caller is the assignee or holds the role.
		// Tenant-wide (s.user_id IS NULL AND s.role_id IS NULL) is excluded.
		scopeFrag, scopeArgs := c.PersonalScopeFilter("s", 2)
		args = append(args, scopeArgs...)
		b.WriteString(` AND `)
		b.WriteString(scopeFrag)
	}

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
	fmt.Fprintf(&b, "%d", limit)

	rows, err := pool.Query(ctx, b.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("listing stack todos: %w", err)
	}
	defer rows.Close()

	var out []stackTodo
	for rows.Next() {
		var st stackTodo
		var description, blockedReason, assigneeName, roleName *string
		var dueDate, snoozedUntil *time.Time
		var resolutionsJSON []byte
		if err := rows.Scan(
			&st.ID, &st.TenantID, &st.Title, &description,
			&st.Status, &st.Priority, &blockedReason,
			&st.ScopeID, &st.Visibility, &dueDate, &snoozedUntil,
			&resolutionsJSON,
			&st.CreatedAt, &st.UpdatedAt, &st.ClosedAt,
			&st.AssigneeID, &assigneeName, &roleName,
		); err != nil {
			return nil, fmt.Errorf("scanning stack todo: %w", err)
		}
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
				return nil, fmt.Errorf("decoding stack todo resolutions: %w", err)
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

func todoToStackItem(t *stackTodo) (shared.StackItem, error) {
	it := shared.StackItem{
		SourceApp:    "todo",
		Kind:         "todo",
		KindLabel:    "Todo",
		Icon:         "📋",
		ID:           t.ID.String(),
		Title:        t.Title,
		Body:         t.Description,
		KindWeight:   2,
		PriorityTier: todoTier(&t.Todo),
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
	meta, err := json.Marshal(map[string]any{
		"due_date":         t.DueDate,
		"priority":         t.Priority,
		"status":           t.Status,
		"visibility":       t.Visibility,
		"assigned_to":      t.AssigneeID,
		"assigned_to_name": t.AssigneeName,
		"role_scope":       t.RoleScopeName,
	})
	if err != nil {
		return it, fmt.Errorf("encoding todo metadata: %w", err)
	}
	it.Metadata = meta
	return it, nil
}

// todoTier maps a todo to one of the shared priority tiers. Due today or
// earlier goes to critical; due within 3 days OR priority=urgent goes to
// high; priority=high or due within 7 days goes to medium; else low.
func todoTier(t *Todo) shared.PriorityTier {
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
