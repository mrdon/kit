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
		res, err := svc.RequestChange(ec.Ctx, caller.TenantID, ChangeRequest{Prompt: inp.Prompt})
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
	}
	// Unknown error: surface the message so the LLM can relay /
	// debug. Wrap with a hint that it might be transient.
	return "Couldn't kick off a Netlify agent run: " + err.Error()
}

// formatRequestChangeOK is the success-case string. Tells the LLM
// what to say to the user — explicit instructions because the
// behaviour (preview URL 404s for ~60s) isn't obvious from the
// raw fields alone.
func formatRequestChangeOK(res *ChangeRunResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Started a Netlify agent run.\n")
	fmt.Fprintf(&b, "- run_id: %s\n", res.RunID)
	fmt.Fprintf(&b, "- base branch: %s\n", res.BaseBranch)
	if res.PreviewURL != "" {
		fmt.Fprintf(&b, "- preview URL: %s\n", res.PreviewURL)
	} else {
		fmt.Fprintf(&b, "- preview URL: (still provisioning — Netlify will populate this in a few seconds)\n")
	}
	b.WriteString("\nWhen reporting to the user:\n")
	b.WriteString("- Share the preview URL.\n")
	b.WriteString("- Tell them the page may 404 for the first ~60 seconds while the build runs.\n")
	b.WriteString("- Tell them to reply in this thread if they want to iterate (v1 doesn't chain yet — each reply starts a fresh run for now).\n")
	return b.String()
}
