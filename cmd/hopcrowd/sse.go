package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strings"
	"time"

	"github.com/mrdon/kit/internal/apps/hoppers"
	"github.com/mrdon/kit/internal/livegame"
)

// conn is one fake device's link to kit: a cookie jar (its identity, exactly
// as a browser keeps it), the SSE stream or the poll fallback, and POSTs.
//
// base is the surface's root: a phone's is the game URL
// (/{slug}/hoppers/{game}), the host's the console API for the game
// (/{slug}/api/hoppers/games/{id}). Both serve /stream and /state.
type conn struct {
	base    string
	client  *http.Client
	headers http.Header
}

func (c *conn) post(ctx context.Context, path string, body, out any) (int, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return 0, fmt.Errorf("encoding %s: %w", path, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, strings.NewReader(string(b)))
	if err != nil {
		return 0, fmt.Errorf("building %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	maps.Copy(req.Header, c.headers)
	res, err := c.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("posting %s: %w", path, err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return res.StatusCode, fmt.Errorf("%s: %d %s", path, res.StatusCode, strings.TrimSpace(string(data)))
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return res.StatusCode, fmt.Errorf("decoding %s: %w", path, err)
		}
	}
	return res.StatusCode, nil
}

// stream reads GET /stream until ctx ends, the connection drops, or the
// server sends a reset (this device's identity changed), handing every
// state frame to fn.
func (c *conn) stream(ctx context.Context, fn func(*hoppers.Frame)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/stream", nil)
	if err != nil {
		return fmt.Errorf("building stream: %w", err)
	}
	maps.Copy(req.Header, c.headers)
	res, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("opening stream: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("stream: %d", res.StatusCode)
	}
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	event, data := "", ""
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			switch event {
			case string(livegame.EventState):
				var f hoppers.Frame
				if err := json.Unmarshal([]byte(data), &f); err == nil {
					fn(&f)
				}
			case string(livegame.EventReset):
				return nil // reconnect as whoever the cookies say now
			}
			event, data = "", ""
		case strings.HasPrefix(line, "event: "):
			event = line[len("event: "):]
		case strings.HasPrefix(line, "data: "):
			data += line[len("data: "):]
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("reading stream: %w", err)
	}
	return nil
}

// poll uses GET /state?since= every interval instead of the stream, the way
// a browser does when SSE is being eaten by a proxy.
func (c *conn) poll(ctx context.Context, every time.Duration, fn func(*hoppers.Frame)) error {
	since := uint64(0)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		u := c.base + "/state"
		if since > 0 {
			u += fmt.Sprintf("?since=%d", since)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return fmt.Errorf("building poll: %w", err)
		}
		maps.Copy(req.Header, c.headers)
		res, err := c.client.Do(req)
		if err != nil {
			return fmt.Errorf("polling: %w", err)
		}
		if res.StatusCode == http.StatusOK {
			var f hoppers.Frame
			if err := json.NewDecoder(res.Body).Decode(&f); err == nil {
				since = f.Version
				fn(&f)
			}
		}
		res.Body.Close()
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}
