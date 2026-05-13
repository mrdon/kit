package netlify

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
)

// ErrAgentRunnersNotAvailable is returned when Netlify's 403 mentions
// the plan-gate copy. Surfaced as typed so the tool handler can
// render upgrade-your-plan guidance instead of leaking raw API text.
var ErrAgentRunnersNotAvailable = errors.New("agent runners not available on this netlify account plan")

// AgentRunner mirrors the public Netlify API agentRunner object. We
// project only the fields we use today; the underlying object has
// more (PR metadata, deploy ids, etc.) we'll add when later steps
// need them.
//
// Source schema: https://github.com/netlify/open-api swagger.yml
// (definitions.agentRunner).
type AgentRunner struct {
	ID                     string `json:"id"`
	SiteID                 string `json:"site_id"`
	State                  string `json:"state"`         // pending | running | succeeded | failed | cancelled
	Branch                 string `json:"branch"`        // base branch we forked off
	ResultBranch           string `json:"result_branch"` // what the agent committed to
	Title                  string `json:"title"`
	CurrentTask            string `json:"current_task"`
	ResultDiff             string `json:"result_diff"`
	LatestSessionDeployURL string `json:"latest_session_deploy_url"` // preview URL
	PRURL                  string `json:"pr_url"`
	PRNumber               int    `json:"pr_number"`
	CreatedAt              string `json:"created_at"`
	UpdatedAt              string `json:"updated_at"`
	DoneAt                 string `json:"done_at"`
}

// CreateAgentRunnerInput is the v1 subset of fields we actually pass.
type CreateAgentRunnerInput struct {
	SiteID              string // required
	Prompt              string
	Branch              string // base branch to fork off; empty = default
	Agent               string // "claude" | "codex" | "gemini"; empty = Netlify's default
	Model               string // model id; empty = agent's default
	ParentAgentRunnerID string // chain off a prior run's id (Netlify's "follow-up" mechanism); empty for fresh run
}

// createAgentRunner kicks off an agent run. Returns the new
// AgentRunner including its id and (initially-empty) preview URL.
//
// Implementation note: Netlify's OpenAPI swagger documents these
// fields as query params, but the live API expects them in the JSON
// body — empty body returns "unexpected end of input" (500), and
// query-only returns "no prompt provided" (400). We send everything
// in the body and ignore the query-param documentation.
func createAgentRunner(ctx context.Context, accessToken string, in CreateAgentRunnerInput) (*AgentRunner, error) {
	if in.SiteID == "" {
		return nil, errors.New("site_id is required")
	}
	bodyMap := map[string]any{"site_id": in.SiteID}
	if in.Prompt != "" {
		bodyMap["prompt"] = in.Prompt
	}
	if in.Branch != "" {
		bodyMap["branch"] = in.Branch
	}
	if in.Agent != "" {
		bodyMap["agent"] = in.Agent
	}
	if in.Model != "" {
		bodyMap["model"] = in.Model
	}
	if in.ParentAgentRunnerID != "" {
		bodyMap["parent_agent_runner_id"] = in.ParentAgentRunnerID
	}
	bodyBytes, err := json.Marshal(bodyMap)
	if err != nil {
		return nil, fmt.Errorf("encoding agent_runners body: %w", err)
	}
	endpoint := netlifyAPIBase + "/agent_runners"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint,
		bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("building agent_runners request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("posting agent_runners: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading agent_runners response: %w", err)
	}
	if resp.StatusCode >= 400 {
		// 403s with the plan-gate message become a typed sentinel
		// so the tool handler can render upgrade guidance.
		if resp.StatusCode == http.StatusForbidden &&
			strings.Contains(string(body), "Agent runners are not available") {
			return nil, ErrAgentRunnersNotAvailable
		}
		return nil, fmt.Errorf("netlify agent_runners failed (status %d): %s",
			resp.StatusCode, string(body))
	}
	var out AgentRunner
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decoding agent_runners response: %w", err)
	}
	return &out, nil
}

// getAgentRunner fetches an existing agent run by id. Used for
// polling status + reading result_diff once the run completes.
func getAgentRunner(ctx context.Context, accessToken, id string) (*AgentRunner, error) {
	endpoint := netlifyAPIBase + "/agent_runners/" + url.PathEscape(id)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("building get agent_runner request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching agent_runner: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("netlify get agent_runner failed (status %d): %s",
			resp.StatusCode, string(body))
	}
	var out AgentRunner
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decoding agent_runner response: %w", err)
	}
	return &out, nil
}
