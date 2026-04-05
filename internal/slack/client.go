package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client wraps the Slack Web API for posting messages and managing reactions.
type Client struct {
	token      string
	httpClient *http.Client
}

// NewClient creates a new Slack API client with the given bot token.
func NewClient(botToken string) *Client {
	return &Client{
		token:      botToken,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// PostMessage sends a message to a Slack channel, optionally in a thread.
func (c *Client) PostMessage(ctx context.Context, channel, threadTS, text string) error {
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
func (c *Client) OpenConversation(ctx context.Context, userID string) (string, error) {
	payload := map[string]string{
		"users": userID,
	}
	resp, err := c.apiCall(ctx, "conversations.open", payload)
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
