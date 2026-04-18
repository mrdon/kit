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
 * bodies are rendered elsewhere in the PWA).
 */
export default function ChatTranscript({ turns, onStop, onRetry }: Props) {
  const bottomRef = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth', block: 'end' });
  }, [turns.length, turns[turns.length - 1]?.status, turns[turns.length - 1]?.response]);

  return (
    <div className="chat-transcript">
      {turns.map((t) => (
        <div key={t.key} className="chat-turn">
          <div className="chat-bubble chat-bubble-user">{t.userText}</div>
          <ChatStatusRow status={t.status} inFlight={t.inFlight} onStop={onStop} />
          {t.response && (
            <div className="chat-bubble chat-bubble-assistant markdown">
              <ReactMarkdown remarkPlugins={[remarkGfm]}>{t.response}</ReactMarkdown>
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
