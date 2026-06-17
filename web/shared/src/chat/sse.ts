// Minimal SSE parser for `fetch` ReadableStream responses.
//
// EventSource is GET-only; our chat endpoints POST and receive a
// text/event-stream body, so we parse the wire format ourselves.
// Callers get an async iterable of parsed events. Each event has a
// typed `event` field (from the `event:` line) and a `data` string
// (from one or more `data:` lines, joined with "\n").

export type SSEFrame = {
  event: string;
  data: string;
};

/**
 * Read SSE frames from a fetch Response body. Yields each frame as it
 * arrives. Throws if the response has no body or is not ok.
 *
 * Lifecycle: the caller controls cancellation via AbortController on the
 * original fetch. When the stream ends (server closes), the iterator
 * returns naturally.
 */
export async function* readSSE(response: Response): AsyncGenerator<SSEFrame> {
  if (!response.ok) {
    throw new Error(`SSE request failed: ${response.status} ${response.statusText}`);
  }
  if (!response.body) {
    throw new Error('SSE response has no body');
  }

  const decoder = new TextDecoder('utf-8');
  const reader = response.body.getReader();
  let buffer = '';

  try {
    while (true) {
      const { value, done } = await reader.read();
      if (done) {
        // Any trailing partial frame is dropped — SSE frames are
        // delimited by \n\n so a tail without the delimiter is
        // incomplete.
        return;
      }
      buffer += decoder.decode(value, { stream: true });
      for (;;) {
        const idx = buffer.indexOf('\n\n');
        if (idx < 0) break;
        const raw = buffer.slice(0, idx);
        buffer = buffer.slice(idx + 2);
        const frame = parseFrame(raw);
        if (frame) yield frame;
      }
    }
  } finally {
    reader.releaseLock();
  }
}

function parseFrame(raw: string): SSEFrame | null {
  let event = 'message';
  const dataLines: string[] = [];
  for (const line of raw.split('\n')) {
    if (line.length === 0 || line.startsWith(':')) continue; // comment/keep-alive
    const colon = line.indexOf(':');
    const field = colon < 0 ? line : line.slice(0, colon);
    // SSE spec: value starts after the colon; a single leading space is stripped.
    let value = colon < 0 ? '' : line.slice(colon + 1);
    if (value.startsWith(' ')) value = value.slice(1);
    if (field === 'event') event = value;
    else if (field === 'data') dataLines.push(value);
    // id/retry are not used here
  }
  if (dataLines.length === 0) return null;
  return { event, data: dataLines.join('\n') };
}
