// Vault browser-side crypto — v1.5 implementation.
//
// Architecture:
//   - SharedWorker (vault-worker.js) holds the unwrapped vault_key for
//     the lifetime of any open vault tab. Encrypt/decrypt/wrap operations
//     proxy through it via postMessage. The worker boundary is the
//     XSS-resistance defense: the postMessage protocol exposes
//     encrypt/decrypt/wrap but no export-raw operation, so an XSS
//     anywhere in Kit cannot exfiltrate the raw key.
//   - IndexedDB stores the wrapped private key, kdf params, and the
//     wrapped vault key after first unlock. Subsequent unlocks on the
//     same browser skip the round trip and use the cached blobs.
//   - BroadcastChannel('kit-vault-lock') syncs lock/unlock across tabs.
//   - Idle-lock fires from the worker after 10 min idle / 30 min total.
//   - Page-side hooks: visibilitychange + beforeunload trigger lock().
//
// Crypto pinned for v1.5:
//   - KDF:        PBKDF2-SHA256 / 600,000 iterations / 32-byte output
//   - Master key splitting:
//                 master_key = PBKDF2(password, kdf_salt)
//                 enc_key    = HKDF(master_key, salt, info="kit-vault-v1-enc")
//                 auth_hash  = HKDF(master_key, salt, info="kit-vault-v1-auth")
//   - Keypair:    RSA-OAEP-2048 / SHA-256
//   - Symmetric:  AES-GCM / 12-byte random nonce per encryption
//
// Argon2id is the v2 KDF target; vendoring requires a CDN fetch
// authorized by the user. Tracked in vault_test (and in the plan's
// Out-of-scope section).

import { parseOtpauthURI, generateTOTP } from "./vault-totp.js";

const TOTP_DEFAULTS = { algorithm: "SHA1", digits: 6, period: 30 };

// Persist only non-default optional fields so the encrypted blob stays
// small for the common case (SHA1 / 6 digits / 30s).
function compactTOTP(t) {
  const out = { secret: t.secret };
  if (t.algorithm !== TOTP_DEFAULTS.algorithm) out.algorithm = t.algorithm;
  if (t.digits !== TOTP_DEFAULTS.digits) out.digits = t.digits;
  if (t.period !== TOTP_DEFAULTS.period) out.period = t.period;
  return out;
}

function expandTOTP(t) {
  return {
    secret: t.secret,
    algorithm: t.algorithm || TOTP_DEFAULTS.algorithm,
    digits: t.digits || TOTP_DEFAULTS.digits,
    period: t.period || TOTP_DEFAULTS.period,
  };
}

const VAULT = (() => {
  const root = document.getElementById("vault-app");
  if (!root) return null;
  return {
    page: root.dataset.page,
    apiBase: root.dataset.apiBase,
    staticBase: root.dataset.staticBase,
    tenantSlug: root.dataset.tenantSlug,
    entryId: root.dataset.entryId || "",
    targetUserId: root.dataset.targetUserId || "",
  };
})();

async function main() {
  if (!window.isSecureContext) {
    setStatus("This page requires HTTPS (or localhost).", "error");
    return;
  }
  await connectWorker();
  installLockHooks();
  // Pages that require a registered vault user redirect to /register
  // when /api/me 404s (no vault_users row). Skip on register / cancel-
  // reset / forgot — those pages are valid regardless of registration
  // state (forgot exists precisely because the user can't unlock).
  if (VAULT.page !== "register" && VAULT.page !== "cancel-reset" && VAULT.page !== "forgot") {
    if (!(await ensureRegistered())) return;
  }
  switch (VAULT.page) {
    case "register":     return wireRegister();
    case "add":          return wireAdd();
    case "reveal":       return wireReveal();
    case "grant":        return wireGrant();
    case "cancel-reset": return wireCancelReset();
    case "forgot":       return wireForgot();
    default: setStatus(`Unknown vault page: ${VAULT.page}`, "error");
  }
}

// ensureRegistered redirects to /{slug}/apps/vault/register when the
// caller has no vault_users row yet. Returns false (caller should
// stop) when redirecting; true when registered. Preserves the
// current URL as ?return_to so register can bounce back.
async function ensureRegistered() {
  try {
    const me = await api("GET", "/me");
    if (me) return true;
  } catch (err) {
    if (!String(err.message || err).includes("HTTP 404")) {
      setStatus(`Error: ${err.message || err}`, "error");
      return false;
    }
    // 404 means no vault_users row — fall through to redirect.
  }
  const returnTo = encodeURIComponent(location.pathname + location.search);
  location.replace(`/${VAULT.tenantSlug}/apps/vault/register?return_to=${returnTo}`);
  return false;
}

