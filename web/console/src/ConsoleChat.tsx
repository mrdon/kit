import { useMemo, useState } from 'react';
import { ChatSheetBody } from '@chat';
import { SLUG } from './workspace';
import { useConsoleChatContext } from './chatContext';

/**
 * Global console chat launcher: a persistent floating circle (bottom-right,
 * mirroring the cards PWA FAB) that opens the shared chat widget in card-less
 * "quick chat" mode. Mounted once in Shell so it's available on every page.
 *
 * It reads the active page's context (registered via useSetChatContext) and
 * forwards both the description — so the agent can resolve "this"/"here" — and
 * the page's onTurnDone refresh hook. A fresh client session id per open keeps
 * each conversation self-contained.
 *
 * The chat endpoints live under the cards API prefix (/{slug}/api/v1), distinct
 * from the console's own /{slug}/api JSON routes; same origin and session
 * cookie, so auth Just Works.
 */
export default function ConsoleChat() {
  const [open, setOpen] = useState(false);
  // New session per open: re-mint when toggled on.
  const clientSessionID = useMemo(() => crypto.randomUUID(), [open]);
  const page = useConsoleChatContext();
  const base = `/${SLUG}/api/v1`;

  return (
    <>
      <button
        type="button"
        className="console-chat-fab"
        aria-label="Open Kit assistant"
        onClick={() => setOpen(true)}
      >
        💬
      </button>
      {open && (
        <ChatSheetBody
          title="Kit assistant"
          executeUrl={`${base}/chat/quick/execute`}
          transcribeUrl={`${base}/chat/transcribe`}
          loginUrl={`/${SLUG}/login`}
          clientSessionID={clientSessionID}
          pageContext={page?.description}
          placeholder="Ask Kit, or add a task…"
          onClose={() => setOpen(false)}
          onTurnDone={page?.onTurnDone}
        />
      )}
    </>
  );
}
