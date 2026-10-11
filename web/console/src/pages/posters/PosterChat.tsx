import { useEffect, useMemo, useState } from 'react';
import ChatTranscript from '@chat/ChatTranscript';
import ChatComposer from '@chat/ChatComposer';
import { useChatStream } from '@chat/useChatStream';
import { apiPost, type ChatHistoryTurn } from '../../api';
import { SLUG } from '../../workspace';

// The chat panel on a poster or template page. The shared widget's
// transcript and composer, mounted in a side panel rather than the
// bottom sheet: the subject is on screen next to it, so "move the photo
// left" has something to point at. The server keys the session on the
// subject and the panel loads that history on mount, so a reload shows
// the conversation the model is still holding.
export default function PosterChat({
  executeUrl,
  title,
  placeholder,
  hint,
  onDone,
  resetUrl,
  loadHistory,
}: {
  executeUrl: string;
  // POST here forgets the server-side conversation; the panel then clears.
  resetUrl?: string;
  // Fetches the past turns the server holds, shown on mount so a reload
  // does not look like a blank conversation the model still remembers.
  loadHistory?: () => Promise<{ turns: ChatHistoryTurn[] }>;
  title: string;
  placeholder: string;
  // One line under the title saying what kind of ask works here.
  hint?: string;
  // Fired after each turn so the page reloads and the new version shows.
  onDone: () => void;
}) {
  const urls = useMemo(
    () => ({
      transcribeUrl: `/${SLUG}/api/v1/chat/transcribe`,
      loginUrl: `/${SLUG}/login`,
    }),
    [],
  );
  const { turns, busy, send, stop, retry, clear, seed } = useChatStream({
    executeUrl,
    loginUrl: urls.loginUrl,
    onDone,
  });
  useEffect(() => {
    if (!loadHistory) return;
    let live = true;
    loadHistory()
      .then((r) => {
        if (!live) return;
        seed(
          r.turns.map((t) => ({
            key: t.key,
            userText: t.user_text,
            response: t.response,
            status: 'done',
            inFlight: false,
            toolCount: t.tool_count,
          })),
        );
      })
      .catch(() => {
        /* a blank panel is the fallback; sending still works */
      });
    return () => {
      live = false;
    };
  }, [loadHistory, seed]);
  const [resetting, setResetting] = useState(false);
  const startOver = async () => {
    if (!resetUrl) return;
    setResetting(true);
    try {
      await apiPost(resetUrl.replace(`/${SLUG}/api`, ''), {});
      clear();
    } finally {
      setResetting(false);
    }
  };

  return (
    <aside className="poster-chat" aria-label={title}>
      <header className="poster-chat-head">
        <div className="poster-chat-title">
          {title}
          {resetUrl && (
            <button
              type="button"
              className="btn btn-ghost btn-sm poster-chat-reset"
              disabled={busy || resetting}
              onClick={startOver}
              title="Forget this conversation and start clean. The poster and its versions stay."
            >
              New conversation
            </button>
          )}
        </div>
        {hint && <div className="poster-chat-hint">{hint}</div>}
      </header>
      {turns.length === 0 ? (
        <div className="chat-transcript poster-chat-empty">
          <p className="muted">{hint ?? 'Describe the change you want.'}</p>
        </div>
      ) : (
        <ChatTranscript turns={turns} onStop={stop} onRetry={retry} />
      )}
      <ChatComposer
        transcribeUrl={urls.transcribeUrl}
        loginUrl={urls.loginUrl}
        busy={busy}
        onSubmit={send}
        placeholder={placeholder}
      />
    </aside>
  );
}
