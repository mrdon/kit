package slack

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/services"
	kitslack "github.com/mrdon/kit/internal/slack"
)

// SlackChannelService handles Slack channel operations with authorization.
type SlackChannelService struct {
	pool *pgxpool.Pool
}

// Configure adds a channel for message search. Admin-only.
func (s *SlackChannelService) Configure(ctx context.Context, c *services.Caller, sc *kitslack.Client, slackChannelID string, roleScopes []string) (*SlackChannel, error) {
	if !c.IsAdmin {
		return nil, services.ErrForbidden
	}

	info, err := sc.GetConversationInfo(ctx, slackChannelID)
	if err != nil {
		return nil, fmt.Errorf("getting channel info: %w", err)
	}
	if !info.IsMember {
		return nil, errors.New("bot is not a member of this channel — invite it first")
	}

	ch, err := createChannel(ctx, s.pool, c.TenantID, slackChannelID, info.Name)
	if err != nil {
		return nil, err
	}

	// Clear existing scopes on reconfigure
	if err := deleteChannelScopes(ctx, s.pool, c.TenantID, ch.ID); err != nil {
		return nil, err
	}

	if len(roleScopes) == 0 {
		if err := addChannelScope(ctx, s.pool, c.TenantID, ch.ID, "tenant", "*"); err != nil {
			return nil, err
		}
	} else {
		for _, role := range roleScopes {
			if err := addChannelScope(ctx, s.pool, c.TenantID, ch.ID, "role", role); err != nil {
				return nil, err
			}
		}
	}

	return ch, nil
}

// Remove deletes a configured channel. Admin-only.
func (s *SlackChannelService) Remove(ctx context.Context, c *services.Caller, slackChannelID string) error {
	if !c.IsAdmin {
		return services.ErrForbidden
	}
	ch, err := getChannelBySlackID(ctx, s.pool, c.TenantID, slackChannelID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return services.ErrNotFound
		}
		return err
	}
	return deleteChannel(ctx, s.pool, c.TenantID, ch.ID)
}

// List returns channels the caller can access.
func (s *SlackChannelService) List(ctx context.Context, c *services.Caller) ([]SlackChannel, error) {
	if c.IsAdmin {
		return listChannelsAll(ctx, s.pool, c.TenantID)
	}
	return listChannelsScoped(ctx, s.pool, c.TenantID, c.Roles)
}

// GetMessagesOpts holds options for GetMessages.
type GetMessagesOpts struct {
	ChannelID string
	Query     string
	After     string // YYYY-MM-DD
	Cursor    string
}

// GetMessagesResult holds the result of GetMessages.
type GetMessagesResult struct {
	Messages   []kitslack.Message
	NextCursor string
	HasMore    bool
}

// GetMessages fetches messages from a configured channel with optional filtering and paging.
func (s *SlackChannelService) GetMessages(ctx context.Context, c *services.Caller, sc *kitslack.Client, opts GetMessagesOpts) (*GetMessagesResult, error) {
	if err := s.checkChannelAccess(ctx, c, opts.ChannelID); err != nil {
		return nil, err
	}

	oldest := time.Now().Add(-24 * time.Hour)
	if opts.After != "" {
		t, err := time.Parse("2006-01-02", opts.After)
		if err != nil {
			return nil, errors.New("invalid after date, use YYYY-MM-DD format")
		}
		oldest = t
	}

	histOpts := kitslack.HistoryOpts{
		Oldest: strconv.FormatInt(oldest.Unix(), 10),
		Cursor: opts.Cursor,
	}

	if opts.Query != "" {
		// Fetch more to account for client-side filtering
		histOpts.Limit = 100
	} else {
		histOpts.Limit = 20
	}

	result, err := sc.GetConversationHistory(ctx, opts.ChannelID, histOpts)
	if err != nil {
		return nil, fmt.Errorf("fetching channel history: %w", err)
	}

	out := &GetMessagesResult{
		NextCursor: result.NextCursor,
		HasMore:    result.HasMore,
	}

	if opts.Query == "" {
		out.Messages = result.Messages
		return out, nil
	}

	query := strings.ToLower(opts.Query)
	for _, m := range result.Messages {
		if strings.Contains(strings.ToLower(m.Text), query) {
			out.Messages = append(out.Messages, m)
			if len(out.Messages) >= 20 {
				break
			}
		}
	}
	return out, nil
}

// checkChannelAccess verifies the caller can access a given Slack channel ID.
func (s *SlackChannelService) checkChannelAccess(ctx context.Context, c *services.Caller, slackChannelID string) error {
	channels, err := s.List(ctx, c)
	if err != nil {
		return fmt.Errorf("checking channel access: %w", err)
	}
	for _, ch := range channels {
		if ch.SlackChannelID == slackChannelID {
			return nil
		}
	}
	return services.ErrForbidden
}
