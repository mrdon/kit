import type { Card, CardDetailResponse, StackResponse } from './types';

async function j<T>(r: Response): Promise<T> {
  if (!r.ok) {
    let msg = `${r.status} ${r.statusText}`;
    try {
      const body = await r.json();
      if (body?.error) msg = body.error;
    } catch { /* ignore */ }
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

const get = (path: string) =>
  fetch(path, { credentials: 'same-origin' });

export const api = {
  stack: async (): Promise<Card[]> => {
    const r = await get('/api/v1/stack');
    const body = await j<StackResponse>(r);
    return body.items ?? [];
  },
  card: async (id: string): Promise<CardDetailResponse> => {
    const r = await get(`/api/v1/cards/${id}`);
    return j<CardDetailResponse>(r);
  },
  resolve: async (id: string, optionID?: string): Promise<Card> => {
    const r = await post(`/api/v1/cards/${id}/resolve`, optionID ? { option_id: optionID } : {});
    const body = await j<{ card: Card }>(r);
    return body.card;
  },
  ack: async (id: string, kind: 'archived' | 'dismissed' | 'saved'): Promise<Card> => {
    const r = await post(`/api/v1/cards/${id}/ack`, { kind });
    const body = await j<{ card: Card }>(r);
    return body.card;
  },
};
