package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// CapturedMessage records a message that would have been sent in dry-run mode.
type CapturedMessage struct {
	Channel  string `json:"channel"`
	ThreadTS string `json:"thread_ts,omitempty"`
	Text     string `json:"text"`
}

// Client wraps the Slack Web API for posting messages and managing reactions.
type Client struct {
	token      string
	httpClient *http.Client
	dryRun     bool
	Captured   []CapturedMessage
}

// NewClient creates a new Slack API client with the given bot token.
func NewClient(botToken string) *Client {
	return &Client{
		token:      botToken,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// NewDryRunClient creates a client that captures messages instead of sending them.
// Read-only API calls (GetUserInfo, OpenConversation, etc.) still hit Slack.
func NewDryRunClient(botToken string) *Client {
	c := NewClient(botToken)
	c.dryRun = true
	return c
}

// PostMessage sends a message to a Slack channel, optionally in a thread.
func (c *Client) PostMessage(ctx context.Context, channel, threadTS, text string) error {
	if c.dryRun {
		c.Captured = append(c.Captured, CapturedMessage{Channel: channel, ThreadTS: threadTS, Text: text})
		return nil
	}
	payload := map[string]string{
		"channel":   channel,
		"thread_ts": threadTS,
		"text":      text,
	}
	_, err := c.apiCall(ctx, "chat.postMessage", payload)
	return err
}

// PostMessageReturningTS sends a message and returns its timestamp (for later update/delete).
func (c *Client) PostMessageReturningTS(ctx context.Context, channel, threadTS, text string) (string, error) {
	if c.dryRun {
		c.Captured = append(c.Captured, CapturedMessage{Channel: channel, ThreadTS: threadTS, Text: text})
		return "dry-run-ts", nil
	}
	payload := map[string]string{
		"channel":   channel,
		"thread_ts": threadTS,
		"text":      text,
	}
	resp, err := c.apiCall(ctx, "chat.postMessage", payload)
	if err != nil {
		return "", err
	}
	ts, _ := resp["ts"].(string)
	return ts, nil
}

// UpdateMessage updates an existing message.
func (c *Client) UpdateMessage(ctx context.Context, channel, messageTS, text string) error {
	if c.dryRun {
		return nil
	}
	payload := map[string]string{
		"channel": channel,
		"ts":      messageTS,
		"text":    text,
	}
	_, err := c.apiCall(ctx, "chat.update", payload)
	return err
}

// DeleteMessage deletes a message.
func (c *Client) DeleteMessage(ctx context.Context, channel, messageTS string) error {
	if c.dryRun {
		return nil
	}
	payload := map[string]string{
		"channel": channel,
		"ts":      messageTS,
	}
	_, err := c.apiCall(ctx, "chat.delete", payload)
	return err
}

// AddReaction adds an emoji reaction to a message.
func (c *Client) AddReaction(ctx context.Context, channel, timestamp, emoji string) error {
	payload := map[string]string{
		"channel":   channel,
		"timestamp": timestamp,
		"name":      emoji,
	}
	_, err := c.apiCall(ctx, "reactions.add", payload)
	return err
}

// RemoveReaction removes an emoji reaction from a message.
func (c *Client) RemoveReaction(ctx context.Context, channel, timestamp, emoji string) error {
	payload := map[string]string{
		"channel":   channel,
		"timestamp": timestamp,
		"name":      emoji,
	}
	_, err := c.apiCall(ctx, "reactions.remove", payload)
	return err
}

// OpenConversation opens a DM channel with a user. Returns the channel ID.
// Uses form-encoded POST because some Slack endpoints don't parse JSON bodies.
func (c *Client) OpenConversation(ctx context.Context, userID string) (string, error) {
	resp, err := c.apiFormCall(ctx, "conversations.open", map[string]string{"users": userID})
	if err != nil {
		return "", err
	}

	channel, ok := resp["channel"].(map[string]any)
	if !ok {
		return "", errors.New("unexpected response format")
	}
	channelID, ok := channel["id"].(string)
	if !ok {
		return "", errors.New("missing channel id")
	}
	return channelID, nil
}

// UserInfo holds profile fields fetched from Slack.
type UserInfo struct {
	SlackUserID string
	DisplayName string
	Timezone    string
}

// GetUserInfo fetches a single user's display name and timezone from Slack.
// Uses form-encoded POST because users.info doesn't parse JSON bodies.
func (c *Client) GetUserInfo(ctx context.Context, userID string) (*UserInfo, error) {
	resp, err := c.apiFormCall(ctx, "users.info", map[string]string{"user": userID})
	if err != nil {
		return nil, err
	}
	user, ok := resp["user"].(map[string]any)
	if !ok {
		return nil, errors.New("unexpected users.info response format")
	}
	return parseUserInfo(user), nil
}

// ListAllUsers fetches all workspace members in bulk via users.list (paginated).
// Much more efficient than per-user GetUserInfo calls for syncing.
func (c *Client) ListAllUsers(ctx context.Context) ([]UserInfo, error) {
	var allUsers []UserInfo
	cursor := ""

	for {
		params := map[string]string{"limit": "200"}
		if cursor != "" {
			params["cursor"] = cursor
		}
		resp, err := c.apiFormCall(ctx, "users.list", params)
		if err != nil {
			return nil, err
		}

		members, _ := resp["members"].([]any)
		for _, m := range members {
			member, ok := m.(map[string]any)
			if !ok {
				continue
			}
			if isBot, _ := member["is_bot"].(bool); isBot {
				continue
			}
			if deleted, _ := member["deleted"].(bool); deleted {
				continue
			}
			allUsers = append(allUsers, *parseUserInfo(member))
		}

		meta, _ := resp["response_metadata"].(map[string]any)
		cursor, _ = meta["next_cursor"].(string)
		if cursor == "" {
			break
		}
	}

	return allUsers, nil
}

func parseUserInfo(user map[string]any) *UserInfo {
	info := &UserInfo{}
	info.SlackUserID, _ = user["id"].(string)
	info.Timezone, _ = user["tz"].(string)
	if profile, ok := user["profile"].(map[string]any); ok {
		info.DisplayName, _ = profile["display_name"].(string)
		if info.DisplayName == "" {
			info.DisplayName, _ = profile["real_name"].(string)
		}
	}
	return info
}

// GetFileContent downloads a file from Slack using the bot token for auth.
func (c *Client) GetFileContent(ctx context.Context, fileURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("downloading file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("file download returned %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

// ConversationInfo holds basic channel information.
type ConversationInfo struct {
	Name     string
	IsMember bool
}

// GetConversationInfo fetches channel info. Returns name and whether the bot is a member.
func (c *Client) GetConversationInfo(ctx context.Context, channelID string) (*ConversationInfo, error) {
	resp, err := c.apiCall(ctx, "conversations.info", map[string]string{"channel": channelID})
	if err != nil {
		return nil, err
	}
	ch, ok := resp["channel"].(map[string]any)
	if !ok {
		return nil, errors.New("unexpected conversations.info response format")
	}
	info := &ConversationInfo{}
	info.Name, _ = ch["name"].(string)
	info.IsMember, _ = ch["is_member"].(bool)
	return info, nil
}

// HistoryOpts configures a conversation history request.
type HistoryOpts struct {
	Limit  int
	Cursor string
	Oldest string // Unix timestamp string
}

// Message represents a Slack message from channel history.
type Message struct {
	UserID    string
	Text      string
	Timestamp string
	ThreadTS  string
}

// HistoryResult holds the result of a conversation history call.
type HistoryResult struct {
	Messages   []Message
	NextCursor string
	HasMore    bool
}

// GetConversationHistory fetches messages from a channel.
func (c *Client) GetConversationHistory(ctx context.Context, channelID string, opts HistoryOpts) (*HistoryResult, error) {
	payload := map[string]any{"channel": channelID}
	if opts.Limit > 0 {
		payload["limit"] = opts.Limit
	} else {
		payload["limit"] = 20
	}
	if opts.Cursor != "" {
		payload["cursor"] = opts.Cursor
	}
	if opts.Oldest != "" {
		payload["oldest"] = opts.Oldest
	}

	resp, err := c.apiCall(ctx, "conversations.history", payload)
	if err != nil {
		return nil, err
	}

	result := &HistoryResult{}
	result.HasMore, _ = resp["has_more"].(bool)
	if meta, ok := resp["response_metadata"].(map[string]any); ok {
		result.NextCursor, _ = meta["next_cursor"].(string)
	}

	msgs, _ := resp["messages"].([]any)
	for _, m := range msgs {
		msg, ok := m.(map[string]any)
		if !ok {
			continue
		}
		result.Messages = append(result.Messages, Message{
			UserID:    strVal(msg, "user"),
			Text:      strVal(msg, "text"),
			Timestamp: strVal(msg, "ts"),
			ThreadTS:  strVal(msg, "thread_ts"),
		})
	}
	return result, nil
}

// FormatTimestamp converts a Unix timestamp to a readable date-time string.
func FormatTimestamp(unixSec int64) string {
	return time.Unix(unixSec, 0).UTC().Format("2006-01-02 15:04")
}

func strVal(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

// AuthTest calls auth.test and returns the team_id and bot_user_id the token belongs to.
func (c *Client) AuthTest(ctx context.Context) (teamID, botUserID string, err error) {
	resp, err := c.apiCall(ctx, "auth.test", map[string]string{})
	if err != nil {
		return "", "", err
	}
	teamID, _ = resp["team_id"].(string)
	botUserID, _ = resp["user_id"].(string)
	return teamID, botUserID, nil
}

func (c *Client) apiFormCall(ctx context.Context, method string, params map[string]string) (map[string]any, error) {
	form := make(url.Values)
	for k, v := range params {
		form.Set(k, v)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://slack.com/api/"+method, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling %s: %w", method, err)
	}
	defer resp.Body.Close()

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	if ok, _ := result["ok"].(bool); !ok {
		errMsg, _ := result["error"].(string)
		return nil, fmt.Errorf("slack api %s: %s", method, errMsg)
	}

	return result, nil
}

func (c *Client) apiCall(ctx context.Context, method string, payload any) (map[string]any, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshaling payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://slack.com/api/"+method, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling %s: %w", method, err)
	}
	defer resp.Body.Close()

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	if ok, _ := result["ok"].(bool); !ok {
		errMsg, _ := result["error"].(string)
		return nil, fmt.Errorf("slack api %s: %s", method, errMsg)
	}

	return result, nil
}
