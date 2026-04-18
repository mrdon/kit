import { useEffect, useState } from 'react';
import ChatTranscript from './ChatTranscript';
import ChatComposer from './ChatComposer';
import { useChatStream } from './useChatStream';

type Props = {
  // The card this chat is scoped to. One conversation per (card, user)
  // on the server, keyed by these three fields.
  sourceApp: string;
  kind: string;
  id: string;
  // Display-only: shown in the sheet header.
  title: string;
  // Dismiss the sheet. The parent should re-enable long-press on the
  // stack when this fires.
  onClose: () => void;
  // Called when a turn completes so the parent can refresh the stack
  // (the agent may have completed/deleted/rescheduled cards).
  onTurnDone?: () => void;
};

/**
 * Bottom-sheet overlay bound to a single card. Holds the chat
 * transcript + composer. Pure container — transcript rendering,
 * composer input, and SSE streaming all live in sub-components and the
 * useChatStream hook.
 */
export default function CardChatSheet({ sourceApp, kind, id, title, onClose, onTurnDone }: Props) {
  const { turns, busy, send, stop, retry } = useChatStream({ sourceApp, kind, id }, onTurnDone);
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
        <ChatComposer card={{ sourceApp, kind, id }} busy={busy} onSubmit={send} />
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
