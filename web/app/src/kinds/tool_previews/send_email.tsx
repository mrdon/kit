import { useState } from 'react';
import ReactMarkdown from 'react-markdown';
import type { ToolPreviewProps } from './index';

type SendEmailArgs = {
  to?: string[];
  cc?: string[];
  bcc?: string[];
  subject?: string;
  body?: string;
  in_reply_to?: string;
  references?: string[];
};

export function SendEmailPreview({ args }: ToolPreviewProps) {
  const a = (args ?? {}) as SendEmailArgs;
  const to = a.to ?? [];
  const cc = a.cc ?? [];
  const bcc = a.bcc ?? [];
  const body = a.body ?? '';
  const isLong = body.split('\n').length > 25 || body.length > 1200;
  const [expanded, setExpanded] = useState(false);
  const shown = !isLong || expanded ? body : truncate(body);

  return (
    <div className="tool-preview tool-preview--send-email">
      <dl className="tool-preview__headers">
        {to.length > 0 && (
          <>
            <dt>To</dt>
            <dd>{to.join(', ')}</dd>
          </>
        )}
        {cc.length > 0 && (
          <>
            <dt>Cc</dt>
            <dd>{cc.join(', ')}</dd>
          </>
        )}
        {bcc.length > 0 && (
          <>
            <dt>Bcc</dt>
            <dd>{bcc.join(', ')}</dd>
          </>
        )}
        <dt>Subject</dt>
        <dd>{a.subject ?? <em>(no subject)</em>}</dd>
        {a.in_reply_to && (
          <>
            <dt>In reply to</dt>
            <dd className="tool-preview__muted">{a.in_reply_to}</dd>
          </>
        )}
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