// ===== KDF + key derivation =====
//
// v1 ships PBKDF2-SHA256 / 600k iterations: OWASP 2024 floor for
// acceptable. The plan's pinned target is Argon2id (m=64MiB, t=3, p=1)
// for memory-hardness against GPU/ASIC offline brute force; PBKDF2 is
// GPU-friendly. Upgrade trigger: real-tenant rollout, compliance ask,
// or any DB-leak incident in the Kit stack.
//
// kdf_params is per-user jsonb so the upgrade is non-disruptive: new
// users get Argon2id once shipped; existing users rotate via a future
// "change KDF" flow (enter master password → derive both old + new →
// server checks old auth_hash → accepts new auth_hash + re-wrapped
// private key, no teammate re-grant required because vault_key
// wrapping doesn't change). See plan §"Crypto primitives" for the
// full transition plan.
//
// TODO(v1.5): vendor @noble/hashes argon2id, switch this constant to
// the Argon2id params block, add /api/rotate_kdf endpoint.
const KDF_ITERATIONS = 600_000;
const KDF_HASH = "SHA-256";

async function pbkdf2(password, salt) {
  const baseKey = await crypto.subtle.importKey(
    "raw", new TextEncoder().encode(password), { name: "PBKDF2" }, false, ["deriveBits"],
  );
  const bits = await crypto.subtle.deriveBits(
    { name: "PBKDF2", hash: KDF_HASH, salt, iterations: KDF_ITERATIONS },
    baseKey, 256,
  );
  return new Uint8Array(bits);
}

async function hkdf(masterKey, salt, info) {
  const baseKey = await crypto.subtle.importKey(
    "raw", masterKey, { name: "HKDF" }, false, ["deriveBits"],
  );
  const bits = await crypto.subtle.deriveBits(
    { name: "HKDF", hash: KDF_HASH, salt, info: new TextEncoder().encode(info) },
    baseKey, 256,
  );
  return new Uint8Array(bits);
}

async function deriveKeys(password, salt) {
  const masterKey = await pbkdf2(password, salt);
  const encKeyBytes = await hkdf(masterKey, salt, "kit-vault-v1-enc");
  const authHash = await hkdf(masterKey, salt, "kit-vault-v1-auth");
  const encKey = await crypto.subtle.importKey(
    "raw", encKeyBytes, { name: "AES-GCM" }, false, ["encrypt", "decrypt"],
  );
  return { encKey, authHash };
}

// ===== AES-GCM helpers (page-side, only for the wrapped private key) =====

async function aesGcmEncrypt(key, plaintext) {
  const nonce = crypto.getRandomValues(new Uint8Array(12));
  const ciphertext = await crypto.subtle.encrypt({ name: "AES-GCM", iv: nonce }, key, plaintext);
  return { ciphertext: new Uint8Array(ciphertext), nonce };
}

async function aesGcmDecrypt(key, ciphertext, nonce) {
  const plaintext = await crypto.subtle.decrypt({ name: "AES-GCM", iv: nonce }, key, ciphertext);
  return new Uint8Array(plaintext);
}

// ===== RSA-OAEP =====

async function generateRSAKeypair() {
  return crypto.subtle.generateKey(
    { name: "RSA-OAEP", modulusLength: 2048, publicExponent: new Uint8Array([0x01, 0x00, 0x01]), hash: "SHA-256" },
    true,
    ["wrapKey", "unwrapKey", "encrypt", "decrypt"],
  );
}
async function exportSpki(pubKey) { return new Uint8Array(await crypto.subtle.exportKey("spki", pubKey)); }
async function exportPkcs8(privKey) { return new Uint8Array(await crypto.subtle.exportKey("pkcs8", privKey)); }
async function importPkcs8(pkcs8) {
  return crypto.subtle.importKey(
    "pkcs8", pkcs8, { name: "RSA-OAEP", hash: "SHA-256" }, false, ["unwrapKey", "decrypt"],
  );
}
// rsaWrapAesKey: page-side wrap, used at register time when we have the
// raw vault_key bytes locally before we hand them to the worker.
async function rsaWrapAesKey(rsaPubKey, rawVaultKey) {
  const wrapped = await crypto.subtle.encrypt({ name: "RSA-OAEP" }, rsaPubKey, rawVaultKey);
  return new Uint8Array(wrapped);
}
async function rsaUnwrapAesKey(rsaPrivKey, wrapped) {
  const raw = await crypto.subtle.decrypt({ name: "RSA-OAEP" }, rsaPrivKey, wrapped);
  return new Uint8Array(raw);
}

// ===== fetch + base64 =====

async function api(method, path, body) {
  // X-Kit-Vault on every state-changing request; Content-Type only when
  // there's a body. Server enforces both as the CSRF gate.
  const headers = {};
  if (method !== "GET") headers["X-Kit-Vault"] = "1";
  if (body) headers["Content-Type"] = "application/json";

  const res = await fetch(`${VAULT.apiBase}${path}`, {
    method,
    credentials: "same-origin",
    headers,
    body: body ? JSON.stringify(body) : undefined,
  });
  if (!res.ok) {
    const text = await res.text().catch(() => res.statusText);
    throw new Error(`HTTP ${res.status}: ${text.trim() || res.statusText}`);
  }
  if (res.status === 204) return null;
  const ct = res.headers.get("Content-Type") || "";
  if (ct.includes("application/json")) return res.json();
  return res.text();
}

function bytesToB64(bytes) {
  let bin = "";
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin);
}
function b64ToBytes(s) {
  const bin = atob(s);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}
const bytesField = bytesToB64;

// ===== SharedWorker proxy =====

let workerPort = null;
let nextMsgID = 1;
const pending = new Map();

