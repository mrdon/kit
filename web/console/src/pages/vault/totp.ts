// TOTP (RFC 6238) for vault entries — a faithful TS port of the proven
// vault-totp.js. Browser-side only; the secret lives inside the encrypted
// entry blob and is never sent to the server.

const ALPHABET = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
const DEFAULTS = { algorithm: 'SHA1', digits: 6, period: 30 };
const VALID_ALGOS = new Set(['SHA1', 'SHA256', 'SHA512']);

export interface TotpParams {
  secret: string;
  algorithm: string;
  digits: number;
  period: number;
}

export function base32Decode(s: string): Uint8Array {
  const cleaned = s.replace(/\s+/g, '').replace(/=+$/g, '').toUpperCase();
  const out = new Uint8Array(Math.floor((cleaned.length * 5) / 8));
  let buffer = 0;
  let bits = 0;
  let byteIdx = 0;
  for (const ch of cleaned) {
    const v = ALPHABET.indexOf(ch);
    if (v < 0) throw new Error(`invalid base32 character: ${ch}`);
    buffer = (buffer << 5) | v;
    bits += 5;
    if (bits >= 8) {
      bits -= 8;
      out[byteIdx++] = (buffer >> bits) & 0xff;
    }
  }
  return out.subarray(0, byteIdx);
}

export function parseOtpauthURI(input: string): TotpParams | null {
  if (!input) return null;
  const trimmed = String(input).trim();
  if (!trimmed) return null;

  if (/^otpauth:\/\//i.test(trimmed)) {
    let url: URL;
    try {
      url = new URL(trimmed);
    } catch {
      return null;
    }
    if (url.host.toLowerCase() !== 'totp') return null;
    const secret = url.searchParams.get('secret');
    if (!secret) return null;
    const algorithm = (
      url.searchParams.get('algorithm') || DEFAULTS.algorithm
    ).toUpperCase();
    if (!VALID_ALGOS.has(algorithm)) return null;
    const digits = parseInt(url.searchParams.get('digits') || String(DEFAULTS.digits), 10);
    const period = parseInt(url.searchParams.get('period') || String(DEFAULTS.period), 10);
    if (!Number.isFinite(digits) || digits < 6 || digits > 10) return null;
    if (!Number.isFinite(period) || period < 1) return null;
    return validatedSecret({ secret, algorithm, digits, period });
  }
  return validatedSecret({ ...DEFAULTS, secret: trimmed });
}

function validatedSecret(params: TotpParams): TotpParams | null {
  try {
    const bytes = base32Decode(params.secret);
    if (bytes.length === 0) return null;
  } catch {
    return null;
  }
  return {
    secret: params.secret.replace(/\s+/g, '').toUpperCase(),
    algorithm: params.algorithm,
    digits: params.digits,
    period: params.period,
  };
}

export async function generateTOTP(
  { secret, algorithm, digits, period }: TotpParams,
  nowMs: number,
): Promise<{ code: string; remainingMs: number }> {
  const counter = Math.floor(nowMs / 1000 / period);
  const counterBytes = new Uint8Array(8);
  let c = counter;
  for (let i = 7; i >= 0; i--) {
    counterBytes[i] = c & 0xff;
    c = Math.floor(c / 256);
  }
  const keyBytes = base32Decode(secret);
  const key = await crypto.subtle.importKey(
    'raw',
    keyBytes as BufferSource,
    { name: 'HMAC', hash: { name: hashName(algorithm) } },
    false,
    ['sign'],
  );
  const sig = new Uint8Array(await crypto.subtle.sign('HMAC', key, counterBytes as BufferSource));
  const offset = sig[sig.length - 1] & 0x0f;
  const binary =
    ((sig[offset] & 0x7f) << 24) |
    (sig[offset + 1] << 16) |
    (sig[offset + 2] << 8) |
    sig[offset + 3];
  const code = String(binary % 10 ** digits).padStart(digits, '0');
  const remainingMs = period * 1000 - (nowMs - counter * period * 1000);
  return { code, remainingMs };
}

function hashName(algorithm: string): string {
  switch (algorithm) {
    case 'SHA1':
      return 'SHA-1';
    case 'SHA256':
      return 'SHA-256';
    case 'SHA512':
      return 'SHA-512';
    default:
      throw new Error(`unsupported TOTP algorithm: ${algorithm}`);
  }
}

// compactTOTP/expandTOTP mirror vault.js: store non-default fields only.
export function compactTOTP(t: TotpParams): Partial<TotpParams> {
  const out: Partial<TotpParams> = { secret: t.secret };
  if (t.algorithm !== DEFAULTS.algorithm) out.algorithm = t.algorithm;
  if (t.digits !== DEFAULTS.digits) out.digits = t.digits;
  if (t.period !== DEFAULTS.period) out.period = t.period;
  return out;
}

export function expandTOTP(t: Partial<TotpParams>): TotpParams {
  return {
    secret: t.secret as string,
    algorithm: t.algorithm || DEFAULTS.algorithm,
    digits: t.digits || DEFAULTS.digits,
    period: t.period || DEFAULTS.period,
  };
}
