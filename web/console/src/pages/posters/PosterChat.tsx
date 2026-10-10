import { useMemo } from 'react';
import ChatTranscript from '@chat/ChatTranscript';
import ChatComposer from '@chat/ChatComposer';
import { useChatStream } from '@chat/useChatStream';
import { SLUG } from '../../workspace';

// The chat panel on a poster or template page. The shared widget's
// transcript and composer, mounted in a side panel rather than the
// bottom sheet: the subject is on screen next to it, so "move the photo
// left" has something to point at. The server keys the session on the
// subject, so a reload picks the conversation back up.
export default function PosterChat({
  executeUrl,
  title,
  placeholder,
  hint,
  onDone,
}: {
  executeUrl: string;
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
  const { turns, busy, send, stop, retry } = useChatStream({
    executeUrl,
    loginUrl: urls.loginUrl,
    onDone,
  });

  return (
    <aside className="poster-chat" aria-label={title}>
      <header className="poster-chat-head">
        <div className="poster-chat-title">{title}</div>
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
