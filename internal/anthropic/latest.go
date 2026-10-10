package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"time"
)

// modelInfo is one row of GET /v1/models.
type modelInfo struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}

// Current naming puts the family first (claude-sonnet-5-5); the Claude 3
// generation put it after the version (claude-3-5-sonnet-20241022). Both
// are matched so the newest wins by date, not by naming scheme.
var familyPatterns = map[string]*regexp.Regexp{
	"opus":   regexp.MustCompile(`^claude-(opus-\d|\d-\d-opus|\d-opus)`),
	"sonnet": regexp.MustCompile(`^claude-(sonnet-\d|\d-\d-sonnet|\d-sonnet)`),
	"haiku":  regexp.MustCompile(`^claude-(haiku-\d|\d-\d-haiku|\d-haiku)`),
}

const (
	resolveRetry   = 10 * time.Minute
	resolveRecheck = 24 * time.Hour
)

// ResolveLatestInBackground keeps the model IDs on the newest release per
// family without ever holding up the caller. Startup proceeds on the
// compiled-in defaults; this retries every ten minutes until the Models
// API answers once, then re-checks daily, and stops when ctx ends.
//
// A goroutine rather than a scheduled task because the IDs are process
// state, not tenant work: there is nothing per tenant to run or audit.
func (c *Client) ResolveLatestInBackground(ctx context.Context) {
	go func() {
		for {
			wait := resolveRecheck
			if err := c.ResolveLatest(ctx); err != nil {
				slog.Warn("resolving latest Claude models; keeping current IDs", "error", err,
					"opus", ModelOpus(), "sonnet", ModelSonnet(), "haiku", ModelHaiku())
				wait = resolveRetry
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
		}
	}()
}

// ResolveLatest does one pass: list the models and move each tier to the
// newest in its family. A family the API does not list keeps its ID.
func (c *Client) ResolveLatest(ctx context.Context) error {
	models, err := c.listModels(ctx)
	if err != nil {
		return err
	}
	latest := pickLatest(models)
	cur := current.Load()
	next := modelSet{opus: pick(latest, "opus", cur.opus), sonnet: pick(latest, "sonnet", cur.sonnet), haiku: pick(latest, "haiku", cur.haiku)}
	if next != *cur {
		slog.Info("claude models moved to latest", "opus", next.opus, "sonnet", next.sonnet, "haiku", next.haiku)
	}
	current.Store(&next)
	return nil
}

func pick(latest map[string]string, family, fallback string) string {
	if id := latest[family]; id != "" {
		return id
	}
	return fallback
}

// pickLatest chooses the newest id per family. Ties on date go to the
// lexically later id, which orders dated snapshots of one release.
func pickLatest(models []modelInfo) map[string]string {
	best := map[string]modelInfo{}
	for _, m := range models {
		for family, re := range familyPatterns {
			if !re.MatchString(m.ID) {
				continue
			}
			cur, ok := best[family]
			if !ok || m.CreatedAt.After(cur.CreatedAt) || (m.CreatedAt.Equal(cur.CreatedAt) && m.ID > cur.ID) {
				best[family] = m
			}
		}
	}
	out := make(map[string]string, len(best))
	for family, m := range best {
		out[family] = m.ID
	}
	return out
}

func (c *Client) listModels(ctx context.Context) ([]modelInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/models?limit=1000", nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("listing models: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("reading models: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &APIError{StatusCode: resp.StatusCode, Body: string(body)}
	}
	var page struct {
		Data []modelInfo `json:"data"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, fmt.Errorf("decoding models: %w", err)
	}
	return page.Data, nil
}
