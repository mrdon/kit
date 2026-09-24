package trivia

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// FeedbackChannel is a workspace's choice of where ratings are posted. An
// empty ID means nowhere, which is where every workspace starts. Name is for
// display.
type FeedbackChannel struct {
	ID   string
	Name string
}

// Off reports whether ratings go nowhere.
func (c FeedbackChannel) Off() bool { return c.ID == "" }

// GetFeedbackChannel reads the workspace's setting; one that never saved any
// posts nowhere.
func GetFeedbackChannel(ctx context.Context, q Querier, tenantID uuid.UUID) (FeedbackChannel, error) {
	var c FeedbackChannel
	err := q.QueryRow(ctx, `
		SELECT feedback_channel_id, feedback_channel_name
		  FROM app_trivia_settings WHERE tenant_id = $1`, tenantID).Scan(&c.ID, &c.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return FeedbackChannel{}, nil
	}
	if err != nil {
		return c, fmt.Errorf("loading trivia feedback channel: %w", err)
	}
	return c, nil
}

// SaveFeedbackChannel stores the workspace's choice.
func SaveFeedbackChannel(ctx context.Context, q Querier, tenantID uuid.UUID, c FeedbackChannel) error {
	var n int
	err := q.QueryRow(ctx, `
		INSERT INTO app_trivia_settings (tenant_id, feedback_channel_id, feedback_channel_name)
		VALUES ($1, $2, $3)
		ON CONFLICT (tenant_id) DO UPDATE
		   SET feedback_channel_id = EXCLUDED.feedback_channel_id,
		       feedback_channel_name = EXCLUDED.feedback_channel_name,
		       updated_at = now()
		RETURNING 1`, tenantID, c.ID, c.Name).Scan(&n)
	if err != nil {
		return fmt.Errorf("saving trivia feedback channel: %w", err)
	}
	return nil
}
