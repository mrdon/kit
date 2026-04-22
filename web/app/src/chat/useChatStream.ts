import { useCallback, useRef, useState } from 'react';
import { api } from '../api';
import { readSSE } from '../sse';
import { BASENAME } from '../workspace';
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
  // URL to POST each turn to. Callers build this via cardChatExecuteUrl
  // or quickChatExecuteUrl so the hook stays agnostic to surface.
  executeUrl: string;
  // Required for quick chat, ignored by card chat. The server keys the
  // session on (user, clientSessionID) when the card is absent.
  clientSessionID?: string;
  // Fired on each successful turn done event (for refreshing the stack
  // or running auto-dismiss logic in the parent sheet). askedQuestion
  // is true when the final response text ends with a question mark —
  // a belt-and-braces signal that the agent expects the user to reply,
  // so quick-chat shouldn't auto-dismiss even if a tool fired.
  onDone?: (info: { actionTaken: boolean; askedQuestion: boolean }) => void;
};

// Terminal tools — firing one of these is just the agent's response,
// not an action. Anything else firing means the agent did something.
// Matches the Terminal: true set in internal/tools/core.go.
const TERMINAL_TOOLS = new Set(['reply_in_thread', 'post_to_channel', 'dm_user']);

/**
 * Hook that drives chat/execute SSE consumption.
 *
 * The caller passes in the execute URL for their surface (card vs quick)
 * plus an optional client session id; we handle fetch lifecycle, SSE
 * parsing, turn state, abort plumbing, and action detection for the
 * auto-dismiss affordance.
 */
export function useChatStream(opts: ChatStreamOptions): UseChatStreamResult {
  const { executeUrl, clientSessionID, onDone } = opts;
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
      let actionTaken = false;
      let lastResponse = '';
      try {
        const resp = await api.chatExecute(
          executeUrl,
          text,
          clientSessionID ? { clientSessionID } : undefined,
          ctrl.signal,
        );
        if (resp.status === 401) {
          // The regular api.j() handles this for JSON calls; do it
          // manually for streams.
          window.location.href = BASENAME + '/login';
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
                updateTurn(turnKey, { status: d.name });
                if (!TERMINAL_TOOLS.has(d.name)) actionTaken = true;
              }
              break;
            }
            case ChatEvent.Response: {
              const d = parseEventData(frame.data) as { text?: string };
              if (typeof d.text === 'string') {
                lastResponse = d.text;
                updateTurn(turnKey, { response: d.text });
              }
              break;
            }
            case ChatEvent.Done: {
              updateTurn(turnKey, { inFlight: false, status: 'done' });
              const askedQuestion = /\?\s*$/.test(lastResponse);
              onDone?.({ actionTaken, askedQuestion });
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
    [executeUrl, clientSessionID, updateTurn, onDone],
  );

  const send = useCallback(
    (userText: string) => {
      const key = `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
      setTurns((ts) => [
        ...ts,
        { key, userText, response: '', status: ChatStatus.Thinking, inFlight: true },
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
      });
      runExecute(turnKey, t.userText);
    },
    [turns, updateTurn, runExecute],
  );

  return { turns, busy, send, stop, retry };
}

