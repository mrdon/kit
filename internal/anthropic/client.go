package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const apiURL = "https://api.anthropic.com/v1/messages"

// APIError is returned by CreateMessage when the API responds with a
// non-2xx status. Callers use errors.As to branch on StatusCode —
// most notably 429 (rate limit) which usually means the conversation
// has grown past the per-minute input-token budget and should be
// reset rather than retried.
type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("API returned %d: %s", e.StatusCode, e.Body)
}

// IsRateLimit reports whether err is a 429 from the Anthropic API.
func IsRateLimit(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusTooManyRequests
}

// Client calls the Claude Messages API.
type Client struct {
	apiKey     string
	httpClient *http.Client
}

// NewClient creates a new Anthropic API client.
func NewClient(apiKey string) *Client {
	return &Client{
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 120 * time.Second},
	}
}

// CacheControl marks a content block for prompt caching.
type CacheControl struct {
	Type string `json:"type"`
}

// Message represents a conversation message.
type Message struct {
	Role    string    `json:"role"`
	Content []Content `json:"content"`
}

// Content represents a content block within a message.
//
// The inner Content field is `any` because different block types put
// different shapes there: tool_result uses a string (tool output),
// tool_search_tool_result uses an array (the tool_reference blocks the
// search returned). Using `any` lets responses round-trip through
// session_events JSON storage without losing data.
type Content struct {
	Type  string `json:"type"`
	Text  string `json:"text,omitempty"`
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Input any    `json:"input,omitempty"`

	// For tool_result and tool_search_tool_result blocks
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   any    `json:"content,omitempty"`

	// For prompt caching
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// SystemBlock is a content block in the system prompt array.
type SystemBlock struct {
	Type         string        `json:"type"`
	Text         string        `json:"text"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// Tool describes a tool the model can call.
//
// Custom tools leave Type empty and supply Name + Description + InputSchema.
// Server tools (e.g. tool_search_tool_regex_20251119) set Type to the
// server-tool identifier, supply Name, and omit Description/InputSchema.
//
// DeferLoading: if true, the tool's schema is not sent in the request
// prefix; the model must discover it via the tool_search_tool. At least
// one tool in the request must be non-deferred or the API returns 400.
//
// CacheControl: when set on a tool entry, Anthropic caches the prefix up
// to and including that entry. We typically mark the last non-deferred
// tool to cache the always-loaded tool block.
type Tool struct {
	Type         string        `json:"type,omitempty"`
	Name         string        `json:"name"`
	Description  string        `json:"description,omitempty"`
	InputSchema  any           `json:"input_schema,omitempty"`
	DeferLoading bool          `json:"defer_loading,omitempty"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// ToolSearchRegex returns the server-side regex tool-search tool. The
// model invokes this to discover tools that were sent with
// DeferLoading=true; the API expands matching tools inline as
// tool_reference blocks. Supported on Haiku 4.5+, no beta header.
func ToolSearchRegex() Tool {
	return Tool{
		Type: "tool_search_tool_regex_20251119",
		Name: "tool_search_tool_regex",
	}
}

// Request is the Messages API request body.
type Request struct {
	Model        string        `json:"model"`
	MaxTokens    int           `json:"max_tokens"`
	System       []SystemBlock `json:"system,omitempty"`
	Messages     []Message     `json:"messages"`
	Tools        []Tool        `json:"tools,omitempty"`
	CacheControl *CacheControl `json:"cache_control,omitempty"` // automatic caching
}

// Response is the Messages API response body.
type Response struct {
	ID         string    `json:"id"`
	Type       string    `json:"type"`
	Role       string    `json:"role"`
	Content    []Content `json:"content"`
	Model      string    `json:"model"`
	StopReason string    `json:"stop_reason"`
	Usage      Usage     `json:"usage"`
}

// Usage contains token usage information.
type Usage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

// ToolUses extracts all tool_use blocks from the response.
func (r *Response) ToolUses() []Content {
	var uses []Content
	for _, c := range r.Content {
		if c.Type == "tool_use" {
			uses = append(uses, c)
		}
	}
	return uses
}

// TextContent extracts the concatenated text from all text blocks.
func (r *Response) TextContent() string {
	var b strings.Builder
	for _, c := range r.Content {
		if c.Type == "text" {
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

// Ephemeral returns a cache control marker for prompt caching.
func Ephemeral() *CacheControl {
	return &CacheControl{Type: "ephemeral"}
}

// CreateMessage sends a request to the Claude Messages API.
func (c *Client) CreateMessage(ctx context.Context, req *Request) (*Response, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("calling API: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, &APIError{StatusCode: resp.StatusCode, Body: string(respBody)}
	}

	var result Response
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	return &result, nil
}
