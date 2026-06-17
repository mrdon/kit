import ChatSheetBody from './ChatSheetBody';
import { cardChatExecuteUrl, chatTranscribeUrl } from '../api';
import { BASENAME } from '../workspace';

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
 * Thin wrapper over the shared ChatSheetBody for the card-scoped
 * surface. Builds the card execute URL and passes the card title; all
 * the heavy lifting (transcript, composer, SSE, keyboard offset) lives
 * in the shared widget. No auto-dismiss — card chat is conversational.
 */
export default function CardChatSheet({ sourceApp, kind, id, title, onClose, onTurnDone }: Props) {
  return (
    <ChatSheetBody
      title={title}
      executeUrl={cardChatExecuteUrl(sourceApp, kind, id)}
      transcribeUrl={chatTranscribeUrl()}
      loginUrl={BASENAME + '/login'}
      onClose={onClose}
      onTurnDone={onTurnDone}
    />
  );
}
