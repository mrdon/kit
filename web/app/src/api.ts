import type {
  ActionResult,
  DetailResponse,
  StackResponse,
} from './types';

async function j<T>(r: Response): Promise<T> {
  if (r.status === 401) {
    // Session missing/expired — bounce to Slack OpenID login.
    window.location.href = '/app/login';
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

export const api = {
  stack: async (cursor?: string, limit?: number): Promise<StackResponse> => {
    const params = new URLSearchParams();
    if (cursor) params.set('cursor', cursor);
    if (limit) params.set('limit', String(limit));
    const qs = params.toString();
    const r = await get(`/api/v1/stack${qs ? `?${qs}` : ''}`);
    return j<StackResponse>(r);
  },

  getItem: async <M = unknown>(
    sourceApp: string,
    kind: string,
    id: string,
  ): Promise<DetailResponse<M>> => {
    const r = await get(
      `/api/v1/stack/items/${encodeURIComponent(sourceApp)}/${encodeURIComponent(kind)}/${encodeURIComponent(id)}`,
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
      `/api/v1/stack/items/${encodeURIComponent(sourceApp)}/${encodeURIComponent(kind)}/${encodeURIComponent(id)}/action`,
      { action_id: actionID, params: params ?? undefined },
    );
    return j<ActionResult>(r);
  },
};
