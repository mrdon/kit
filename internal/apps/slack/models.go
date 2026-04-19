package slack

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
)

// SlackChannel represents a configured Slack channel for message search.
type SlackChannel struct {
	ID             uuid.UUID `json:"id"`
	TenantID       uuid.UUID `json:"tenant_id"`
	SlackChannelID string    `json:"slack_channel_id"`
	ChannelName    string    `json:"channel_name"`
	CreatedAt      time.Time `json:"created_at"`
}

func createChannel(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, slackChannelID, name string) (*SlackChannel, error) {
	ch := &SlackChannel{TenantID: tenantID, SlackChannelID: slackChannelID, ChannelName: name}
	err := pool.QueryRow(ctx, `
		INSERT INTO app_slack_channels (tenant_id, slack_channel_id, channel_name)
		VALUES ($1, $2, $3)
		ON CONFLICT (tenant_id, slack_channel_id) DO UPDATE SET channel_name = EXCLUDED.channel_name
		RETURNING id, created_at`,
		tenantID, slackChannelID, name,
	).Scan(&ch.ID, &ch.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("creating slack channel: %w", err)
	}
	return ch, nil
}

func deleteChannel(ctx context.Context, pool *pgxpool.Pool, tenantID, channelID uuid.UUID) error {
	_, err := pool.Exec(ctx, `
		DELETE FROM app_slack_channels WHERE tenant_id = $1 AND id = $2`,
		tenantID, channelID,
	)
	if err != nil {
		return fmt.Errorf("deleting slack channel: %w", err)
	}
	return nil
}

func addChannelScope(ctx context.Context, pool *pgxpool.Pool, tenantID, channelID uuid.UUID, roleID, userID *uuid.UUID) error {
	scopeID, err := models.GetOrCreateScope(ctx, pool, tenantID, roleID, userID)
	if err != nil {
		return fmt.Errorf("get-or-create scope: %w", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO app_slack_channel_scopes (tenant_id, channel_id, scope_id)
		VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING`,
		tenantID, channelID, scopeID,
	)
	if err != nil {
		return fmt.Errorf("adding channel scope: %w", err)
	}
	return nil
}

func deleteChannelScopes(ctx context.Context, pool *pgxpool.Pool, tenantID, channelID uuid.UUID) error {
	_, err := pool.Exec(ctx, `
		DELETE FROM app_slack_channel_scopes WHERE tenant_id = $1 AND channel_id = $2`,
		tenantID, channelID,
	)
	if err != nil {
		return fmt.Errorf("deleting channel scopes: %w", err)
	}
	return nil
}

func listChannelsAll(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) ([]SlackChannel, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, tenant_id, slack_channel_id, channel_name, created_at
		FROM app_slack_channels
		WHERE tenant_id = $1
		ORDER BY channel_name`,
		tenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing slack channels: %w", err)
	}
	defer rows.Close()
	return scanChannels(rows)
}

func listChannelsScoped(ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID, roleIDs []uuid.UUID) ([]SlackChannel, error) {
	scopeSQL, scopeArgs := models.ScopeFilterIDs("sc", 2, userID, roleIDs)
	args := append([]any{tenantID}, scopeArgs...)
	rows, err := pool.Query(ctx, `
		SELECT DISTINCT c.id, c.tenant_id, c.slack_channel_id, c.channel_name, c.created_at
		FROM app_slack_channels c
		JOIN app_slack_channel_scopes cs ON cs.channel_id = c.id AND cs.tenant_id = c.tenant_id
		JOIN scopes sc ON sc.id = cs.scope_id
		WHERE c.tenant_id = $1
		AND (`+scopeSQL+`)
		ORDER BY c.channel_name`,
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("listing scoped slack channels: %w", err)
	}
	defer rows.Close()
	return scanChannels(rows)
}

func getChannelBySlackID(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, slackChannelID string) (*SlackChannel, error) {
	var ch SlackChannel
	err := pool.QueryRow(ctx, `
		SELECT id, tenant_id, slack_channel_id, channel_name, created_at
		FROM app_slack_channels
		WHERE tenant_id = $1 AND slack_channel_id = $2`,
		tenantID, slackChannelID,
	).Scan(&ch.ID, &ch.TenantID, &ch.SlackChannelID, &ch.ChannelName, &ch.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("getting slack channel: %w", err)
	}
	return &ch, nil
}

func scanChannels(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]SlackChannel, error) {
	var channels []SlackChannel
	for rows.Next() {
		var ch SlackChannel
		if err := rows.Scan(&ch.ID, &ch.TenantID, &ch.SlackChannelID, &ch.ChannelName, &ch.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning slack channel: %w", err)
		}
		channels = append(channels, ch)
	}
	return channels, rows.Err()
}
