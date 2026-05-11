// SharedWorker that holds the unwrapped vault_key for the lifetime of any
// open vault tab.
//
// Threat model: all vault tabs from the same origin connect to this same
// SharedWorker instance. The CryptoKey lives in the worker's scope only;
// the page interacts via postMessage and the protocol exposes encrypt /
// decrypt but **no export-raw operation**. `set_key` is one-shot per
// worker instance: subsequent calls fail until an explicit `lock` clears
// the captive key. This blocks the chosen-key-oracle attack where XSS
// swaps the key mid-session.
//
// vault_generation: every API response includes the tenant's current
// vault_generation. If the worker sees a higher generation than what was
// active when it cached its key, it locks immediately — that means a
// teammate rotated the password and our cached key is no longer valid.
//
// Cross-tab sync uses BroadcastChannel('kit-vault-lock'):
//   - 'unlocked' when a tab successfully unlocks
//   - 'locked' when any tab triggers manual / idle / tab-close / rotate lock
//
// Idle-lock: the worker times out after IDLE_MS of no activity, or
// ABSOLUTE_MS of total uptime, whichever comes first.
//
// In-flight sentinel: an explicit lock waits up to DRAIN_TIMEOUT_MS for
// outstanding crypto operations to drain before clearing the key.

const STATE = {
  vaultKey: null,
  vaultGeneration: 0,
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
    STATE.vaultGeneration = 0;
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
        await setKey(msg.rawKey, msg.vaultGeneration | 0);
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
      case "check_generation":
        // Page just observed a vault_generation in an API response;
        // we drop our cached key if it's stale.
        if (STATE.vaultKey !== null && (msg.vaultGeneration | 0) > STATE.vaultGeneration) {
          await lockNow();
        }
        port.postMessage({ id: msg.id, ok: true });
        break;
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

async function setKey(rawKey, vaultGeneration) {
  // One-shot per worker lifetime. An attacker who lands on the page
  // could otherwise call set_key with their own AES key after the
  // legitimate unlock, turning subsequent encrypts into a chosen-key
  // oracle. Refuse the swap; the page must call `lock` first.
  if (STATE.vaultKey !== null) {
    throw new Error("vault already unlocked; call lock first to re-key");
  }
  STATE.vaultKey = await crypto.subtle.importKey(
    "raw",
    rawKey,
    { name: "AES-GCM" },
    false,
    ["encrypt", "decrypt"],
  );
  STATE.vaultGeneration = vaultGeneration | 0;
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

async function lockNow() {
  // Wait up to DRAIN_TIMEOUT_MS for in-flight operations to drain so
  // pending encrypt promises don't resolve into POSTs after lock.
  const deadline = Date.now() + DRAIN_TIMEOUT_MS;
  while (STATE.inFlight > 0 && Date.now() < deadline) {
    await new Promise((r) => setTimeout(r, 50));
  }
  STATE.vaultKey = null;
  STATE.vaultGeneration = 0;
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
