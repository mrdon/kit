package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"strings"

	"github.com/mrdon/kit/internal/auth"
)

// hostConn finds the game behind a phone URL in the console's host API and
// returns a connection that acts as the signed-in host: the session cookie
// on every request, and the CSRF header the console's writes require.
func hostConn(ctx context.Context, game, session string) (*conn, error) {
	if session == "" {
		return nil, errors.New("-host needs -session (a signed-in kit_session cookie value)")
	}
	u, err := url.Parse(game)
	if err != nil {
		return nil, fmt.Errorf("parsing -game: %w", err)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/") // {slug}, hoppers, {game}
	if len(parts) != 3 {
		return nil, fmt.Errorf("-game %q is not https://host/{slug}/hoppers/{game}", game)
	}
	api := u.Scheme + "://" + u.Host + "/" + parts[0] + "/api/hoppers"
	headers := http.Header{
		"Cookie":        {auth.SessionCookieName + "=" + session},
		auth.CSRFHeader: {"1"},
	}
	c := &conn{base: api, client: http.DefaultClient, headers: headers}
	id, err := c.gameID(ctx, parts[2])
	if err != nil {
		return nil, err
	}
	c.base = api + "/games/" + id
	return c, nil
}

func (c *conn) gameID(ctx context.Context, name string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/games", nil)
	if err != nil {
		return "", fmt.Errorf("building game list: %w", err)
	}
	maps.Copy(req.Header, c.headers)
	res, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("listing games: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("listing games: %d (is -session signed in to this workspace?)", res.StatusCode)
	}
	var out struct {
		Games []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"games"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decoding game list: %w", err)
	}
	for _, g := range out.Games {
		if g.Name == name {
			return g.ID, nil
		}
	}
	return "", fmt.Errorf("no game %q among the workspace's recent games", name)
}