async function connectWorker() {
  const w = new SharedWorker(`${VAULT.staticBase}/vault-worker.js`, { type: "module", name: "kit-vault" });
  workerPort = w.port;
  workerPort.onmessage = (ev) => {
    const { id, ok, result, error } = ev.data || {};
    const p = pending.get(id);
    if (!p) return;
    pending.delete(id);
    ok ? p.resolve(result) : p.reject(new Error(error || "worker error"));
  };
  workerPort.start();
}

function workerCall(type, payload) {
  return new Promise((resolve, reject) => {
    const id = nextMsgID++;
    pending.set(id, { resolve, reject });
    workerPort.postMessage({ id, type, ...(payload || {}) });
  });
}

// ===== IndexedDB persistence =====
//
// One object store, key='self', that holds:
//   { kdfParams, wrappedPrivateKey: {ciphertext, nonce}, wrappedVaultKey }
// vault_key is never persisted in unwrapped form.

const DB_NAME = "kit-vault";
const DB_STORE = "self";

// dbBusy serializes all IDB operations through a single promise chain.
// Without it, a concurrent dbPut + dbWipe race could leave wrapped key
// material on disk after a "lock" event. Each operation appends to the
// chain; the chain catches errors so one failure doesn't poison
// subsequent operations.
let dbBusy = Promise.resolve();
function dbSerial(fn) {
  const next = dbBusy.then(fn, fn);
  // Don't surface the prior error to the next caller.
  dbBusy = next.catch(() => {});
  return next;
}

function openDB() {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open(DB_NAME, 1);
    req.onupgradeneeded = () => {
      req.result.createObjectStore(DB_STORE);
    };
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
}

async function dbPut(value) {
  return dbSerial(async () => {
    const db = await openDB();
    try {
      await new Promise((resolve, reject) => {
        const tx = db.transaction(DB_STORE, "readwrite");
        tx.objectStore(DB_STORE).put(value, "self");
        tx.oncomplete = () => resolve();
        tx.onerror = () => reject(tx.error);
      });
    } finally {
      db.close();
    }
  });
}

async function dbGet() {
  return dbSerial(async () => {
    const db = await openDB();
    try {
      return await new Promise((resolve, reject) => {
        const tx = db.transaction(DB_STORE, "readonly");
        const r = tx.objectStore(DB_STORE).get("self");
        r.onsuccess = () => resolve(r.result || null);
        r.onerror = () => reject(r.error);
      });
    } finally {
      db.close();
    }
  });
}

// dbWipe removes the entire vault DB. On `onblocked` (another open
// connection holds the DB) we log + retry once after a short delay; if
// it still fails we surface the failure so callers can react instead
// of silently leaving wrapped key material on disk. Plan §"Lock /
// wipe" requires the wipe to actually happen on every lock.
async function dbWipe() {
  return dbSerial(async () => {
    for (let attempt = 0; attempt < 2; attempt++) {
      const ok = await deleteOnce();
      if (ok) return;
      console.warn("vault: IDB wipe blocked; retrying");
      await new Promise((r) => setTimeout(r, 100));
    }
    console.error("vault: IDB wipe failed after retries; wrapped key material may persist");
    throw new Error("idb wipe failed");
  });
}

function deleteOnce() {
  return new Promise((resolve) => {
    const r = indexedDB.deleteDatabase(DB_NAME);
    r.onsuccess = () => resolve(true);
    r.onerror = () => resolve(false);
    r.onblocked = () => resolve(false);
  });
}

// ===== BroadcastChannel cross-tab sync =====

const lockChannel = new BroadcastChannel("kit-vault-lock");
lockChannel.onmessage = (ev) => {
  if (ev.data && ev.data.type === "locked") {
    onLockedExternally();
  }
};

function onLockedExternally() {
  // Worker locked (idle, manual, or from another tab). Wipe UI state +
  // IDB material so an XSS post-lock can't drain the wrapped private
  // key + auth_hash for offline brute-force. Re-unlock will re-fetch
  // the wrapped material from /api/me.
  stopTOTPRender();
  hideSection("reveal-area");
  hideSection("add-form");
  hideSection("grant-area");
  showSection("unlock-prompt");
  // Best-effort IDB wipe; ignore errors so a broken IDB doesn't block
  // the UX clear.
  dbWipe().catch(() => {});
  setStatus("Vault locked.", "");
}

async function lockNow() {
  try { await workerCall("lock"); } catch {}
  // Lock = full wipe. Plan §"Lock / wipe" specifies IndexedDB cache
  // wiped on every lock event; the marginal cost is one extra GET /me
  // round-trip on next unlock, which is fine.
  try { await dbWipe(); } catch {}
}

// ===== page-side lock hooks =====

