import { useMemo } from 'react';
import ChatSheetBody from './ChatSheetBody';
import { chatTranscribeUrl, quickChatExecuteUrl } from '../api';

type Props = {
  // Dismiss the sheet. The parent re-enables the FAB / clears state
  // when this fires.
  onClose: () => void;
  // Called when a turn completes so the parent can refresh the stack
  // (the agent may have captured a todo that surfaces in the next page).
  onTurnDone?: () => void;
  // Optional pre-captured audio blob to transcribe on open. Used by
  // the FAB's long-press-to-record flow: the FAB records while the
  // sheet is closed, then opens the sheet and hands off the blob.
  seedAudioBlob?: Blob | null;
};

/**
 * Quick-chat (card-less) sheet. Optimized for fast capture — opens
 * empty, auto-dismisses ~1.5s after the agent fires a non-terminal
 * tool, but stays open on clarifications/questions/approvals.
 *
 * Session is fresh per open: we mint a UUID on mount and pass it with
 * every turn, so multi-turn within one open attaches but a close+reopen
 * starts clean.
 */
export default function QuickChatSheet({ onClose, onTurnDone, seedAudioBlob }: Props) {
  // Fresh client session id per mount. useMemo[] is intentional — we
  // want one id for the lifetime of this sheet, regardless of re-renders.
  const clientSessionID = useMemo(() => crypto.randomUUID(), []);

  return (
    <ChatSheetBody
      title="Quick chat"
      executeUrl={quickChatExecuteUrl()}
      transcribeUrl={chatTranscribeUrl()}
      clientSessionID={clientSessionID}
      placeholder="Add a todo, ask a question…"
      onClose={onClose}
      onTurnDone={onTurnDone}
      autoDismissOnAction
      seedAudioBlob={seedAudioBlob}
    />
  );
}
