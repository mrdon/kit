package slack

import "encoding/json"

// OuterEvent is the top-level envelope Slack sends to the Events API endpoint.
type OuterEvent struct {
	Token     string          `json:"token"`
	TeamID    string          `json:"team_id"`
	Type      string          `json:"type"`
	Challenge string          `json:"challenge"`
	Event     json.RawMessage `json:"event"`
}

// InnerEvent is the common fields across all Slack event types.
type InnerEvent struct {
	Type    string `json:"type"`
	SubType string `json:"subtype"`
	BotID   string `json:"bot_id"`
}

// MessageEvent represents a message event from Slack.
type MessageEvent struct {
	Type      string `json:"type"`
	SubType   string `json:"subtype"`
	BotID     string `json:"bot_id"`
	User      string `json:"user"`
	Text      string `json:"text"`
	Timestamp string `json:"ts"`
	ThreadTS  string `json:"thread_ts"`
	Channel   string `json:"channel"`
	ChannelID string `json:"channel_id"`
	TeamID    string `json:"team_id"`
	Files     []File `json:"files"`
}

// File represents a Slack file attachment.
type File struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	MimeType string `json:"mimetype"`
	FileType string `json:"filetype"`
	URL      string `json:"url_private_download"`
}

// AppMentionEvent represents an app_mention event from Slack.
type AppMentionEvent struct {
	Type      string `json:"type"`
	User      string `json:"user"`
	Text      string `json:"text"`
	Timestamp string `json:"ts"`
	ThreadTS  string `json:"thread_ts"`
	Channel   string `json:"channel"`
	TeamID    string `json:"team_id"`
	Files     []File `json:"files"`
}

// ShouldProcess returns true if this message event should be processed by the agent.
func (m *MessageEvent) ShouldProcess() bool {
	// Ignore bot messages
	if m.BotID != "" {
		return false
	}
	// Ignore subtypes (message_changed, message_deleted, etc.)
	if m.SubType != "" {
		return false
	}
	return true
}

// ThreadTimestamp returns the thread_ts for replying. If the message is not in
// a thread, it returns the message's own ts to start a new thread.
func (m *MessageEvent) ThreadTimestamp() string {
	if m.ThreadTS != "" {
		return m.ThreadTS
	}
	return m.Timestamp
}

// ThreadTimestamp returns the thread_ts for replying.
func (a *AppMentionEvent) ThreadTimestamp() string {
	if a.ThreadTS != "" {
		return a.ThreadTS
	}
	return a.Timestamp
}