function installLockHooks() {
  // Lock when the tab is hidden long enough or on tab close — defense
  // against a stale tab leaking the cached key.
  let hiddenAt = 0;
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "hidden") {
      hiddenAt = Date.now();
    } else if (hiddenAt && Date.now() - hiddenAt > 5 * 60_000) {
      // Returning after 5+ min hidden: lock.
      lockNow();
      hiddenAt = 0;
    }
  });
  // pagehide fires reliably on mobile + bfcache navigations where
  // beforeunload is unreliable. Both fire the lock; the worker drains
  // its own in-flight ops, and we kick off an IDB wipe even though the
  // tab may close before it completes.
  const onClose = () => {
    stopTOTPRender();
    if (workerPort) workerPort.postMessage({ id: nextMsgID++, type: "lock" });
    // Best-effort sync wipe trigger — browsers limit work in unload
    // handlers but this gives the wipe its first event-loop tick before
    // the page dies.
    dbWipe().catch(() => {});
  };
  window.addEventListener("pagehide", onClose);
  window.addEventListener("beforeunload", onClose);
}

// ===== unlock flow =====

async function unlock(password) {
  // If another tab already unlocked this SharedWorker, we don't need
  // to re-derive — and the worker's set_key is one-shot anyway.
  if (await workerCall("has_key")) return;

  // Try IndexedDB cache first — round-trip to /me only on miss.
  let cached = await dbGet();
  let kdfParams, wrappedPriv, wrappedVaultKey;
  if (cached) {
    kdfParams = cached.kdfParams;
    wrappedPriv = cached.wrappedPriv;
    wrappedVaultKey = cached.wrappedVaultKey;
  } else {
    const me = await api("GET", "/me");
    if (!me || !me.kdf_params || !me.kdf_params.salt) {
      throw new Error("No vault registration found on this account. Open /register first.");
    }
    kdfParams = me.kdf_params;
  }

  const salt = b64ToBytes(kdfParams.salt);
  const { encKey, authHash } = await deriveKeys(password, salt);

  // Always POST /unlock so the server can rate-limit + audit and so
  // we get the latest wrapped_vault_key (in case it was re-granted
  // after a reset).
  const resp = await api("POST", "/unlock", { auth_hash: bytesField(authHash) });

  // Decrypt the user's RSA private key with enc_key.
  const privCT = b64ToBytes(resp.user_private_key_ciphertext);
  const privNonce = b64ToBytes(resp.user_private_key_nonce);
  const pkcs8 = await aesGcmDecrypt(encKey, privCT, privNonce);
  const rsaPriv = await importPkcs8(pkcs8);

  // RSA-unwrap the vault_key.
  const wrappedVK = b64ToBytes(resp.wrapped_vault_key);
  const rawVaultKey = await rsaUnwrapAesKey(rsaPriv, wrappedVK);

  // Hand to the worker. The worker imports as a CryptoKey; we zero
  // the page-side buffer right after so the raw bytes don't linger in
  // page memory longer than necessary.
  await workerCall("set_key", { rawKey: rawVaultKey.buffer });
  rawVaultKey.fill(0);

  // Persist the wrapped state for future unlocks on this device.
  await dbPut({
    kdfParams,
    wrappedPriv: { ciphertext: bytesToB64(privCT), nonce: bytesToB64(privNonce) },
    wrappedVaultKey: bytesToB64(wrappedVK),
  });
}

async function ensureUnlocked() {
  const ok = await workerCall("has_key");
  if (ok) return;
  showSection("unlock-prompt");
  hideSection("add-form");
  hideSection("reveal-area");
  hideSection("grant-area");
  return new Promise((resolve, reject) => {
    const form = document.getElementById("unlock-form");
    if (!form) return reject(new Error("no unlock form on this page"));
    form.addEventListener("submit", async (ev) => {
      ev.preventDefault();
      const pw = new FormData(form).get("master_password");
      try {
        await unlock(pw);
        hideSection("unlock-prompt");
        resolve();
      } catch (err) {
        const status = document.getElementById("unlock-status");
        if (status) {
          status.textContent = err.message || String(err);
          status.className = "error";
        }
        reject(err);
      }
    }, { once: true });
  });
}

// ===== page wires =====

