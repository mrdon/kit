// Surface-agnostic fetch helpers for the chat endpoints. Both take a
// fully-built URL (the caller knows its own base path) and return the
// raw fetch Response whose body is an SSE stream. Extracted from the
// cards PWA's api.ts so the console can reuse the exact same wire
// behaviour without duplicating it.

// chatTranscribe uploads audio to the given URL and returns the fetch
// Response whose body is an SSE stream of partial/final/error events.
// The X-Kit-Chat header lifts the request out of the CORS "simple
// request" category so the server's CSRF check passes for multipart
// bodies.
export function chatTranscribe(
  url: string,
  audio: Blob,
  signal?: AbortSignal,
): Promise<Response> {
  const form = new FormData();
  form.append('audio', audio, 'clip');
  return fetch(url, {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'X-Kit-Chat': '1' },
    body: form,
    signal,
  });
}

// chatExecute posts the user's text (typed or edited transcript) to the
// given URL and returns an SSE stream of status/tool/response/done
// events. clientSessionID is required for quick chat and ignored by
// card chat (the server keys on the card triple instead).
export function chatExecute(
  url: string,
  text: string,
  opts?: { clientSessionID?: string },
  signal?: AbortSignal,
): Promise<Response> {
  const body: Record<string, unknown> = { text };
  if (opts?.clientSessionID) body.client_session_id = opts.clientSessionID;
  return fetch(url, {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
    signal,
  });
}
