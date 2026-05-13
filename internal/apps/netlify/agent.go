package netlify

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/tools"
)

// netlifyTools is the shared metadata for the agent + MCP surfaces.
// Keep prose terse — the system prompt fragment in
// prompts/system_netlify.tmpl carries the "when to use" guidance.
var netlifyTools = []services.ToolMeta{
	{
		Name: "netlify_request_change",
		Description: `Request a change to the team's marketing website. Use this when the user asks
for a tweak to the public site (e.g. "make the banner blue", "update our hours",
"add this paragraph to the about page"). The change runs in Netlify's cloud — Kit
does not edit the site directly. Returns a preview URL the user can click to see
the result (404 for the first ~60 seconds while the build runs).

In v1 this is single-shot: each call starts a fresh run off the production
branch and the user gets ONE preview URL back. Don't call this in a loop;
respond to the user with the URL and wait for their reply.`,
		Schema: services.PropsReq(map[string]any{
			"prompt": services.Field("string",
				"The change to make, in plain English. Pass the user's request as-is "+
					"unless it's clearly missing critical context."),
		}, "prompt"),
	},
}

func registerNetlifyAgentTools(r *tools.Registry, svc *Service) {
	for _, meta := range netlifyTools {
		r.Register(tools.Def{
			Name:          meta.Name,
			Description:   meta.Description,
			Schema:        meta.Schema,
			DefaultPolicy: tools.PolicyAllow,
			Handler:       agentHandlerFor(meta.Name, svc),
		})
	}
}

func agentHandlerFor(name string, svc *Service) tools.HandlerFunc {
	if name == "netlify_request_change" {
		return requestChangeHandler(svc)
	}
	return nil
}

func requestChangeHandler(svc *Service) tools.HandlerFunc {
	return func(ec *tools.ExecContext, raw json.RawMessage) (string, error) {
		var inp struct {
			Prompt string `json:"prompt"`
		}
		if err := json.Unmarshal(raw, &inp); err != nil {
			return "", fmt.Errorf("parsing args: %w", err)
		}
		if strings.TrimSpace(inp.Prompt) == "" {
			return "", errors.New("prompt is required")
		}
		caller := ec.Caller()
		if caller == nil {
			return "", errors.New("no caller in context")
		}
		res, err := svc.RequestChange(ec.Ctx, caller.TenantID, ChangeRequest{
			Prompt:        inp.Prompt,
			SlackChannel:  ec.Channel,
			SlackThreadTS: ec.ThreadTS,
		})
		if err != nil {
			return formatRequestChangeError(ec, err), nil
		}
		return formatRequestChangeOK(res), nil
	}
}

// formatRequestChangeError turns the typed sentinels into agent-
// friendly copy that points the user at the integrations page.
// Returns a string (not an error) so the LLM relays it verbatim
// instead of throwing a generic "tool failed" line.
func formatRequestChangeError(ec *tools.ExecContext, err error) string {
	slug := ""
	if ec.Tenant != nil {
		slug = ec.Tenant.Slug
	}
	settingsURL := "Kit's integrations page"
	if slug != "" {
		settingsURL = fmt.Sprintf("Kit's integrations page (/%s/admin/integrations)", slug)
	}
	switch {
	case errors.Is(err, ErrNetlifyNotConnected):
		return "Netlify isn't connected for this workspace yet. An admin needs to connect it at " + settingsURL + " before I can make website changes."
	case errors.Is(err, ErrGitHubNotConnected):
		return "GitHub isn't connected for this workspace yet. An admin needs to install the Kit GitHub App at " + settingsURL + " before I can make website changes."
	case errors.Is(err, ErrAgentRunnersNotAvailable):
		return "The connected Netlify account's plan doesn't include Agent Runners. " +
			"Migrate the team to Netlify's Credit-based plan (the free tier is enough to test) " +
			"and try again. Legacy 'Starter' accounts don't have Agent Runners — that's the most " +
			"common cause of this error."
	}
	// Unknown error: surface the message so the LLM can relay /
	// debug. Wrap with a hint that it might be transient.
	return "Couldn't kick off a Netlify agent run: " + err.Error()
}

// formatRequestChangeOK is the success-case string. Tells the LLM
// what to say to the user — deliberately brief. Chaining is
// automatic and handled inside Kit; the LLM never needs to know
// or mention it. When a watcher is running (the common Slack
// case), the watcher posts the preview URL when the build is
// ready; the LLM just gives a brief acknowledgement.
func formatRequestChangeOK(res *ChangeRunResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Started a Netlify agent run (id=%s).\n", res.RunID)
	if res.PreviewURL != "" {
		fmt.Fprintf(&b, "- preview URL: %s\n", res.PreviewURL)
	}
	if res.WatcherStarted {
		b.WriteString("\nA background watcher is polling Netlify. When the build is ready " +
			"(~60 seconds), Kit will post the preview URL into this Slack thread directly. ")
		b.WriteString("Reply to the user with a brief acknowledgement (e.g. \"On it — I'll post the preview here when it's ready.\"). ")
		b.WriteString("DO NOT call netlify_request_change again in this turn — the watcher handles delivery.")
	} else {
		b.WriteString("\nNo Slack thread bound to this call (probably an MCP / scripted invocation). " +
			"Share the preview URL with the user; the page may 404 for the first ~60 seconds.")
	}
	return b.String()
}
