// SharedWorker that holds the unwrapped vault_key for the lifetime of any
// open vault tab.
//
// Threat model: all vault tabs from the same origin connect to this same
// SharedWorker instance. The CryptoKey lives in the worker's scope only;
// the page interacts via postMessage and the protocol exposes encrypt /
// decrypt / wrap but **no export-raw operation**. The worker also
// **does not trust the page** for security-sensitive inputs:
//
//   - `wrap_for` takes only a target user_id; the worker fetches the
//     target's pubkey *itself* via the same-origin API. This blocks the
//     XSS-supplies-attacker-pubkey attack — even an XSS that hijacks
//     `window.fetch` can't intercept the worker's own fetch context.
//   - `set_key` is one-shot per worker instance: subsequent calls fail
//     until an explicit `lock` clears the captive key. This blocks the
//     chosen-key-oracle attack where XSS swaps the key mid-session.
//
// Cross-tab sync uses BroadcastChannel('kit-vault-lock'):
//   - 'unlocked' when a tab successfully unlocks (other tabs ask the
//     worker for the cached key state)
//   - 'locked' when any tab triggers manual / idle / tab-close lock
//     (other tabs wipe their UI state)
//
// Idle-lock: the worker times out after IDLE_MS of no activity, or
// ABSOLUTE_MS of total uptime, whichever comes first. On timeout it nulls
// its key reference and broadcasts 'locked'.
//
// In-flight sentinel: an explicit lock waits up to DRAIN_TIMEOUT_MS for
// outstanding crypto operations to drain before clearing the key — so an
// encrypt() Promise can't resolve into a POST after the user thinks they
// locked.

const STATE = {
  vaultKey: null,
  lastActivity: 0,
  unlockedAt: 0,
  inFlight: 0,
};

const IDLE_MS = 10 * 60_000;     // 10 min idle
const ABSOLUTE_MS = 30 * 60_000; // 30 min absolute
const DRAIN_TIMEOUT_MS = 2_000;
const POLL_MS = 30_000;

const broadcast = new BroadcastChannel("kit-vault-lock");

// Other tabs broadcasting 'locked' should make us drop too.
broadcast.onmessage = (ev) => {
  if (ev.data && ev.data.type === "locked") {
    // Don't loop the broadcast; just clear local state.
    STATE.vaultKey = null;
    STATE.unlockedAt = 0;
    STATE.lastActivity = 0;
  }
};

self.onconnect = (ev) => {
  const port = ev.ports[0];
  port.onmessage = (msg) => handle(port, msg.data);
  port.start();
};

async function handle(port, msg) {
  try {
    switch (msg.type) {
      case "set_key":
        await setKey(msg.rawKey);
        port.postMessage({ id: msg.id, ok: true });
        break;
      case "has_key":
        port.postMessage({ id: msg.id, ok: true, result: STATE.vaultKey !== null });
        break;
      case "encrypt": {
        const out = await encrypt(msg.plaintext);
        port.postMessage({ id: msg.id, ok: true, result: out });
        break;
      }
      case "decrypt": {
        const out = await decrypt(msg.ciphertext, msg.nonce);
        port.postMessage({ id: msg.id, ok: true, result: out });
        break;
      }
      case "wrap_for": {
        // Page passes only the target user_id. Worker fetches the
        // target's pubkey itself so an XSS-controlled page can't swap
        // in an attacker-controlled key. msg.apiBase is allowed only
        // because it varies by tenant slug; we sanity-check it
        // matches the worker's own origin.
        const out = await wrapForRecipient(msg.targetUserID, msg.apiBase);
        port.postMessage({ id: msg.id, ok: true, result: out });
        break;
      }
      case "lock":
        await lockNow();
        port.postMessage({ id: msg.id, ok: true });
        break;
      default:
        port.postMessage({ id: msg.id, ok: false, error: `unknown type: ${msg.type}` });
    }
  } catch (err) {
    port.postMessage({ id: msg.id, ok: false, error: String((err && err.message) || err) });
  }
}

async function setKey(rawKey) {
  // One-shot per worker lifetime. An attacker who lands on the page
  // could otherwise call set_key with their own AES key after the
  // legitimate unlock, turning subsequent encrypts into a chosen-key
  // oracle. Refuse the swap; the page must call `lock` first.
  if (STATE.vaultKey !== null) {
    throw new Error("vault already unlocked; call lock first to re-key");
  }
  // Extractable inside the worker so we can wrapKey at grant time. The
  // protocol does not expose any "export raw" message — extractability
  // is irrelevant outside the worker.
  STATE.vaultKey = await crypto.subtle.importKey(
    "raw",
    rawKey,
    { name: "AES-GCM" },
    true, // extractable (worker-internal only)
    ["encrypt", "decrypt", "wrapKey"],
  );
  STATE.unlockedAt = Date.now();
  STATE.lastActivity = Date.now();
  broadcast.postMessage({ type: "unlocked" });
  scheduleIdleCheck();
}

