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
does not edit the site directly.

Kit posts the preview URL back to the Slack thread automatically when the
build finishes (~60s). Don't share the URL yourself; just acknowledge the
request briefly and wait. Iteration in a thread is automatic — call this
tool again with a new prompt and Kit chains the new run off the previous
turn's state.`,
		Schema: services.PropsReq(map[string]any{
			"prompt": services.Field("string",
				"The change to make, in plain English. Pass the user's request as-is "+
					"unless it's clearly missing critical context."),
		}, "prompt"),
	},
	{
		Name: "netlify_publish_change",
		Description: `Publish the latest preview to the live site. Call this when the user clearly
indicates they want the changes from this Slack thread to go live — "publish",
"ship it", "make it live", "looks good, deploy", "send it", etc.

Refuses if the thread has no agent runs yet, or if the latest run is still
building. Once it succeeds, Netlify commits the changes to the production
branch and the live site updates in ~90 seconds.`,
		Schema: services.Props(map[string]any{}),
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
	switch name {
	case "netlify_request_change":
		return requestChangeHandler(svc)
	case "netlify_publish_change":
		return publishChangeHandler(svc)
	}
	return nil
}

func publishChangeHandler(svc *Service) tools.HandlerFunc {
	return func(ec *tools.ExecContext, _ json.RawMessage) (string, error) {
		caller := ec.Caller()
		if caller == nil {
			return "", errors.New("no caller in context")
		}
		if ec.Channel == "" || ec.ThreadTS == "" {
			return "Publishing requires running this from inside a Slack thread that has at least one Netlify run.", nil
		}
		res, err := svc.PublishChange(ec.Ctx, caller.TenantID, ec.Channel, ec.ThreadTS)
		if err != nil {
			return formatPublishError(ec, err), nil
		}
		var b strings.Builder
		b.WriteString(":rocket: Published.\n")
		if res.PRTitle != "" {
			fmt.Fprintf(&b, "_%q_\n", res.PRTitle)
		} else if res.Summary != "" {
			fmt.Fprintf(&b, "_%s_\n", res.Summary)
		}
		if res.ChangedFiles > 0 || res.Additions > 0 || res.Deletions > 0 {
			fmt.Fprintf(&b, "- %d file(s) changed (+%d / -%d)\n",
				res.ChangedFiles, res.Additions, res.Deletions)
		}
		if res.PRURL != "" {
			fmt.Fprintf(&b, "- PR: %s\n", res.PRURL)
		}
		b.WriteString("\nNetlify is rebuilding now; the live site updates in about a minute and a half. " +
			"Tell the user it's publishing — include the PR link verbatim if present.")
		return b.String(), nil
	}
}

// formatPublishError converts typed sentinels into copy the LLM can
// relay verbatim. Unknown errors fall through with the raw message.
func formatPublishError(ec *tools.ExecContext, err error) string {
	slug := ""
	if ec.Tenant != nil {
		slug = ec.Tenant.Slug
	}
	switch {
	case errors.Is(err, ErrNothingToPublish):
		return "There's no preview to publish in this thread yet. Make a change first by asking for one (e.g. \"make the H1 blue\"), then publish it once the preview looks right."
	case errors.Is(err, ErrPublishPending):
		return "The latest build is still running — wait for the preview-ready message and try publishing again after."
	case errors.Is(err, ErrNetlifyNotConnected):
		page := "Kit's integrations page"
		if slug != "" {
			page = "Kit's integrations page (/" + slug + "/admin/integrations)"
		}
		return "Netlify isn't connected for this workspace. Connect it at " + page + " before publishing."
	case errors.Is(err, ErrNetlifyCodingInstallMissing):
		return "Netlify can't push to GitHub because its **Coding** GitHub App isn't authorized on " +
			"this Netlify team. (This is separate from Kit's GitHub App and from Netlify's normal " +
			"site→repo connection.) In the Netlify dashboard go to **Team settings → Connected services " +
			"→ GitHub → Manage permissions** and grant the Coding app write access, then try publishing " +
			"again."
	}
	return "Couldn't publish the change: " + err.Error()
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