async function wireRegister() {
  const form = document.getElementById("register-form");
  if (!form) return;
  form.addEventListener("submit", async (ev) => {
    ev.preventDefault();
    setStatus("Generating keys…");

    const data = new FormData(form);
    const pw = data.get("master_password");
    if (pw !== data.get("master_password_confirm")) {
      setStatus("Passwords don't match.", "error");
      return;
    }
    if (typeof pw !== "string" || pw.length < 14) {
      setStatus("Master password must be at least 14 characters.", "error");
      return;
    }

    // If the user is retrying after a canary failure (or another tab
    // unlocked first), the worker's set_key is one-shot per lifetime.
    // Lock first so the new register's set_key call succeeds.
    if (await workerCall("has_key")) {
      await workerCall("lock");
    }

    const salt = crypto.getRandomValues(new Uint8Array(16));
    const kdfParams = {
      algo: "pbkdf2-sha256",
      iterations: KDF_ITERATIONS,
      salt: bytesToB64(salt),
    };
    const { encKey, authHash } = await deriveKeys(pw, salt);

    setStatus("Generating RSA keypair…");
    const rsa = await generateRSAKeypair();
    const spki = await exportSpki(rsa.publicKey);
    const pkcs8 = await exportPkcs8(rsa.privateKey);
    const wrappedPriv = await aesGcmEncrypt(encKey, pkcs8);

    let wrappedVaultKey = null;
    let body = {
      auth_hash: bytesField(authHash),
      kdf_params: kdfParams,
      user_public_key: bytesField(spki),
      user_private_key_ciphertext: bytesField(wrappedPriv.ciphertext),
      user_private_key_nonce: bytesField(wrappedPriv.nonce),
    };

    setStatus("Registering with server…");
    let res;
    try {
      res = await api("POST", "/register", body);
    } catch (err) {
      if ((err.message || "").includes("first user in tenant must self-grant")) {
        // Bootstrap: generate vault_key, wrap with own pubkey, retry.
        const rawVK = crypto.getRandomValues(new Uint8Array(32));
        wrappedVaultKey = await rsaWrapAesKey(rsa.publicKey, rawVK);
        body.wrapped_vault_key = bytesField(wrappedVaultKey);
        res = await api("POST", "/register", body);
        // Hand the raw key to the worker so this admin is unlocked,
        // then zero the page-side buffer.
        await workerCall("set_key", { rawKey: rawVK.buffer });
        rawVK.fill(0);
      } else {
        throw err;
      }
    }

    // Real self-unlock canary: re-decrypt the wrapped private key using
    // enc_key and round-trip a small plaintext through AES-GCM. If any
    // part fails (corrupt ciphertext, wrong key, etc.) we abort *before*
    // POSTing /self_unlock_test, so the server keeps pending=true and
    // the user can re-register without a 24h reset cooldown.
    {
      const reReadPriv = await aesGcmDecrypt(encKey, wrappedPriv.ciphertext, wrappedPriv.nonce);
      const reImported = await importPkcs8(reReadPriv);
      if (wrappedVaultKey) {
        // Bootstrap admin: the unwrap path is RSA-OAEP(wrapped, priv).
        const unwrapped = await rsaUnwrapAesKey(reImported, wrappedVaultKey);
        if (unwrapped.length !== 32) throw new Error("vault key length wrong");
      }
      // AES round-trip: encrypt + decrypt a known canary using enc_key.
      const canary = new TextEncoder().encode("kit-vault-canary");
      const encCanary = await aesGcmEncrypt(encKey, canary);
      const decCanary = await aesGcmDecrypt(encKey, encCanary.ciphertext, encCanary.nonce);
      if (new TextDecoder().decode(decCanary) !== "kit-vault-canary") {
        throw new Error("canary round-trip mismatch");
      }
    }
    // Server flips pending=false only after this final auth_hash check.
    await api("POST", "/self_unlock_test", { auth_hash: bytesField(authHash) });

    // Persist for next-device unlock (only the bootstrap admin has a
    // wrapped vault key right now; non-bootstrap users get one after
    // grant and persist on first unlock).
    if (wrappedVaultKey) {
      await dbPut({
        kdfParams,
        wrappedPriv: {
          ciphertext: bytesToB64(wrappedPriv.ciphertext),
          nonce: bytesToB64(wrappedPriv.nonce),
        },
        wrappedVaultKey: bytesToB64(wrappedVaultKey),
      });
    }

    if (wrappedVaultKey) {
      showChecklist({
        paneId: "success-pane",
        title: "Vault ready",
        intro: "You're the workspace's first vault member, so you set up the master key. Future teammates register, then you (or another admin) grant them access.",
        hideIds: ["register-form"],
        steps: [
          { label: "Workspace vault initialized", state: "done" },
          { label: "Add your first secret", sublabel: "Use the \"Add a secret\" page or ask Kit in Slack.", state: "current" },
        ],
      });
    } else {
      showChecklist({
        paneId: "success-pane",
        title: "You're registered — almost there",
        intro: "Your keys are set up. An admin still needs to grant your account access to the workspace vault key before you can read or add secrets.",
        hideIds: ["register-form"],
        steps: [
          { label: "Vault registered", sublabel: "Master password saved on this device only.", state: "done" },
          { label: "An admin grants you access", sublabel: "They'll see a card on their swipe stack. Ping someone if it's urgent.", state: "current" },
          { label: "Read and add secrets", state: "pending" },
        ],
      });
    }

    // If the user landed on /register from a deep link to /add or
    // /reveal (ensureRegistered redirected them), bounce back so they
    // can complete what they came for. Bootstrap admins go back to
    // their original page; non-bootstrap users get the same redirect
    // but will see the unlock prompt fail until granted.
    const params = new URLSearchParams(window.location.search);
    const returnTo = params.get("return_to");
    if (returnTo && returnTo.startsWith("/" + VAULT.tenantSlug + "/")) {
      setTimeout(() => location.replace(returnTo), 1500);
    }
  });
}

