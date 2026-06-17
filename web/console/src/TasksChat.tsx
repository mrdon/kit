import { useMemo } from 'react';
import { ChatSheetBody } from '@chat';
import { SLUG } from './workspace';

type Props = {
  onClose: () => void;
  // Fired when a turn completes so the page can refresh — the assistant
  // may have created or changed tasks.
  onTurnDone?: () => void;
};

/**
 * Console-side wrapper around the shared chat widget (the same one the
 * cards PWA uses). Runs in card-less "quick chat" mode against Kit's
 * agent, which has the task tools — so the user can talk or type to add,
 * update, and ask about tasks. A fresh client session id per open keeps
 * each conversation self-contained.
 *
 * The chat endpoints live under the cards API prefix (/{slug}/api/v1),
 * distinct from the console's own /{slug}/api JSON routes; same origin
 * and session cookie, so auth Just Works.
 */
export default function TasksChat({ onClose, onTurnDone }: Props) {
  const clientSessionID = useMemo(() => crypto.randomUUID(), []);
  const base = `/${SLUG}/api/v1`;
  return (
    <ChatSheetBody
      title="Kit assistant"
      executeUrl={`${base}/chat/quick/execute`}
      transcribeUrl={`${base}/chat/transcribe`}
      loginUrl={`/${SLUG}/login`}
      clientSessionID={clientSessionID}
      placeholder="Ask about your tasks, or add one…"
      onClose={onClose}
      onTurnDone={onTurnDone}
    />
  );
}
