import { useState } from 'react';
import ReactMarkdown from 'react-markdown';
import type { ToolPreviewProps } from './index';

type PostToChannelArgs = {
  channel?: string;
  text?: string;
};

// Matches the backend's displayChannel helper: bare names get a '#'
// prefix, Slack ids (C…, G…, D…) render as-is so long-press / copy-paste
// still works on mobile.
function displayChannel(c: string): string {
  if (!c) return '';
  if (c.startsWith('#')) return c;
  if (/^[CGD][A-Z0-9]+$/.test(c)) return c;
  return '#' + c;
}

export function PostToChannelPreview({ args }: ToolPreviewProps) {
  const a = (args ?? {}) as PostToChannelArgs;
  const text = a.text ?? '';
  const isLong = text.split('\n').length > 25 || text.length > 1200;
  const [expanded, setExpanded] = useState(false);
  const shown = !isLong || expanded ? text : truncate(text);

  return (
    <div className="tool-preview tool-preview--slack-post">
      <dl className="tool-preview__headers">
        <dt>Channel</dt>
        <dd>{a.channel ? displayChannel(a.channel) : <em>(not set)</em>}</dd>
      </dl>
      <div className="tool-preview__body">
        <ReactMarkdown>{shown}</ReactMarkdown>
        {isLong && (
          <button
            type="button"
            className="tool-preview__expand"
            onClick={() => setExpanded((v) => !v)}
          >
            {expanded ? 'Show less' : 'Show more'}
          </button>
        )}
      </div>
    </div>
  );
}

function truncate(body: string): string {
  const lines = body.split('\n');
  if (lines.length > 25) {
    return lines.slice(0, 25).join('\n') + '\n\n…';
  }
  if (body.length > 1200) {
    return body.slice(0, 1200) + '…';
  }
  return body;
}