async function wireAdd() {
  await ensureUnlocked();
  const form = document.getElementById("add-form");
  if (!form) return;
  showSection("add-form");
  const params = new URLSearchParams(window.location.search);
  if (params.get("title")) form.elements.title.value = params.get("title");
  if (params.get("url")) form.elements.url.value = params.get("url");

  await populateRoleSelector(document.getElementById("role-selector"), null);
  wirePasswordHelpers(form);

  form.addEventListener("submit", async (ev) => {
    ev.preventDefault();

    const fd = new FormData(form);
    const roleID = fd.get("role_id");
    if (!roleID) {
      setStatus("Pick a role.", "error");
      return;
    }

    setStatus("Encrypting…");
    const value = {
      password: fd.get("password") || "",
      notes: fd.get("notes") || "",
    };
    const totp = parseOtpauthURI(fd.get("totp") || "");
    if (totp) value.totp = compactTOTP(totp);
    const valueJSON = JSON.stringify(value);
    const enc = await workerCall("encrypt", { plaintext: new TextEncoder().encode(valueJSON) });

    try {
      await api("POST", "/entries", {
        title: fd.get("title") || "",
        username: fd.get("username") || "",
        url: normalizeURL(fd.get("url") || ""),
        value_ciphertext: bytesField(new Uint8Array(enc.ciphertext)),
        value_nonce: bytesField(new Uint8Array(enc.nonce)),
        role_id: roleID,
      });
    } catch (err) {
      setStatus(`Save failed: ${err.message || err}`, "error");
      return;
    }
    setStatus("", "");
    form.reset();
    hideSection("add-form");
    showSection("saved-message");
  });
}

