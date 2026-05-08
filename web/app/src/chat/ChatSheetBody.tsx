import { useEffect, useState } from 'react';
import ChatTranscript from './ChatTranscript';
import ChatComposer from './ChatComposer';
import { useChatStream } from './useChatStream';

type Props = {
  // Header label shown at the top of the sheet.
  title: string;
  // URL to POST each chat turn to. Built by the caller via
  // cardChatExecuteUrl or quickChatExecuteUrl.
  executeUrl: string;
  // URL the mic uploads audio to. Card-agnostic today but passed in so
  // surfaces can point their own routes if needed.
  transcribeUrl: string;
  // Required for quick chat, ignored for card chat.
  clientSessionID?: string;
  // Optional placeholder override for the composer.
  placeholder?: string;
  // Dismiss the sheet. Parent re-enables long-press on the stack when
  // this fires.
  onClose: () => void;
  // Called when a turn completes so the parent can refresh the stack.
  onTurnDone?: () => void;
  // Pre-captured audio handed to the composer to transcribe on open.
  // Used by the quick-chat FAB's long-press flow.
  seedAudioBlob?: Blob | null;
};

/**
 * Shared bottom-sheet body for both card chat (CardChatSheet) and
 * quick chat (QuickChatSheet). Renders header + transcript + composer
 * and owns keyboard offset + SSE wiring. The sheet stays open until
 * the user closes it (backdrop tap, ✕ button, or Escape).
 */
export default function ChatSheetBody({
  title,
  executeUrl,
  transcribeUrl,
  clientSessionID,
  placeholder,
  onClose,
  onTurnDone,
  seedAudioBlob,
}: Props) {
  const { turns, busy, send, stop, retry } = useChatStream({
    executeUrl,
    clientSessionID,
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
