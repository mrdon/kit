import { useMemo } from 'react';
import { ChatSheetBody } from '@chat';
import { chatTranscribeUrl, quickChatExecuteUrl } from '../api';
import { BASENAME } from '../workspace';

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
 * Quick-chat (card-less) sheet. Opens empty for fast capture and stays
 * open until the user dismisses it (backdrop tap, ✕, or Escape).
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
      loginUrl={BASENAME + '/login'}
      clientSessionID={clientSessionID}
      placeholder="Add a todo, ask a question…"
      onClose={onClose}
      onTurnDone={onTurnDone}
      seedAudioBlob={seedAudioBlob}
    />
  );
}