// normalizeURL prepends https:// when the user typed a bare hostname so
// the saved URL is a clickable link on the reveal page. Empty stays empty;
// anything with an explicit scheme is left alone.
function normalizeURL(raw) {
  const v = raw.trim();
  if (!v) return "";
  if (/^[a-z][a-z0-9+.-]*:\/\//i.test(v)) return v;
  return "https://" + v;
}

// wirePasswordHelpers wires the Show/Hide toggle and the Suggest button
// for the password input. Generated passwords use crypto.getRandomValues
// over a 70-char alphabet (~6.13 bits/char) → 20 chars ≈ 122 bits, well
// past the brute-force threshold for any realistic offline attack.
function wirePasswordHelpers(form) {
  const input = form.elements.password;
  const toggle = document.getElementById("toggle-password");
  const suggest = document.getElementById("generate-password");
  if (!input || !toggle || !suggest) return;

  toggle.addEventListener("click", () => {
    if (input.type === "password") {
      input.type = "text";
      toggle.textContent = "Hide";
    } else {
      input.type = "password";
      toggle.textContent = "Show";
    }
  });

  suggest.addEventListener("click", () => {
    input.value = generatePassword(20);
    input.type = "text";
    toggle.textContent = "Hide";
  });
}

function generatePassword(length) {
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!@#$%^&*";
  const out = new Array(length);
  const buf = new Uint32Array(length);
  crypto.getRandomValues(buf);
  for (let i = 0; i < length; i++) {
    out[i] = alphabet[buf[i] % alphabet.length];
  }
  return out.join("");
}

// populateRoleSelector fills a <select> element with the caller's
// tenant roles. The tenant's default_role_id (the auto-managed
// 'member' role) is rendered first as "Members (everyone)" since
// every workspace user is implicitly assigned to it; scoping a
// secret there is the way to share with everyone. selectedID
// pre-selects a specific role (pass null to default to the member
// role).
async function populateRoleSelector(selectEl, selectedID) {
  if (!selectEl) return;
  const principals = await api("GET", "/principals");
  selectEl.innerHTML = "";
  const defaultID = principals.default_role_id || null;
  const roles = principals.roles || [];
  // Render the default-role option first with the friendlier label.
  for (const role of roles) {
    if (role.id === defaultID) {
      const opt = document.createElement("option");
      opt.value = role.id;
      opt.textContent = `Members (everyone in the workspace)`;
      selectEl.appendChild(opt);
      break;
    }
  }
  for (const role of roles) {
    if (role.id === defaultID) continue;
    const opt = document.createElement("option");
    opt.value = role.id;
    opt.textContent = role.name;
    selectEl.appendChild(opt);
  }
  if (selectedID) {
    selectEl.value = selectedID;
  } else if (defaultID) {
    selectEl.value = defaultID;
  }
}

async function wireReveal() {
  await ensureUnlocked();
  showSection("reveal-area");

  const entry = await api("GET", `/entries/${VAULT.entryId}`);
  document.getElementById("entry-title").textContent = entry.title;
  document.getElementById("entry-username").textContent = entry.username || "";
  const urlEl = document.getElementById("entry-url");
  if (entry.url) {
    urlEl.href = entry.url;
    urlEl.textContent = entry.url;
  }

  const ct = b64ToBytes(entry.value_ciphertext);
  const nonce = b64ToBytes(entry.value_nonce);
  const plain = await workerCall("decrypt", { ciphertext: ct, nonce });
  const decoded = JSON.parse(new TextDecoder().decode(new Uint8Array(plain)));

  const pwEl = document.getElementById("entry-password");
  const notes = (decoded.notes || "").trim();
  if (notes) {
    document.getElementById("entry-notes").textContent = notes;
    document.getElementById("entry-notes-section").hidden = false;
  }

  document.getElementById("show-password").addEventListener("click", () => {
    pwEl.textContent = decoded.password || "";
    pwEl.classList.remove("hidden");
  });
  document.getElementById("copy-password").addEventListener("click", async () => {
    try {
      await navigator.clipboard.writeText(decoded.password || "");
      const s = document.getElementById("copy-status");
      if (s) {
        s.textContent = "Copied. Will clear in 90s.";
        s.className = "success";
      }
      setTimeout(async () => {
        try { await navigator.clipboard.writeText(""); } catch {}
        const s2 = document.getElementById("copy-status");
        if (s2) s2.textContent = "Cleared.";
      }, 90_000);
    } catch (err) {
      setStatus(`Copy failed: ${err.message || err}`, "error");
    }
  });

  if (decoded.totp?.secret) {
    await startTOTPRender(expandTOTP(decoded.totp));
  }

  // Visibility (role) edit affordance.
  await wireRoleEdit(entry.role_id || null, entry.role_name || null);
}

// Module-scope timer so onLockedExternally and the lock-button handler
// can both clear it. There's only ever one TOTP timer per page.
let totpTimer = null;

async function startTOTPRender(params) {
  const section = document.getElementById("entry-totp");
  const codeEl = document.getElementById("totp-code");
  const countdownEl = document.getElementById("totp-countdown");
  const copyBtn = document.getElementById("copy-totp");
  if (!section || !codeEl || !countdownEl || !copyBtn) return;
  section.hidden = false;

  let lastCode = "";
  const tick = async () => {
    const { code, remainingMs } = await generateTOTP(params, Date.now());
    if (code !== lastCode) {
      codeEl.textContent = `${code.slice(0, 3)} ${code.slice(3)}`;
      lastCode = code;
    }
    countdownEl.textContent = `refreshes in ${Math.ceil(remainingMs / 1000)}s`;
  };
  await tick();
  totpTimer = setInterval(tick, 1000);

  copyBtn.addEventListener("click", async () => {
    try {
      await navigator.clipboard.writeText(lastCode);
      countdownEl.textContent = "copied";
    } catch (err) {
      setStatus(`Copy failed: ${err.message || err}`, "error");
    }
  });
}

function stopTOTPRender() {
  if (totpTimer) {
    clearInterval(totpTimer);
    totpTimer = null;
  }
}

// wireRoleEdit renders the current owning role + an Edit button that
// swaps in a single-select dropdown. On Save, PUTs role_id; on 401
// (ErrStepUpRequired from server-side widening detection), prompts for
// re-unlock and retries once. The dropdown is filtered to roles the
// caller is a member of (server enforces the same rule), so an owner
// viewing a secret scoped to a role they aren't on can SEE it but can't
// switch the scope to another non-member role.
async function wireRoleEdit(currentRoleID, currentRoleName) {
  const display = document.getElementById("visibility-display");
  const editBtn = document.getElementById("edit-visibility-button");
  const form = document.getElementById("visibility-form");
  const select = document.getElementById("visibility-role-selector");
  const cancel = document.getElementById("cancel-visibility-edit");
  const status = document.getElementById("visibility-status");
  let principalsCache = null;

  const renderLabel = async (roleID, fallbackName) => {
    if (!principalsCache) {
      try { principalsCache = await api("GET", "/principals"); } catch {}
    }
    if (roleID && roleID === principalsCache?.default_role_id) {
      display.textContent = "Members (everyone in the workspace)";
      return;
    }
    const name = (principalsCache?.roles || []).find((r) => r.id === roleID)?.name || fallbackName;
    display.textContent = name ? `Role: ${name}` : `Role: ${roleID}`;
  };

  await renderLabel(currentRoleID, currentRoleName);

  editBtn.addEventListener("click", async () => {
    await populateRoleSelector(select, currentRoleID);
    form.hidden = false;
    editBtn.hidden = true;
  });

  cancel.addEventListener("click", () => {
    form.hidden = true;
    editBtn.hidden = false;
    status.textContent = "";
  });

  form.addEventListener("submit", async (ev) => {
    ev.preventDefault();
    const next = select.value;
    if (!next) {
      status.textContent = "Pick a role.";
      status.className = "error";
      return;
    }
    status.textContent = "Saving…";
    status.className = "";
    const put = () => api("PUT", `/entries/${VAULT.entryId}/role`, { role_id: next });
    try {
      await put();
    } catch (err) {
      if ((err.message || "").includes("HTTP 401")) {
        status.textContent = "Re-unlock to confirm…";
        try { await workerCall("lock"); } catch {}
        await ensureUnlocked();
        try { await put(); } catch (retryErr) {
          status.textContent = `Save failed: ${retryErr.message || retryErr}`;
          status.className = "error";
          return;
        }
      } else {
        status.textContent = `Save failed: ${err.message || err}`;
        status.className = "error";
        return;
      }
    }
    currentRoleID = next;
    await renderLabel(currentRoleID, null);
    form.hidden = true;
    editBtn.hidden = false;
    status.textContent = "Saved.";
    status.className = "success";
  });
}

async function wireGrant() {
  await ensureUnlocked();
  showSection("grant-area");

  const target = await api("GET", `/users/${VAULT.targetUserId}`);
  if (!target || !target.public_key) {
    setStatus("Target user has not registered yet.", "error");
    return;
  }
  document.getElementById("target-name").textContent = VAULT.targetUserId;
  document.getElementById("target-fingerprint").textContent = target.fingerprint || "";
  if (target.reset) {
    document.getElementById("reset-banner").hidden = false;
  }

  document.getElementById("grant-button").addEventListener("click", async () => {
    setStatus("Wrapping vault key for target…");
    // Pass only the target user id; the worker fetches the pubkey
    // itself so an XSS on this page can't swap in an attacker key.
    const wrapped = await workerCall("wrap_for", {
      targetUserID: VAULT.targetUserId,
      apiBase: VAULT.apiBase,
    });
    await api("POST", `/grants/${VAULT.targetUserId}`, {
      wrapped_vault_key: bytesField(new Uint8Array(wrapped)),
    });
    setStatus("Granted. The target user can now unlock the vault.", "success");
  });

  document.getElementById("decline-button").addEventListener("click", async () => {
    if (!confirm("Decline this registration? The user will need to re-register from scratch.")) return;
    setStatus("Declining…");
    await api("DELETE", `/users/${VAULT.targetUserId}`);
    setStatus("Declined. The user's pending registration was removed.", "success");
  });
}

// wireForgot wires the "Send reset request" button on /apps/vault/forgot.
// POSTs /api/forgot which mints the admin-scoped decision card. No
// master-password unlock required — by definition the user can't unlock.
async function wireForgot() {
  const btn = document.getElementById("forgot-submit");
  if (!btn) return;
  btn.addEventListener("click", async () => {
    btn.disabled = true;
    setStatus("Sending request…");
    try {
      await api("POST", "/forgot", {});
    } catch (err) {
      btn.disabled = false;
      setStatus(`Request failed: ${err.message || err}`, "error");
      return;
    }
    showChecklist({
      paneId: "success-pane",
      title: "Reset request sent",
      intro: "Every workspace admin now sees a card on their swipe stack asking them to approve. Ping one of them on Slack if it's urgent.",
      hideIds: ["forgot-intro"],
      steps: [
        { label: "Reset request sent", state: "done" },
        { label: "An admin verifies and approves", sublabel: "They'll confirm it's really you (Slack DM, in person, etc.) before clicking Approve.", state: "current" },
        { label: "Set a new master password", sublabel: "You'll see a card on your stack with the link.", state: "pending" },
        { label: "An admin re-grants your access", state: "pending" },
        { label: "Read and add secrets", state: "pending" },
      ],
    });
  });
}

// wireCancelReset wires the small confirmation page linked from the
// reset-triggered briefing. POSTs /cancel_reset on confirm; that endpoint
// wipes the row server-side. No master-password unlock required — by
// definition the legitimate user can't unlock right now (the attacker
// just changed the master password). Authentication is the session cookie.
async function wireCancelReset() {
  document.getElementById("cancel-button").addEventListener("click", async () => {
    if (!confirm("Cancel the reset and wipe the pending vault keys for your account?")) return;
    setStatus("Cancelling…");
    try {
      await api("POST", "/cancel_reset", {});
    } catch (err) {
      setStatus(`Cancel failed: ${err.message || err}`, "error");
      return;
    }
    setStatus("Reset cancelled. Your vault account is wiped — re-register when you're ready.", "success");
    document.getElementById("cancel-button").disabled = true;
    document.getElementById("dismiss-button").disabled = true;
  });
  document.getElementById("dismiss-button").addEventListener("click", () => {
    setStatus("Dismissed. Your reset stays active.", "");
  });
}

// ===== UI helpers =====

function setStatus(text, kind) {
  const el = document.getElementById("status");
  if (!el) return;
  el.textContent = text || "";
  el.className = kind || "";
}
// showChecklist swaps a form/section out for a "what just happened, what
// happens next" pane. steps is an array of { label, sublabel?, state }
// where state is "done" | "current" | "pending". hideIds is a list of
// element IDs to hide while the pane is shown (typically the form).
function showChecklist({ paneId, title, intro, steps, hideIds }) {
  const pane = document.getElementById(paneId);
  if (!pane) return;
  if (Array.isArray(hideIds)) {
    for (const id of hideIds) hideSection(id);
  }
  const parts = [];
  if (title) parts.push(`<h2>${escHTML(title)}</h2>`);
  if (intro) parts.push(`<p>${escHTML(intro)}</p>`);
  parts.push('<ul class="checklist">');
  for (const s of steps) {
    const sub = s.sublabel ? `<div class="sublabel">${escHTML(s.sublabel)}</div>` : "";
    parts.push(
      `<li class="${s.state}"><span class="marker" aria-hidden="true"></span>` +
      `<span class="label">${escHTML(s.label)}${sub}</span></li>`,
    );
  }
  parts.push("</ul>");
  pane.innerHTML = parts.join("");
  pane.hidden = false;
  pane.scrollIntoView({ behavior: "smooth", block: "nearest" });
}
function escHTML(s) {
  return String(s).replace(/[&<>"']/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c],
  );
}
function showSection(id) { const el = document.getElementById(id); if (el) el.hidden = false; }

// Entry point. MUST be at the end of the file: top-level `let`
// declarations (workerPort, dbBusy, …) are in TDZ until reached in
// source order, so calling main() before them threw a ReferenceError.
if (VAULT) {
  main().catch((err) => {
    console.error("vault: unhandled error", err);
    setStatus(`Error: ${err.message || err}`, "error");
  });
}
function hideSection(id) { const el = document.getElementById(id); if (el) el.hidden = true; }
