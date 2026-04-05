package web

import (
	"html/template"
	"net/http"
)

var landingTmpl = template.Must(template.New("landing").Parse(landingHTML))

const landingHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Kit — Your team's knowledge, everywhere</title>
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; color: #1a1a2e; background: #fafafa; }
  .container { max-width: 680px; margin: 0 auto; padding: 80px 24px; }
  h1 { font-size: 2.5rem; margin-bottom: 8px; }
  .tagline { font-size: 1.25rem; color: #555; margin-bottom: 48px; line-height: 1.5; }
  .section { margin-bottom: 48px; }
  h2 { font-size: 1.1rem; text-transform: uppercase; letter-spacing: 0.05em; color: #888; margin-bottom: 12px; }
  p { line-height: 1.6; margin-bottom: 16px; }
  ul { margin: 0 0 16px 20px; line-height: 1.8; }
  .btn { display: inline-block; padding: 12px 24px; border-radius: 6px; text-decoration: none; font-weight: 600; font-size: 1rem; }
  .btn-slack { background: #4A154B; color: #fff; }
  .btn-slack:hover { background: #611f69; }
  code { background: #f0f0f0; padding: 2px 6px; border-radius: 3px; font-size: 0.9em; }
  pre { background: #f0f0f0; padding: 16px; border-radius: 6px; overflow-x: auto; font-size: 0.85rem; line-height: 1.5; margin-bottom: 16px; }
  .tab-bar { display: flex; gap: 0; margin-bottom: 0; }
  .tab { padding: 8px 16px; background: #e0e0e0; border: none; cursor: pointer; font-size: 0.9rem; font-family: inherit; border-radius: 6px 6px 0 0; }
  .tab.active { background: #f0f0f0; font-weight: 600; }
  .tab-content { display: none; }
  .tab-content.active { display: block; }
  .tab-content pre { border-radius: 0 6px 6px 6px; margin-bottom: 0; }
</style>
</head>
<body>
<div class="container">
  <h1>Kit</h1>
  <p class="tagline">Your team's knowledge and automation — accessible from Slack, AI tools, or anything you build.</p>

  <div class="section">
    <p>Every team has answers trapped in docs, threads, and people's heads. Kit puts it all in one place and makes it available everywhere your team already works.</p>
    <ul>
      <li>New hire needs the closing checklist? They ask in Slack.</li>
      <li>Building a report with Claude Code? It pulls the data straight from Kit.</li>
      <li>Daily standup summary? Kit runs it automatically at 9am.</li>
    </ul>
    <p>One source of truth. Every tool your team uses can tap into it.</p>
  </div>

  <div class="section">
    <h2>Get Started with Slack</h2>
    <p>Add Kit to your Slack workspace. Your team can ask questions, manage knowledge, and schedule tasks through natural conversation.</p>
    <a href="/slack/install" class="btn btn-slack">Add to Slack</a>
  </div>

  <div class="section">
    <h2>Connect Your AI Tools</h2>
    <p>Use Kit from any MCP-compatible AI client. Create skills, manage rules, and access your team's knowledge without leaving your editor.</p>

    <div class="tab-bar">
      <button class="tab active" onclick="showTab('claude-code')">Claude Code</button>
      <button class="tab" onclick="showTab('cursor')">Cursor</button>
    </div>

    <div id="claude-code" class="tab-content active">
      <pre>claude mcp add --transport http kit {{.BaseURL}}/mcp</pre>
    </div>

    <div id="cursor" class="tab-content">
      <pre>// Add to .cursor/mcp.json
{
  "mcpServers": {
    "kit": {
      "type": "streamable-http",
      "url": "{{.BaseURL}}/mcp"
    }
  }
}</pre>
    </div>

    <p>On first connect, you'll sign in with Slack — same identity, no extra accounts.</p>
  </div>
</div>

<script>
function showTab(id) {
  document.querySelectorAll('.tab-content').forEach(el => el.classList.remove('active'));
  document.querySelectorAll('.tab').forEach(el => el.classList.remove('active'));
  document.getElementById(id).classList.add('active');
  event.target.classList.add('active');
}
</script>
</body>
</html>`

// NewLandingHandler creates a handler that serves the landing page with the given base URL.
func NewLandingHandler(baseURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		landingTmpl.Execute(w, map[string]string{"BaseURL": baseURL})
	}
}
