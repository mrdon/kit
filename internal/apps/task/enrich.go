package task

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// taskView is a Task plus the display names the console UI needs. The list
// query stores only UUIDs (assignee_user_id, scope_id); the swipe feed and
// MCP render names lazily, but a board/list grouped by role or assignee
// would otherwise show raw UUIDs. Embedding *Task promotes its JSON fields.
type taskView struct {
	*Task
	AssigneeName string `json:"assignee_name,omitempty"`
	RoleName     string `json:"role_name,omitempty"`
}

// eventView is a TaskEvent plus the author's display name.
type eventView struct {
	*TaskEvent
	AuthorName string `json:"author_name,omitempty"`
}

// enrichTasks resolves assignee + role display names for a page of tasks
// in two batched lookups (no N+1). Names that can't be resolved are left
// empty rather than failing the whole response.
func enrichTasks(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, tasks []Task) ([]taskView, error) {
	scopeIDs := make([]uuid.UUID, 0, len(tasks))
	userIDs := make([]uuid.UUID, 0, len(tasks))
	for i := range tasks {
		scopeIDs = append(scopeIDs, tasks[i].ScopeID)
		if tasks[i].AssigneeUserID != nil {
			userIDs = append(userIDs, *tasks[i].AssigneeUserID)
		}
	}

	roleByScope, err := roleNamesByScope(ctx, pool, tenantID, scopeIDs)
	if err != nil {
		return nil, err
	}
	nameByUser, err := userNames(ctx, pool, tenantID, userIDs)
	if err != nil {
		return nil, err
	}

	out := make([]taskView, len(tasks))
	for i := range tasks {
		t := &tasks[i]
		v := taskView{Task: t, RoleName: roleByScope[t.ScopeID]}
		if t.AssigneeUserID != nil {
			v.AssigneeName = nameByUser[*t.AssigneeUserID]
		}
		out[i] = v
	}
	return out, nil
}

// enrichEvents resolves author display names for a task's activity log.
func enrichEvents(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, events []TaskEvent) ([]eventView, error) {
	userIDs := make([]uuid.UUID, 0, len(events))
	for i := range events {
		if events[i].AuthorID != nil {
			userIDs = append(userIDs, *events[i].AuthorID)
		}
	}
	nameByUser, err := userNames(ctx, pool, tenantID, userIDs)
	if err != nil {
		return nil, err
	}
	out := make([]eventView, len(events))
	for i := range events {
		e := &events[i]
		v := eventView{TaskEvent: e}
		if e.AuthorID != nil {
			v.AuthorName = nameByUser[*e.AuthorID]
		}
		out[i] = v
	}
	return out, nil
}

func roleNamesByScope(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, scopeIDs []uuid.UUID) (map[uuid.UUID]string, error) {
	out := map[uuid.UUID]string{}
	if len(scopeIDs) == 0 {
		return out, nil
	}
	rows, err := pool.Query(ctx, `
		SELECT s.id, r.name
		FROM scopes s
		LEFT JOIN roles r ON r.id = s.role_id
		WHERE s.tenant_id = $1 AND s.id = ANY($2)`, tenantID, scopeIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var name *string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		if name != nil {
			out[id] = *name
		}
	}
	return out, rows.Err()
}

func userNames(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, userIDs []uuid.UUID) (map[uuid.UUID]string, error) {
	out := map[uuid.UUID]string{}
	if len(userIDs) == 0 {
		return out, nil
	}
	rows, err := pool.Query(ctx, `
		SELECT id, display_name FROM users
		WHERE tenant_id = $1 AND id = ANY($2)`, tenantID, userIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var name *string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		if name != nil {
			out[id] = *name
		}
	}
	return out, rows.Err()
}
