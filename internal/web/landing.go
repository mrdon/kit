package web

import (
	"net/http"
)

const landingHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Kit — Knowledge base for your team</title>
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; color: #1a1a2e; background: #fafafa; }
  .container { max-width: 640px; margin: 0 auto; padding: 80px 24px; }
  h1 { font-size: 2.5rem; margin-bottom: 8px; }
  .tagline { font-size: 1.25rem; color: #555; margin-bottom: 48px; }
  .section { margin-bottom: 40px; }
  h2 { font-size: 1.1rem; text-transform: uppercase; letter-spacing: 0.05em; color: #888; margin-bottom: 12px; }
  p { line-height: 1.6; margin-bottom: 16px; }
  .btn { display: inline-block; padding: 12px 24px; border-radius: 6px; text-decoration: none; font-weight: 600; font-size: 1rem; }
  .btn-slack { background: #4A154B; color: #fff; }
  .btn-slack:hover { background: #611f69; }
  code { background: #f0f0f0; padding: 2px 6px; border-radius: 3px; font-size: 0.9em; }
  pre { background: #f0f0f0; padding: 16px; border-radius: 6px; overflow-x: auto; font-size: 0.85rem; line-height: 1.5; }
</style>
</head>
<body>
<div class="container">
  <h1>Kit</h1>
  <p class="tagline">Role-aware knowledge base for your team</p>

  <div class="section">
    <p>Kit gives your team instant access to skills, rules, and memories — scoped by role so everyone sees what's relevant to them. Manage knowledge through Slack or any AI client via MCP.</p>
  </div>

  <div class="section">
    <h2>Install into Slack</h2>
    <p>Add Kit to your workspace to get started with the conversational interface.</p>
    <a href="/slack/install" class="btn btn-slack">Add to Slack</a>
  </div>

  <div class="section">
    <h2>Connect via MCP</h2>
    <p>Use Kit from Claude Code, Cursor, or any MCP-compatible AI client. Add this to your MCP configuration:</p>
    <pre>{
  "mcpServers": {
    "kit": {
      "type": "streamable-http",
      "url": "https://&lt;your-kit-domain&gt;/mcp"
    }
  }
}</pre>
    <p>On first connect, you'll sign in with Slack to link your identity.</p>
  </div>
</div>
</body>
</html>`

// HandleLanding serves the Kit landing page.
func HandleLanding(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(landingHTML))
}
