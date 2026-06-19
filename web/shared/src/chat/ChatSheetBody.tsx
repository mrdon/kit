import { useEffect, useState } from 'react';
import ChatTranscript from './ChatTranscript';
import ChatComposer from './ChatComposer';
import { useChatStream } from './useChatStream';

export type ChatSheetBodyProps = {
  // Header label shown at the top of the sheet.
  title: string;
  // URL to POST each chat turn to. Built by the caller for its surface
  // (card chat, quick chat, tasks chat, …).
  executeUrl: string;
  // URL the mic uploads audio to.
  transcribeUrl: string;
  // Where to bounce the browser on a 401 (each app knows its login path).
  loginUrl: string;
  // Required for quick/tasks chat, ignored for card chat.
  clientSessionID?: string;
  // Optional description of where the user is (web console page), sent with
  // each turn so the agent can resolve "this"/"here". Quick chat only.
  pageContext?: string;
  // Optional placeholder override for the composer.
  placeholder?: string;
  // Dismiss the sheet.
  onClose: () => void;
  // Called when a turn completes so the parent can refresh its view.
  onTurnDone?: () => void;
  // Pre-captured audio handed to the composer to transcribe on open.
  seedAudioBlob?: Blob | null;
};

/**
 * Shared bottom-sheet body for every chat surface (card chat, quick
 * chat, tasks chat). Renders header + transcript + composer and owns
 * keyboard offset + SSE wiring. Stays open until the user closes it
 * (backdrop tap, ✕ button, or Escape).
 */
export default function ChatSheetBody({
  title,
  executeUrl,
  transcribeUrl,
  loginUrl,
  clientSessionID,
  pageContext,
  placeholder,
  onClose,
  onTurnDone,
  seedAudioBlob,
}: ChatSheetBodyProps) {
  const { turns, busy, send, stop, retry } = useChatStream({
    executeUrl,
    loginUrl,
    clientSessionID,
    pageContext,
    onDone: onTurnDone,
  });

  const keyboardOffset = useKeyboardOffset();

  // Escape closes the sheet when the sheet has focus.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !busy) onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [busy, onClose]);

  return (
    <div className="chat-sheet-backdrop" onClick={onClose}>
      <div
        className="chat-sheet"
        onClick={(e) => e.stopPropagation()}
        style={{ bottom: keyboardOffset }}
      >
        <header className="chat-sheet-header">
          <div className="chat-sheet-title" title={title}>
            {title}
          </div>
          <button type="button" className="chat-sheet-close" onClick={onClose} aria-label="Close">
            ×
          </button>
        </header>
        <ChatTranscript turns={turns} onStop={stop} onRetry={retry} />
        <ChatComposer
          transcribeUrl={transcribeUrl}
          loginUrl={loginUrl}
          busy={busy}
          onSubmit={send}
          placeholder={placeholder}
          seedAudioBlob={seedAudioBlob}
        />
      </div>
    </div>
  );
}

/**
 * On iOS Safari the on-screen keyboard doesn't resize the layout
 * viewport. `visualViewport` tells us how much vertical space the
 * keyboard is eating so we can shift the sheet's bottom above it.
 * Desktop browsers set the offset to 0.
 */
function useKeyboardOffset(): number {
  const [offset, setOffset] = useState(0);
  useEffect(() => {
    const vv = window.visualViewport;
    if (!vv) return;
    const update = () => {
      const diff = window.innerHeight - vv.height - vv.offsetTop;
      setOffset(diff > 10 ? diff : 0);
    };
    vv.addEventListener('resize', update);
    vv.addEventListener('scroll', update);
    update();
    return () => {
      vv.removeEventListener('resize', update);
      vv.removeEventListener('scroll', update);
    };
  }, []);
  return offset;
}
