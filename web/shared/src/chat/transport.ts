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
//
// When files are present the request is sent as multipart/form-data with
// the X-Kit-Chat header (which lifts it out of the CORS "simple request"
// category so the server's CSRF check passes) — the same shape audio
// upload already uses. Otherwise it stays a plain JSON POST.
export function chatExecute(
  url: string,
  text: string,
  opts?: { clientSessionID?: string; pageContext?: string; files?: File[] },
  signal?: AbortSignal,
): Promise<Response> {
  const files = opts?.files ?? [];
  if (files.length > 0) {
    const form = new FormData();
    form.append('text', text);
    if (opts?.clientSessionID) form.append('client_session_id', opts.clientSessionID);
    if (opts?.pageContext) form.append('page_context', opts.pageContext);
    for (const f of files) form.append('files', f, f.name);
    return fetch(url, {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'X-Kit-Chat': '1' },
      body: form,
      signal,
    });
  }
  const body: Record<string, unknown> = { text };
  if (opts?.clientSessionID) body.client_session_id = opts.clientSessionID;
  if (opts?.pageContext) body.page_context = opts.pageContext;
  return fetch(url, {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
    signal,
  });
}
