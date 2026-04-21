package tools

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// slackSendOnce is the idempotency boundary for post_to_channel / dm_user
// when gated via require_approval. Mirrors the email sendOnce pattern: it
// stamps a claim row BEFORE calling Slack so a stuck-resolving retry
// finds the prior slack_ts and skips re-posting. On post error the claim
// row is removed so a re-approve can try again cleanly.
//
// post is the side-effecting caller — invoked only when this resolveTok
// has no prior successful post recorded. Return the Slack message ts on
// success; errors are surfaced verbatim and cause the claim to be
// released.
func slackSendOnce(
	ctx context.Context,
	pool *pgxpool.Pool,
	resolveTok, tenantID, userID uuid.UUID,
	toolName, channelID string,
	post func(ctx context.Context) (slackTS string, err error),
) (string, error) {
	if resolveTok == uuid.Nil {
		return "", errors.New("slack send requires an approval token")
	}

	var existingTS string
	err := pool.QueryRow(ctx, `
		INSERT INTO app_slack_sent_messages
		  (resolve_token, tenant_id, user_id, tool_name, channel_id, slack_ts)
		VALUES ($1, $2, $3, $4, $5, '')
		ON CONFLICT (resolve_token) DO UPDATE
		  SET resolve_token = EXCLUDED.resolve_token
		RETURNING slack_ts`,
		resolveTok, tenantID, userID, toolName, channelID,
	).Scan(&existingTS)
	if err != nil {
		return "", fmt.Errorf("claiming slack send idempotency row: %w", err)
	}
	if existingTS != "" {
		return existingTS, nil
	}

	ts, sendErr := post(ctx)
	if sendErr != nil {
		_, _ = pool.Exec(ctx, `
			DELETE FROM app_slack_sent_messages
			WHERE resolve_token = $1 AND tenant_id = $2 AND slack_ts = ''`,
			resolveTok, tenantID)
		return "", sendErr
	}
	if ts == "" {
		// Slack returned ok but no ts (shouldn't normally happen). Mark
		// the row as claimed with a sentinel so we still dedupe on retry.
		ts = "sent"
	}
	if _, err := pool.Exec(ctx, `
		UPDATE app_slack_sent_messages
		SET slack_ts = $1
		WHERE resolve_token = $2 AND tenant_id = $3`,
		ts, resolveTok, tenantID,
	); err != nil {
		return ts, fmt.Errorf("recording slack send ts: %w", err)
	}
	return ts, nil
}
