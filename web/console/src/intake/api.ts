// Standalone API client for the PUBLIC expense-intake page. This entry is
// deliberately separate from the console's api.ts: it does NO session bootstrap
// (no /me, no login redirect) and derives the slug from the intake URL
// (/{slug}/expenses/submit) rather than from /{slug}/web.

const slugPattern = /^[a-z0-9][a-z0-9-]{0,62}$/;
const slug = location.pathname.split('/')[1] ?? '';
if (!slugPattern.test(slug)) {
  throw new Error(`Intake mounted under unexpected path: ${location.pathname}`);
}

export const SLUG = slug;
const BASE = `/${slug}/api/expenses/intake`;

export interface ScanResult {
  attachment_id: string;
  vendor: string;
  spent_on: string;
  amount: string;
  tax: string;
  currency: string;
}

export interface SubmitBody {
  email: string;
  name?: string;
  purpose?: string;
  vendor?: string;
  spent_on?: string;
  amount: string;
  tax?: string;
  category?: string;
  currency?: string;
  attachment_id: string;
  website?: string; // honeypot — always sent empty by real clients
}

async function fail(r: Response): Promise<never> {
  let msg = `Request failed (${r.status})`;
  try {
    const body = await r.json();
    if (body?.error) msg = body.error;
  } catch {
    /* non-JSON error body */
  }
  throw new Error(msg);
}

export async function scanReceipt(file: File): Promise<ScanResult> {
  const fd = new FormData();
  fd.append('receipt', file);
  const r = await fetch(`${BASE}/scan`, {
    method: 'POST',
    credentials: 'same-origin',
    body: fd,
  });
  if (!r.ok) return fail(r);
  return r.json();
}

export async function submitIntake(
  body: SubmitBody,
): Promise<{ ok: boolean; report_id: string }> {
  const r = await fetch(`${BASE}/submit`, {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (!r.ok) return fail(r);
  return r.json();
}
