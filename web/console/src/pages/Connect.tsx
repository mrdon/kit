import { useState } from 'react';
import { Link } from 'react-router-dom';
import { SLUG } from '../workspace';
import { useSetChatContext } from '../chatContext';

// Connect surfaces this workspace's real MCP endpoint and card-stack URL so
// users can wire up Claude Code, Cursor, and other MCP clients without
// hunting for their slug. The public landing page only shows placeholder
// (`your-slug`) instructions; this page fills them in for the tenant you're
// signed into.

const origin = window.location.origin;
const mcpURL = `${origin}/${SLUG}/mcp`;
const cardsURL = `${origin}/${SLUG}/`;
const claudeCmd = `claude mcp add --transport http kit ${mcpURL}`;
const cursorJSON = `{
  "mcpServers": {
    "kit-${SLUG}": {
      "type": "streamable-http",
      "url": "${mcpURL}"
    }
  }
}`;

// Copyable renders a code block with a copy button that reports success.
function Copyable({ label, text }: { label: string; text: string }) {
  const [copied, setCopied] = useState(false);
  const copy = () => {
    navigator.clipboard.writeText(text).then(() => {
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    });
  };
  return (
    <div className="connect-block">
      {label && <span className="connect-label">{label}</span>}
      <div className="connect-code">
        <pre>{text}</pre>
        <button className="connect-copy" onClick={copy} type="button">
          {copied ? 'Copied' : 'Copy'}
        </button>
      </div>
    </div>
  );
}

export default function Connect() {
  useSetChatContext('the Connect AI tools page (MCP endpoint and card-stack URLs for this workspace)');
  return (
    <div className="page">
      <div className="page-head">
        <nav className="crumbs">
          <Link to="/">Home</Link>
          <span className="crumb-sep">/</span>
          <span>Connect AI tools</span>
        </nav>
        <h1>Connect AI tools</h1>
        <p className="page-sub">
          Use Kit from any MCP-compatible AI client. Create skills, manage
          rules, and pull your team's knowledge into your editor. On first
          connect you'll sign in with Slack — same identity, no extra account.
        </p>
      </div>

      <section className="card-list">
        <article className="card connect-card">
          <span className="card-title">MCP endpoint</span>
          <span className="card-desc">
            This workspace's endpoint. Every AI client points here.
          </span>
          <Copyable label="" text={mcpURL} />
        </article>

        <article className="card connect-card">
          <span className="card-title">Claude Code</span>
          <span className="card-desc">Run this once to add Kit as an MCP server.</span>
          <Copyable label="" text={claudeCmd} />
        </article>

        <article className="card connect-card">
          <span className="card-title">Cursor</span>
          <span className="card-desc">
            Add to <code>.cursor/mcp.json</code> — one entry per workspace.
          </span>
          <Copyable label="" text={cursorJSON} />
        </article>

        <article className="card connect-card">
          <span className="card-title">Card stack</span>
          <span className="card-desc">
            The swipeable mobile feed for decisions and briefings. Open it on
            your phone and add to your home screen for a PWA.
          </span>
          <Copyable label="" text={cardsURL} />
          <a className="connect-open" href={cardsURL}>
            Open card stack →
          </a>
        </article>
      </section>
    </div>
  );
}
