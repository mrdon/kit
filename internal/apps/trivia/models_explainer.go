package trivia

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// GetExplainerLink reads the workspace's lobby video link; "" when it has
// none, which is every workspace until an admin pastes one.
func GetExplainerLink(ctx context.Context, q Querier, tenantID uuid.UUID) (string, error) {
	var link string
	err := q.QueryRow(ctx, `
		SELECT explainer_video_link
		  FROM app_trivia_settings WHERE tenant_id = $1`, tenantID).Scan(&link)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("loading trivia explainer link: %w", err)
	}
	return link, nil
}

// SaveExplainerLink stores the link, or clears it with "". It shares a row
// with the feedback channel, so it only ever touches its own column.
func SaveExplainerLink(ctx context.Context, q Querier, tenantID uuid.UUID, link string) error {
	var n int
	err := q.QueryRow(ctx, `
		INSERT INTO app_trivia_settings (tenant_id, explainer_video_link)
		VALUES ($1, $2)
		ON CONFLICT (tenant_id) DO UPDATE
		   SET explainer_video_link = EXCLUDED.explainer_video_link,
		       updated_at = now()
		RETURNING 1`, tenantID, link).Scan(&n)
	if err != nil {
		return fmt.Errorf("saving trivia explainer link: %w", err)
	}
	return nil
}
