import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
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
 * cookie, so auth Just Works. A paired device holding kit.chat uses
 * /{slug}/api/device/chat/* instead, where it answers questions only.
 */
// DEVICE_IDLE_MS is how long a device's chat stays open after its last answer.
const DEVICE_IDLE_MS = 10 * 60 * 1000;

export default function ConsoleChat({ device = false }: { device?: boolean }) {
  const [open, setOpen] = useState(false);
  // New session per open: re-mint when toggled on. On a shared device that
  // is also what keeps one person's questions out of the next person's.
  const clientSessionID = useMemo(() => crypto.randomUUID(), [open]);
  const page = useConsoleChatContext();
  // A paired device chats on its own capability-gated mounts, as the device.
  const base = device ? `/${SLUG}/api/device` : `/${SLUG}/api/v1`;

  // On a shared device, an open chat closes after a quiet spell, so the next
  // person starts a fresh conversation instead of inheriting this one.
  const idleTimer = useRef<number | undefined>(undefined);
  const touch = useCallback(() => {
    window.clearTimeout(idleTimer.current);
    if (device && open) {
      idleTimer.current = window.setTimeout(() => setOpen(false), DEVICE_IDLE_MS);
    }
  }, [device, open]);
  useEffect(() => {
    touch();
    return () => window.clearTimeout(idleTimer.current);
  }, [touch]);
  const onTurnDone = () => {
    touch();
    page?.onTurnDone?.();
  };

  return (
    <>
      <button
        type="button"
        className="console-chat-fab"
        aria-label="Open Kit assistant"
        onClick={() => setOpen(true)}
      >
        <svg
          width="22"
          height="22"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.75"
          strokeLinecap="round"
          strokeLinejoin="round"
          aria-hidden="true"
        >
          <path d="M21 11.5a8.38 8.38 0 0 1-8.5 8.5 8.5 8.5 0 0 1-3.8-.9L3 21l1.9-5.7A8.38 8.38 0 0 1 4 11.5 8.5 8.5 0 0 1 12.5 3 8.38 8.38 0 0 1 21 11.5z" />
        </svg>
      </button>
      {open && (
        <ChatSheetBody
          title="Kit assistant"
          executeUrl={device ? `${base}/chat/execute` : `${base}/chat/quick/execute`}
          transcribeUrl={`${base}/chat/transcribe`}
          loginUrl={`/${SLUG}/login`}
          clientSessionID={clientSessionID}
          pageContext={device ? undefined : page?.description}
          placeholder={device ? 'Ask Kit a question…' : 'Ask Kit, or add a task…'}
          onClose={() => setOpen(false)}
          onTurnDone={onTurnDone}
        />
      )}
    </>
  );
}
