import { useCallback, useRef, useState } from 'react';
import { chatExecute } from './transport';
import { readSSE } from './sse';
import { ChatEvent, ChatStatus, type ChatStatusType } from './events';
import { parseEventData } from './parse';

export type ChatTurn = {
  // Stable key for React list rendering.
  key: string;
  // What the user said (or typed). Rendered as the right-aligned bubble.
  userText: string;
  // What Kit said (or "" until response arrives). Left-aligned bubble.
  response: string;
  // Latest status for the in-progress status line below the user bubble.
  // "thinking" | "cancelled" | "done" | "error" | tool name string.
  status: string;
  // When true, a request is in flight and Stop should be shown.
  inFlight: boolean;
  // Count of tool calls fired during this turn (incl. terminal reply
  // tools like reply_in_thread). Surfaced as a small badge alongside
  // the assistant bubble so the user has a persistent indicator that
  // the agent did something — the per-tool status flashes too briefly
  // to register on instant tools.
  toolCount: number;
  // On transport/server error, the message text so the UI can show retry.
  errorMessage?: string;
};

export type UseChatStreamResult = {
  turns: ChatTurn[];
  // True while any turn is executing.
  busy: boolean;
  // Add a new turn for the given user text and start executing it.
  send: (userText: string) => void;
  // Abort the in-flight request, if any.
  stop: () => void;
  // Start a turn using an already-added placeholder (for voice flow
  // where the user bubble was rendered before send). Exported for
  // flexibility but not used by the default composer today.
  retry: (turnKey: string) => void;
};

export type ChatStreamOptions = {
  // URL to POST each turn to. Callers build this for their surface
  // (card chat vs quick chat) so the hook stays agnostic.
  executeUrl: string;
  // Where to send the browser on a 401 (session missing/expired). Each
  // app knows its own login path; the hook stays origin-agnostic.
  loginUrl: string;
  // Required for quick chat, ignored by card chat. The server keys the
  // session on (user, clientSessionID) when the card is absent.
  clientSessionID?: string;
  // Fired when a turn finishes so the parent can refresh its view (the
  // agent may have created/changed data that should now appear).
  onDone?: () => void;
};

/**
 * Hook that drives chat/execute SSE consumption.
 *
 * The caller passes in the execute URL for their surface (card vs quick)
 * plus an optional client session id; we handle fetch lifecycle, SSE
 * parsing, turn state, and abort plumbing.
 */
export function useChatStream(opts: ChatStreamOptions): UseChatStreamResult {
  const { executeUrl, loginUrl, clientSessionID, onDone } = opts;
  const [turns, setTurns] = useState<ChatTurn[]>([]);
  const [busy, setBusy] = useState(false);
  const abortRef = useRef<AbortController | null>(null);

  const updateTurn = useCallback((key: string, patch: Partial<ChatTurn>) => {
    setTurns((ts) => ts.map((t) => (t.key === key ? { ...t, ...patch } : t)));
  }, []);

  const runExecute = useCallback(
    async (turnKey: string, text: string) => {
      const ctrl = new AbortController();
      abortRef.current = ctrl;
      setBusy(true);
      try {
        const resp = await chatExecute(
          executeUrl,
          text,
          clientSessionID ? { clientSessionID } : undefined,
          ctrl.signal,
        );
        if (resp.status === 401) {
          // Streams bypass the JSON fetch wrapper, so handle 401 here.
          window.location.href = loginUrl;
          return;
        }
        if (!resp.ok) {
          // Pre-stream rejections come back as plain http.Error bodies.
          const reason = (await resp.text().catch(() => '')) || `${resp.status} ${resp.statusText}`;
          updateTurn(turnKey, {
            inFlight: false,
            status: 'error',
            errorMessage: reason.trim(),
          });
          return;
        }
        for await (const frame of readSSE(resp)) {
          switch (frame.event) {
            case ChatEvent.Status: {
              const d = parseEventData(frame.data) as { status?: ChatStatusType };
              if (d.status) updateTurn(turnKey, { status: d.status });
              break;
            }
            case ChatEvent.Tool: {
              const d = parseEventData(frame.data) as { name?: string };
              if (d.name) {
                setTurns((ts) =>
                  ts.map((t) =>
                    t.key === turnKey
                      ? { ...t, status: d.name ?? t.status, toolCount: t.toolCount + 1 }
                      : t,
                  ),
                );
              }
              break;
            }
            case ChatEvent.Response: {
              const d = parseEventData(frame.data) as { text?: string };
              if (typeof d.text === 'string') {
                updateTurn(turnKey, { response: d.text });
              }
              break;
            }
            case ChatEvent.Done: {
              updateTurn(turnKey, { inFlight: false, status: 'done' });
              onDone?.();
              break;
            }
            case ChatEvent.Error: {
              const d = parseEventData(frame.data) as { message?: string };
              updateTurn(turnKey, {
                inFlight: false,
                status: 'error',
                errorMessage: d.message ?? 'unknown error',
              });
              break;
            }
          }
        }
      } catch (e) {
        if ((e as Error).name === 'AbortError') {
          updateTurn(turnKey, { inFlight: false, status: ChatStatus.Cancelled });
        } else {
          updateTurn(turnKey, {
            inFlight: false,
            status: 'error',
            errorMessage: (e as Error).message,
          });
        }
      } finally {
        if (abortRef.current === ctrl) abortRef.current = null;
        setBusy(false);
      }
    },
    [executeUrl, loginUrl, clientSessionID, updateTurn, onDone],
  );

  const send = useCallback(
    (userText: string) => {
      const key = `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
      setTurns((ts) => [
        ...ts,
        {
          key,
          userText,
          response: '',
          status: ChatStatus.Thinking,
          inFlight: true,
          toolCount: 0,
        },
      ]);
      runExecute(key, userText);
    },
    [runExecute],
  );

  const stop = useCallback(() => {
    abortRef.current?.abort();
  }, []);

  const retry = useCallback(
    (turnKey: string) => {
      const t = turns.find((x) => x.key === turnKey);
      if (!t) return;
      updateTurn(turnKey, {
        inFlight: true,
        status: ChatStatus.Thinking,
        errorMessage: undefined,
        response: '',
        toolCount: 0,
      });
      runExecute(turnKey, t.userText);
    },
    [turns, updateTurn, runExecute],
  );

  return { turns, busy, send, stop, retry };
}