async function encrypt(plaintext) {
  if (!STATE.vaultKey) throw new Error("vault locked");
  STATE.inFlight++;
  STATE.lastActivity = Date.now();
  try {
    const nonce = crypto.getRandomValues(new Uint8Array(12));
    const ct = await crypto.subtle.encrypt({ name: "AES-GCM", iv: nonce }, STATE.vaultKey, plaintext);
    return { ciphertext: new Uint8Array(ct), nonce };
  } finally {
    STATE.inFlight--;
  }
}

async function decrypt(ciphertext, nonce) {
  if (!STATE.vaultKey) throw new Error("vault locked");
  STATE.inFlight++;
  STATE.lastActivity = Date.now();
  try {
    const pt = await crypto.subtle.decrypt({ name: "AES-GCM", iv: nonce }, STATE.vaultKey, ciphertext);
    return new Uint8Array(pt);
  } finally {
    STATE.inFlight--;
  }
}

async function wrapForRecipient(targetUserID, apiBase) {
  if (!STATE.vaultKey) throw new Error("vault locked");
  if (typeof targetUserID !== "string" || targetUserID === "") {
    throw new Error("target user id required");
  }
  if (typeof apiBase !== "string" || !apiBase.startsWith("/")) {
    throw new Error("api base required");
  }
  // Sanity-check apiBase shape: tenant-prefixed relative path that ends
  // with /apps/vault/api. Reject anything else so a hijacked page can't
  // point us at attacker-controlled fetches.
  if (!/^\/[A-Za-z0-9_-]+\/apps\/vault\/api$/.test(apiBase)) {
    throw new Error("api base shape invalid");
  }
  // URL-encode the target id segment to defuse path-injection.
  const url = `${apiBase}/users/${encodeURIComponent(targetUserID)}`;
  STATE.inFlight++;
  STATE.lastActivity = Date.now();
  try {
    // Worker's own fetch — independent of any page-side fetch hooking.
    // The session cookie rides along automatically (same-origin).
    const resp = await fetch(url, {
      credentials: "same-origin",
      headers: { "X-Kit-Vault": "1" },
    });
    if (!resp.ok) {
      throw new Error(`fetch target user: HTTP ${resp.status}`);
    }
    const target = await resp.json();
    if (!target || typeof target.public_key !== "string") {
      throw new Error("target user has no public key");
    }
    const rsaSpki = b64ToBytes(target.public_key);
    const recipient = await crypto.subtle.importKey(
      "spki",
      rsaSpki,
      { name: "RSA-OAEP", hash: "SHA-256" },
      false,
      ["wrapKey"],
    );
    const wrapped = await crypto.subtle.wrapKey("raw", STATE.vaultKey, recipient, { name: "RSA-OAEP" });
    return new Uint8Array(wrapped);
  } finally {
    STATE.inFlight--;
  }
}

// b64ToBytes — local copy so the worker doesn't share code with the
// page. Same implementation as vault.js.
function b64ToBytes(s) {
  const bin = atob(s);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

async function lockNow() {
  // Wait up to DRAIN_TIMEOUT_MS for in-flight operations to drain so
  // pending encrypt promises don't resolve into POSTs after lock.
  const deadline = Date.now() + DRAIN_TIMEOUT_MS;
  while (STATE.inFlight > 0 && Date.now() < deadline) {
    await new Promise((r) => setTimeout(r, 50));
  }
  STATE.vaultKey = null;
  STATE.unlockedAt = 0;
  STATE.lastActivity = 0;
  broadcast.postMessage({ type: "locked" });
}

let idleTimer = null;
function scheduleIdleCheck() {
  if (idleTimer) clearTimeout(idleTimer);
  idleTimer = setTimeout(idleCheck, POLL_MS);
}

async function idleCheck() {
  if (!STATE.vaultKey) return;
  const now = Date.now();
  if (now - STATE.lastActivity > IDLE_MS || now - STATE.unlockedAt > ABSOLUTE_MS) {
    await lockNow();
    return;
  }
  scheduleIdleCheck();
}
