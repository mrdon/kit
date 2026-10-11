import type { ChatUsage } from './events';
import { useEffect, useRef } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import type { ChatTurn } from './useChatStream';
import ChatStatusRow from './ChatStatusRow';

type Props = {
  turns: ChatTurn[];
  onStop: () => void;
  onRetry: (key: string) => void;
};

/**
 * Renders the scrollable transcript. Each turn gets a user bubble and,
 * while the agent runs, a status row. Once the response arrives it's
 * rendered as an assistant bubble (markdown-rendered, matching how
 * bodies are rendered elsewhere).
 */
export default function ChatTranscript({ turns, onStop, onRetry }: Props) {
  const bottomRef = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    // Scroll the transcript itself, not the page: scrollIntoView walks every
    // scrollable ancestor, so each tool event nudged the whole page up.
    const pane = bottomRef.current?.parentElement;
    if (pane) pane.scrollTo({ top: pane.scrollHeight, behavior: 'smooth' });
  }, [turns.length, turns[turns.length - 1]?.status, turns[turns.length - 1]?.response]);

  return (
    <div className="chat-transcript">
      {turns.map((t) => (
        <div key={t.key} className="chat-turn">
          {t.attachments && t.attachments.length > 0 && (
            <div className="chat-turn-attachments">
              {t.attachments.map((a, i) =>
                a.previewUrl ? (
                  <img
                    key={`${a.name}-${i}`}
                    src={a.previewUrl}
                    alt={a.name}
                    className="chat-attachment-thumb"
                  />
                ) : (
                  <span key={`${a.name}-${i}`} className="chat-attachment-chip" title={a.name}>
                    📄 {a.name}
                  </span>
                ),
              )}
            </div>
          )}
          {t.userText && <div className="chat-bubble chat-bubble-user">{t.userText}</div>}
          <ChatStatusRow
            status={t.status}
            inFlight={t.inFlight}
            toolCount={t.toolCount}
            onStop={onStop}
          />
          {t.response && (
            <div className="chat-bubble chat-bubble-assistant markdown">
              <ReactMarkdown remarkPlugins={[remarkGfm]}>{t.response}</ReactMarkdown>
            </div>
          )}
          {!t.inFlight && t.toolCount > 0 && t.response && (
            <div className="chat-tool-count" aria-label={`${t.toolCount} tool ${t.toolCount === 1 ? 'call' : 'calls'}`}>
              {t.toolCount} tool {t.toolCount === 1 ? 'call' : 'calls'}
              {t.usage && <span className="chat-usage"> · {usageText(t.usage)}</span>}
            </div>
          )}
          {t.errorMessage && (
            <div className="chat-turn-error">
              <span>{t.errorMessage}</span>
              <button type="button" onClick={() => onRetry(t.key)} className="chat-retry">
                Retry
              </button>
            </div>
          )}
        </div>
      ))}
      <div ref={bottomRef} />
    </div>
  );
}

// usageText is the one-line cost of a turn: tokens in and out, and a rough
// dollar figure when the model is priced. Cached input is counted as in.
function usageText(u: ChatUsage): string {
  const k = (n: number) => (n >= 1000 ? `${(n / 1000).toFixed(n >= 10000 ? 0 : 1)}k` : String(n));
  const inTok = u.input_tokens + u.cache_read_input_tokens + u.cache_creation_input_tokens;
  const parts = [`${u.calls} ${u.calls === 1 ? 'call' : 'calls'}`, `${k(inTok)} in`, `${k(u.output_tokens)} out`];
  if (u.cost_usd !== undefined) parts.push(u.cost_usd < 0.01 ? '<$0.01' : `~$${u.cost_usd.toFixed(2)}`);
  return parts.join(' · ');
}
