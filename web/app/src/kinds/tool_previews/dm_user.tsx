import { useState } from 'react';
import ReactMarkdown from 'react-markdown';
import type { ToolPreviewProps } from './index';

type DMUserArgs = {
  user_id?: string;
  text?: string;
};

export function DMUserPreview({ args }: ToolPreviewProps) {
  const a = (args ?? {}) as DMUserArgs;
  const text = a.text ?? '';
  const isLong = text.split('\n').length > 25 || text.length > 1200;
  const [expanded, setExpanded] = useState(false);
  const shown = !isLong || expanded ? text : truncate(text);

  return (
    <div className="tool-preview tool-preview--slack-dm">
      <dl className="tool-preview__headers">
        <dt>Recipient</dt>
        <dd>{a.user_id ? `<@${a.user_id}>` : <em>(not set)</em>}</dd>
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
