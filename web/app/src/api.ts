import type {
  ActionResult,
  DetailResponse,
  StackResponse,
} from './types';
import { BASENAME } from './workspace';

async function j<T>(r: Response): Promise<T> {
  if (r.status === 401) {
    // Session missing/expired — bounce to Slack OpenID login.
    window.location.href = BASENAME + '/login';
    // Throw so awaiting callers don't try to parse a body that won't arrive.
    throw new Error('redirecting to login');
  }
  if (!r.ok) {
    let msg = `${r.status} ${r.statusText}`;
    try {
      const body = await r.json();
      if (body?.error) msg = body.error;
    } catch {
      /* ignore */
    }
    throw new Error(msg);
  }
  return r.json() as Promise<T>;
}

const post = (path: string, body?: unknown) =>
  fetch(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    credentials: 'same-origin',
    body: body ? JSON.stringify(body) : '{}',
  });

const get = (path: string) => fetch(path, { credentials: 'same-origin' });

const cardPath = (sourceApp: string, kind: string, id: string) =>
  `${BASENAME}/api/v1/stack/items/${encodeURIComponent(sourceApp)}/${encodeURIComponent(kind)}/${encodeURIComponent(id)}`;

// Chat URL builders. Callers build the right URL for their surface
// (card chat vs quick chat) and pass it to chatExecute / chatTranscribe,
// which are otherwise surface-agnostic. Transcribe is card-agnostic on
// the server, so there's one shared URL.
export const cardChatExecuteUrl = (sourceApp: string, kind: string, id: string) =>
  `${cardPath(sourceApp, kind, id)}/chat/execute`;
export const quickChatExecuteUrl = () => `${BASENAME}/api/v1/chat/quick/execute`;
export const chatTranscribeUrl = () => `${BASENAME}/api/v1/chat/transcribe`;

// Action endpoint URL. Exported so the pending-action fuse can flush
// via navigator.sendBeacon on pagehide without depending on the api
// object's fetch wrapper.
export const stackActionUrl = (sourceApp: string, kind: string, id: string) =>
  `${cardPath(sourceApp, kind, id)}/action`;

export const api = {
  stack: async (
    opts?: { cursor?: string; limit?: number; focus?: string },
  ): Promise<StackResponse> => {
    const params = new URLSearchParams();
    if (opts?.cursor) params.set('cursor', opts.cursor);
    if (opts?.limit) params.set('limit', String(opts.limit));
    if (opts?.focus) params.set('focus', opts.focus);
    const qs = params.toString();
    const r = await get(`${BASENAME}/api/v1/stack${qs ? `?${qs}` : ''}`);
    return j<StackResponse>(r);
  },

  getItem: async <M = unknown>(
    sourceApp: string,
    kind: string,
    id: string,
  ): Promise<DetailResponse<M>> => {
    const r = await get(
      `${BASENAME}/api/v1/stack/items/${encodeURIComponent(sourceApp)}/${encodeURIComponent(kind)}/${encodeURIComponent(id)}`,
    );
    return j<DetailResponse<M>>(r);
  },

  doAction: async (
    sourceApp: string,
    kind: string,
    id: string,
    actionID: string,
    params?: unknown,
  ): Promise<ActionResult> => {
    const r = await post(
      `${cardPath(sourceApp, kind, id)}/action`,
      { action_id: actionID, params: params ?? undefined },
    );
    return j<ActionResult>(r);
  },

  // The chat fetch helpers (transcribe/execute) live in the shared chat
  // widget (web/shared/chat → @chat/transport); the CardChatSheet /
  // QuickChatSheet wrappers build the URLs via the exports above and the
  // shared hooks call them. Keeping them out of here avoids duplication.
};
