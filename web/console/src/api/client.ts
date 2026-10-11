import { SLUG } from '../workspace';

// The console talks to JSON endpoints under /{slug}/web/api/*. Those
// routes return 401 (never a 303-to-login) on a missing/expired session
// — a redirect would dump login HTML into fetch().json(). We intercept
// 401 here and do the client-side bounce to the login interstitial,
// preserving where the user was via return_to.
//
// State-changing requests carry the X-Kit-Web: 1 header. The server
// requires it on every POST/PATCH/DELETE; sending a custom header lifts
// the request out of the CORS "simple request" category, which is the
// CSRF guard (same pattern as the cards app's X-Kit-Chat / vault's
// X-Kit-Vault).
export const CSRF_HEADER = 'X-Kit-Web';

// The console API lives at /{slug}/api/... — fetch-only, not under the
// /web shell prefix. The cards service worker skips anything containing
// /api/, so these responses are never cached.
export const API_BASE = `/${SLUG}/api`;

export function loginRedirect(): never {
  const returnTo = encodeURIComponent(
    location.pathname + location.search,
  );
  window.location.href = `/${SLUG}/login?return_to=${returnTo}`;
  throw new Error('redirecting to login');
}

export async function parse<T>(r: Response): Promise<T> {
  if (r.status === 401) loginRedirect();
  if (!r.ok) throw new Error(await errorMessage(r));
  if (r.status === 204) return undefined as T;
  return r.json() as Promise<T>;
}

// The server refuses a request in two dialects: a JSON `{error}` body from
// most handlers, and Go's `http.Error` plain text from the rest. Read both.
// Reading only the JSON one turned every plain-text refusal into "400 Bad
// Request" — throwing away a message the handler had gone to the trouble of
// writing for exactly this moment ("topic "Space" has 0 fresh questions but
// the board needs 2"), and leaving the host with nothing to act on.
export async function errorMessage(r: Response): Promise<string> {
  const fallback = `${r.status} ${r.statusText}`;
  let body: string;
  try {
    body = (await r.text()).trim();
  } catch {
    return fallback;
  }
  if (!body) return fallback;
  try {
    const parsed = JSON.parse(body);
    if (parsed?.error) return String(parsed.error);
  } catch {
    /* not JSON — fall through to the text itself */
  }
  // A proxy's error page is markup, not a sentence. Never put one in a
  // banner; the status line is more use than a wall of HTML.
  if (body.startsWith('<') || body.length > 300) return fallback;
  return body;
}

export async function apiGet<T>(path: string): Promise<T> {
  const r = await fetch(`${API_BASE}${path}`, { credentials: 'same-origin' });
  return parse<T>(r);
}

async function mutate<T>(
  method: 'POST' | 'PATCH' | 'PUT' | 'DELETE',
  path: string,
  body?: unknown,
): Promise<T> {
  const r = await fetch(`${API_BASE}${path}`, {
    method,
    credentials: 'same-origin',
    headers: {
      'Content-Type': 'application/json',
      [CSRF_HEADER]: '1',
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  return parse<T>(r);
}

export const apiPost = <T>(path: string, body?: unknown) =>
  mutate<T>('POST', path, body);
export const apiPatch = <T>(path: string, body?: unknown) =>
  mutate<T>('PATCH', path, body);
export const apiPut = <T>(path: string, body?: unknown) =>
  mutate<T>('PUT', path, body);
export const apiDelete = <T>(path: string, body?: unknown) =>
  mutate<T>('DELETE', path, body);
