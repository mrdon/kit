// TOTP (RFC 6238) for vault entries. Browser-side only — the secret
// lives inside the encrypted entry blob and is never sent to the
// server. Three exports:
//
//   parseOtpauthURI(input)  → {secret, algorithm, digits, period} | null
//   generateTOTP(params, nowMs) → {code, remainingMs}
//   base32Decode(s)         → Uint8Array
//
// vault.js already exceeds the 500-line cap; this module keeps the new
// code separate so that file doesn't grow further.

const ALPHABET = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
const DEFAULTS = { algorithm: "SHA1", digits: 6, period: 30 };
const VALID_ALGOS = new Set(["SHA1", "SHA256", "SHA512"]);

// base32Decode parses RFC 4648 base32 (no extensions, no checksum).
// Whitespace and '=' padding are ignored; case is folded to upper.
// Throws if any other character appears.
export function base32Decode(s) {
  const cleaned = s.replace(/\s+/g, "").replace(/=+$/g, "").toUpperCase();
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

// parseOtpauthURI accepts either an `otpauth://totp/...` URI or a raw
// base32 secret. Returns null if the input doesn't decode to anything
// usable (callers should silently skip invalid input). The label and
// issuer fields are intentionally dropped: the entry title already
// names the account.
export function parseOtpauthURI(input) {
  if (!input) return null;
  const trimmed = String(input).trim();
  if (!trimmed) return null;

  if (/^otpauth:\/\//i.test(trimmed)) {
    let url;
    try {
      url = new URL(trimmed);
    } catch {
      return null;
    }
    if (url.host.toLowerCase() !== "totp") return null;
    const secret = url.searchParams.get("secret");
    if (!secret) return null;
    const algorithm = (url.searchParams.get("algorithm") || DEFAULTS.algorithm).toUpperCase();
    if (!VALID_ALGOS.has(algorithm)) return null;
    const digits = parseInt(url.searchParams.get("digits") || DEFAULTS.digits, 10);
    const period = parseInt(url.searchParams.get("period") || DEFAULTS.period, 10);
    if (!Number.isFinite(digits) || digits < 6 || digits > 10) return null;
    if (!Number.isFinite(period) || period < 1) return null;
    return validatedSecret({ secret, algorithm, digits, period });
  }

  // Raw base32 — apply defaults.
  return validatedSecret({ ...DEFAULTS, secret: trimmed });
}

function validatedSecret(params) {
  try {
    const bytes = base32Decode(params.secret);
    if (bytes.length === 0) return null;
  } catch {
    return null;
  }
  return {
    secret: params.secret.replace(/\s+/g, "").toUpperCase(),
    algorithm: params.algorithm,
    digits: params.digits,
    period: params.period,
  };
}

// generateTOTP returns {code, remainingMs} where code is a digit
// string padded to `digits` and remainingMs is how long until the
// counter rolls. Standard RFC 6238 dynamic-truncation HOTP underneath.
export async function generateTOTP({ secret, algorithm, digits, period }, nowMs) {
  const counter = Math.floor(nowMs / 1000 / period);
  const counterBytes = new Uint8Array(8);
  // 8-byte big-endian counter; JS numbers handle up to 2^53 safely
  // which covers ~285 million years at period=30, so the high 21 bits
  // we drop on the shift below are always zero in practice.
  let c = counter;
  for (let i = 7; i >= 0; i--) {
    counterBytes[i] = c & 0xff;
    c = Math.floor(c / 256);
  }
  const keyBytes = base32Decode(secret);
  const key = await crypto.subtle.importKey(
    "raw",
    keyBytes,
    { name: "HMAC", hash: { name: hashName(algorithm) } },
    false,
    ["sign"],
  );
  const sig = new Uint8Array(await crypto.subtle.sign("HMAC", key, counterBytes));
  const offset = sig[sig.length - 1] & 0x0f;
  const binary =
    ((sig[offset] & 0x7f) << 24) |
    (sig[offset + 1] << 16) |
    (sig[offset + 2] << 8) |
    sig[offset + 3];
  const code = String(binary % 10 ** digits).padStart(digits, "0");
  const remainingMs = period * 1000 - (nowMs - counter * period * 1000);
  return { code, remainingMs };
}

function hashName(algorithm) {
  switch (algorithm) {
    case "SHA1": return "SHA-1";
    case "SHA256": return "SHA-256";
    case "SHA512": return "SHA-512";
    default: throw new Error(`unsupported TOTP algorithm: ${algorithm}`);
  }
}
