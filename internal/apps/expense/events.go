package expense

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func appendEvent(ctx context.Context, pool *pgxpool.Pool, tenantID, reportID uuid.UUID, authorID *uuid.UUID, eventType, content, oldValue, newValue string) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO app_expense_report_events (tenant_id, report_id, author_id, event_type, content, old_value, new_value)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		tenantID, reportID, authorID, eventType,
		nilIfEmpty(content), nilIfEmpty(oldValue), nilIfEmpty(newValue),
	)
	if err != nil {
		return fmt.Errorf("appending report event: %w", err)
	}
	return nil
}

func getRecentEvents(ctx context.Context, pool *pgxpool.Pool, tenantID, reportID uuid.UUID, limit int) ([]ReportEvent, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, tenant_id, report_id, author_id, event_type, content, old_value, new_value, created_at
		FROM app_expense_report_events
		WHERE tenant_id = $1 AND report_id = $2
		ORDER BY created_at DESC
		LIMIT $3`,
		tenantID, reportID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("getting report events: %w", err)
	}
	defer rows.Close()

	var events []ReportEvent
	for rows.Next() {
		var e ReportEvent
		var content, oldValue, newValue *string
		if err := rows.Scan(&e.ID, &e.TenantID, &e.ReportID, &e.AuthorID, &e.EventType, &content, &oldValue, &newValue, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning report event: %w", err)
		}
		if content != nil {
			e.Content = *content
		}
		if oldValue != nil {
			e.OldValue = *oldValue
		}
		if newValue != nil {
			e.NewValue = *newValue
		}
		events = append(events, e)
	}
	return events, rows.Err()
}
