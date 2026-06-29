// Vault browser-side crypto — v2 (shared master password).
//
// Architecture:
//   - One shared master password per tenant (out-of-band). Every member
//     types the same password to unlock.
//   - SharedWorker (vault-worker.js) holds the unwrapped vault_key for
//     the lifetime of any open vault tab. The page interacts via
//     postMessage; the protocol exposes encrypt/decrypt only, never an
//     export-raw operation.
//   - IndexedDB caches kdfParams + wrapped_vault_key + nonce + tenant
//     id bytes between sessions on the same browser.
//   - BroadcastChannel('kit-vault-lock') syncs lock/unlock across tabs.
//   - vault_generation: every API response carries the tenant's current
//     generation. If the worker sees a higher generation than its
//     cached key was set against, it locks immediately (handles
//     teammate-initiated rotation).
//
// Crypto:
//   - KDF:    PBKDF2-SHA256, 600,000 iterations, 32-byte output
//   - Split:  master_key = PBKDF2(password, salt)
//             enc_key   = HKDF(master_key, salt, "kit-vault-v1-enc")
//             auth_hash = HKDF(master_key, salt, "kit-vault-v1-auth")
//   - Wrap:   wrapped_vault_key = AES-GCM(vault_key, enc_key,
//                                          nonce, aad=tenant_id_bytes)
//   - Entry:  AES-GCM(JSON{password,notes,totp?}, vault_key)

import { parseOtpauthURI, generateTOTP } from "./vault-totp.js";

const TOTP_DEFAULTS = { algorithm: "SHA1", digits: 6, period: 30 };

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
  };
})();

async function main() {
  if (!window.isSecureContext) {
    setStatus("This page requires HTTPS (or localhost).", "error");
    return;
  }
  await connectWorker();
  installLockHooks();
  switch (VAULT.page) {
    case "setup":  return wireSetup();
    case "rotate": return wireRotate();
    case "nuke":   return wireNuke();
    case "list":   return wireList();
    case "add":    return wireAdd();
    case "reveal": return wireReveal();
    default: setStatus(`Unknown vault page: ${VAULT.page}`, "error");
  }
}

// ===== KDF + key derivation =====

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

// ===== AES-GCM with AAD (page-side, for the wrapped vault_key) =====

async function aesGcmEncryptWithAAD(key, plaintext, aad) {
  const nonce = crypto.getRandomValues(new Uint8Array(12));
  const ct = await crypto.subtle.encrypt(
    { name: "AES-GCM", iv: nonce, additionalData: aad },
    key, plaintext,
  );
  return { ciphertext: new Uint8Array(ct), nonce };
}

async function aesGcmDecryptWithAAD(key, ciphertext, nonce, aad) {
  const pt = await crypto.subtle.decrypt(
    { name: "AES-GCM", iv: nonce, additionalData: aad },
    key, ciphertext,
  );
  return new Uint8Array(pt);
}

// ===== fetch + base64 =====

async function api(method, path, body) {
  const resp = await fetch(VAULT.apiBase + path, {
    method,
    credentials: "same-origin",
    headers: {
      "X-Kit-Vault": "1",
      ...(body !== undefined ? { "Content-Type": "application/json" } : {}),
    },
    body: body !== undefined ? JSON.stringify(body) : null,
  });
  if (!resp.ok) {
    // Prefer the server's JSON {error} message (e.g. a 403 "Incorrect
    // vault password.") over a raw "HTTP 403" string.
    let msg = "";
    try {
      const body = await resp.json();
      if (body && body.error) msg = body.error;
    } catch {
      /* non-JSON error body */
    }
    throw new Error(msg || `HTTP ${resp.status}: ${resp.statusText}`);
  }
  if (resp.status === 204) return null;
  return resp.json();
}

function bytesToB64(bytes) {
  let bin = "";
  for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
  return btoa(bin);
}

function b64ToBytes(s) {
  const bin = atob(s);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

const bytesField = bytesToB64;

function hexToBytes(hex) {
  if (typeof hex !== "string" || hex.length % 2 !== 0) {
    throw new Error("invalid hex string");
  }
  const out = new Uint8Array(hex.length / 2);
  for (let i = 0; i < out.length; i++) {
    out[i] = parseInt(hex.substr(i * 2, 2), 16);
  }
  return out;
}

// ===== SharedWorker proxy =====

let workerPort = null;
let nextMsgID = 1;
const pending = new Map();

async function connectWorker() {
  const sw = new SharedWorker(`${VAULT.staticBase}/vault-worker.js`, { name: "kit-vault", type: "module" });
  workerPort = sw.port;
  workerPort.onmessage = (ev) => {
    const m = ev.data;
    const p = pending.get(m.id);
    if (!p) return;
    pending.delete(m.id);
    if (m.ok) p.resolve(m.result);
    else p.reject(new Error(m.error || "worker error"));
  };
  workerPort.start();
}

function workerCall(type, payload) {
  const id = nextMsgID++;
  return new Promise((resolve, reject) => {
    pending.set(id, { resolve, reject });
    workerPort.postMessage({ id, type, ...(payload || {}) });
  });
}

// ===== IndexedDB persistence (cached unlock state) =====

const DB_NAME = "kit-vault";
const DB_STORE = "self";

let dbChain = Promise.resolve();
function dbSerial(fn) {
  dbChain = dbChain.then(fn, fn);
  return dbChain;
}

function openDB() {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open(DB_NAME, 2);
    req.onupgradeneeded = () => {
      const db = req.result;
      if (!db.objectStoreNames.contains(DB_STORE)) {
        db.createObjectStore(DB_STORE);
      }
    };
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
}

async function dbPut(value) {
  return dbSerial(async () => {
    const db = await openDB();
    return new Promise((resolve, reject) => {
      const tx = db.transaction(DB_STORE, "readwrite");
      tx.objectStore(DB_STORE).put(value, "current");
      tx.oncomplete = resolve;
      tx.onerror = () => reject(tx.error);
    });
  });
}

async function dbGet() {
  return dbSerial(async () => {
    const db = await openDB();
    return new Promise((resolve, reject) => {
      const tx = db.transaction(DB_STORE, "readonly");
      const req = tx.objectStore(DB_STORE).get("current");
      req.onsuccess = () => resolve(req.result || null);
      req.onerror = () => reject(req.error);
    });
  });
}

async function dbWipe() {
  return dbSerial(() => new Promise((resolve) => {
    const req = indexedDB.deleteDatabase(DB_NAME);
    req.onsuccess = resolve;
    req.onerror = resolve;
    req.onblocked = resolve;
  }));
}

// ===== BroadcastChannel + lock hooks =====

const lockChannel = new BroadcastChannel("kit-vault-lock");
lockChannel.onmessage = (ev) => {
  if (ev.data && ev.data.type === "locked") {
    onLockedExternally();
  }
};

function onLockedExternally() {
  // Repaint UI to "locked" state. Each page wire sets up its own
  // unlock prompt; here we just hide content areas and surface the
  // prompt if it exists.
  hideSection("list-area");
  hideSection("add-form");
  hideSection("reveal-area");
  showSection("unlock-prompt");
}

async function lockNow() {
  await workerCall("lock").catch(() => {});
  await dbWipe();
  lockChannel.postMessage({ type: "locked" });
}

function installLockHooks() {
  // The SharedWorker survives page reloads and navigation; the only
  // automatic lock paths are its idle (10min) and absolute (30min)
  // timers, or an explicit "Lock now" button. We deliberately do NOT
  // lock on beforeunload — that would re-prompt for the master
  // password on every refresh, which is hostile UX.
  //
  // When the user closes the last vault tab, the browser GCs the
  // SharedWorker after a short grace period and the cached key dies
  // with it; reopening starts fresh and prompts for unlock. That's
  // the natural close-the-tab-to-lock behaviour and doesn't need
  // explicit help here.
}

// ===== unlock flow =====

async function unlock(password) {
  if (await workerCall("has_key")) return;

  // Try cached kdf_params first; fall back to /status on miss.
  let cached = await dbGet();
  let kdfParams;
  if (cached && cached.kdfParams) {
    kdfParams = cached.kdfParams;
  } else {
    const st = await api("GET", "/status");
    if (!st || !st.set_up) {
      throw new Error("Vault is not set up yet. Ask an admin to set it up.");
    }
    kdfParams = st.kdf_params;
  }

  const salt = b64ToBytes(kdfParams.salt);
  const { encKey, authHash } = await deriveKeys(password, salt);

  const resp = await api("POST", "/unlock", { auth_hash: bytesField(authHash) });

  const wrappedVK = b64ToBytes(resp.wrapped_vault_key);
  const wrappedVKNonce = b64ToBytes(resp.wrapped_vault_key_nonce);
  const aad = hexToBytes(resp.tenant_id_bytes);
  const vaultGeneration = resp.vault_generation | 0;

  const rawVaultKey = await aesGcmDecryptWithAAD(encKey, wrappedVK, wrappedVKNonce, aad);

  await workerCall("set_key", { rawKey: rawVaultKey.buffer, vaultGeneration });
  rawVaultKey.fill(0);

  await dbPut({
    kdfParams,
    wrappedVaultKey: bytesToB64(wrappedVK),
    wrappedVaultKeyNonce: bytesToB64(wrappedVKNonce),
    tenantIDBytes: resp.tenant_id_bytes,
    vaultGeneration,
  });
}

async function ensureUnlocked() {
  const ok = await workerCall("has_key");
  if (ok) return;
  showSection("unlock-prompt");
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

async function wireSetup() {
  const form = document.getElementById("setup-form");
  if (!form) return;
  await wirePassphraseHelpers(form);

  form.addEventListener("submit", async (ev) => {
    ev.preventDefault();
    const data = new FormData(form);
    const pw = data.get("master_password");
    if (pw !== data.get("master_password_confirm")) {
      setStatus("Passwords don't match.", "error");
      return;
    }
    if (typeof pw !== "string" || pw.length < 4) {
      setStatus("Pick at least 4 characters (this catches empty submissions; otherwise we accept whatever you type).", "error");
      return;
    }
    if (await workerCall("has_key")) await workerCall("lock");

    setStatus("Setting up vault…");
    const salt = crypto.getRandomValues(new Uint8Array(16));
    const kdfParams = {
      algo: "pbkdf2-sha256",
      iterations: KDF_ITERATIONS,
      salt: bytesToB64(salt),
    };
    const { encKey, authHash } = await deriveKeys(pw, salt);
    const rawVaultKey = crypto.getRandomValues(new Uint8Array(32));

    // Look up tenant_id_bytes by calling /status (which works for non-
    // initialized tenants too) — but for setup we need it from somewhere.
    // Since the page has the slug but not the UUID, we POST to setup
    // and let the server derive AAD from the tenant_id of the request.
    // We need the tenant_id_bytes for AES-GCM, though. Simplest: do a
    // /status call before setup. /status returns set_up=false but we
    // can pass tenant_id_bytes alongside.
    //
    // Approach: ship tenant_id_bytes in the basePageData by reading the
    // server's response on first setup. We compute AAD client-side as
    // empty bytes here -- wait, that would mismatch the server's check.
    //
    // Cleanest fix: derive AAD on the server, not the browser. But that
    // breaks the AAD invariant (browser must know the AAD it wraps with).
    //
    // For now, fetch tenant_id_bytes from /status. Status is fine to
    // return tenant_id_bytes whether set_up or not.
    let tenantIDHex;
    try {
      const st = await api("GET", "/status");
      tenantIDHex = st.tenant_id_bytes;
      if (!tenantIDHex) throw new Error("server didn't return tenant_id_bytes");
    } catch (err) {
      setStatus(`Couldn't initialize vault: ${err.message || err}`, "error");
      return;
    }
    const aad = hexToBytes(tenantIDHex);

    const { ciphertext: wrappedVK, nonce } = await aesGcmEncryptWithAAD(encKey, rawVaultKey, aad);

    // Sanity check: round-trip locally before posting.
    try {
      const roundtrip = await aesGcmDecryptWithAAD(encKey, wrappedVK, nonce, aad);
      if (!sameBytes(roundtrip, rawVaultKey)) throw new Error("local round-trip mismatch");
    } catch (err) {
      setStatus(`Crypto self-check failed: ${err.message || err}. Refusing to send broken data.`, "error");
      return;
    }

    try {
      await api("POST", "/setup", {
        kdf_params: kdfParams,
        auth_hash: bytesField(authHash),
        wrapped_vault_key: bytesField(wrappedVK),
        wrapped_vault_key_nonce: bytesField(nonce),
      });
    } catch (err) {
      setStatus(`Setup failed: ${err.message || err}`, "error");
      return;
    }

    // Setup succeeded — cache and seed the worker so the admin doesn't
    // have to re-type immediately.
    await workerCall("set_key", { rawKey: rawVaultKey.buffer, vaultGeneration: 1 });
    rawVaultKey.fill(0);
    await dbPut({
      kdfParams,
      wrappedVaultKey: bytesToB64(wrappedVK),
      wrappedVaultKeyNonce: bytesToB64(nonce),
      tenantIDBytes: tenantIDHex,
      vaultGeneration: 1,
    });

    setStatus("Vault set up. Redirecting…");
    setTimeout(() => {
      window.location.href = `/${VAULT.tenantSlug}/apps/vault`;
    }, 800);
  });
}

async function wireRotate() {
  const form = document.getElementById("rotate-form");
  if (!form) return;
  await wirePassphraseHelpers(form);

  form.addEventListener("submit", async (ev) => {
    ev.preventDefault();
    const data = new FormData(form);
    const oldPw = data.get("old_password");
    const newPw = data.get("new_password");
    if (newPw !== data.get("new_password_confirm")) {
      setStatus("New passwords don't match.", "error");
      return;
    }
    if (typeof newPw !== "string" || newPw.length < 4) {
      setStatus("New password must be at least 4 characters.", "error");
      return;
    }

    // Step 1: unlock locally with old password to recover vault_key.
    if (await workerCall("has_key")) await workerCall("lock");
    try {
      await unlock(oldPw);
    } catch (err) {
      setStatus(`Could not unlock with the old password: ${err.message || err}`, "error");
      return;
    }

    // Worker has vault_key cached now. Pull out a wrap-only handle by
    // asking the worker to AES-GCM-encrypt the vault_key under a new
    // wrapping key — but the worker doesn't have export-raw. So we
    // recover vault_key by re-deriving from the cached wrapped blob
    // (which we just unwrapped) and the old password. Simpler: do the
    // unwrap inline here instead of going through the worker for the
    // raw bytes.
    const cached = await dbGet();
    if (!cached) {
      setStatus("Internal error: missing cached vault state after unlock.", "error");
      return;
    }
    const oldSalt = b64ToBytes(cached.kdfParams.salt);
    const oldKeys = await deriveKeys(oldPw, oldSalt);
    const wrappedVK = b64ToBytes(cached.wrappedVaultKey);
    const wrappedVKNonce = b64ToBytes(cached.wrappedVaultKeyNonce);
    const aad = hexToBytes(cached.tenantIDBytes);
    let rawVaultKey;
    try {
      rawVaultKey = await aesGcmDecryptWithAAD(oldKeys.encKey, wrappedVK, wrappedVKNonce, aad);
    } catch (err) {
      setStatus("Could not recover vault key — try unlocking the vault first then rotating.", "error");
      return;
    }

    // Step 2: derive new keys and re-wrap vault_key.
    const newSalt = crypto.getRandomValues(new Uint8Array(16));
    const newKDF = {
      algo: "pbkdf2-sha256",
      iterations: KDF_ITERATIONS,
      salt: bytesToB64(newSalt),
    };
    const newKeys = await deriveKeys(newPw, newSalt);
    const { ciphertext: newWrappedVK, nonce: newNonce } =
      await aesGcmEncryptWithAAD(newKeys.encKey, rawVaultKey, aad);
    rawVaultKey.fill(0);

    try {
      const resp = await api("POST", "/rotate", {
        kdf_params: newKDF,
        auth_hash: bytesField(newKeys.authHash),
        wrapped_vault_key: bytesField(newWrappedVK),
        wrapped_vault_key_nonce: bytesField(newNonce),
      });
      await dbPut({
        kdfParams: newKDF,
        wrappedVaultKey: bytesToB64(newWrappedVK),
        wrappedVaultKeyNonce: bytesToB64(newNonce),
        tenantIDBytes: cached.tenantIDBytes,
        vaultGeneration: resp.vault_generation | 0,
      });
    } catch (err) {
      setStatus(`Rotation failed: ${err.message || err}`, "error");
      return;
    }
    setStatus("Password rotated. Redirecting…");
    setTimeout(() => { window.location.href = `/${VAULT.tenantSlug}/apps/vault`; }, 800);
  });
}

async function wireNuke() {
  const form = document.getElementById("nuke-form");
  if (!form) return;
  form.addEventListener("submit", async (ev) => {
    ev.preventDefault();
    const data = new FormData(form);
    const confirmSlug = data.get("confirm_slug");
    if (confirmSlug !== VAULT.tenantSlug) {
      setStatus("Confirmation text doesn't match the slug.", "error");
      return;
    }
    setStatus("Destroying vault…");
    try {
      await api("POST", "/nuke", { confirm_slug: confirmSlug });
    } catch (err) {
      setStatus(`Nuke failed: ${err.message || err}`, "error");
      return;
    }
    await dbWipe();
    await workerCall("lock").catch(() => {});
    setStatus("Vault destroyed. Redirecting to setup…");
    setTimeout(() => { window.location.href = `/${VAULT.tenantSlug}/apps/vault/setup`; }, 800);
  });
}

async function wireList() {
  showSection("list-area");
  const ul = document.getElementById("list-entries");
  const empty = document.getElementById("list-empty");
  const filter = document.getElementById("list-filter");
  if (!ul || !empty || !filter) return;

  let rows = [];
  try {
    rows = await api("GET", "/entries?limit=500");
  } catch (err) {
    setStatus(`Couldn't load secrets: ${err.message || err}`, "error");
    return;
  }
  if (!Array.isArray(rows) || rows.length === 0) {
    empty.hidden = false;
    return;
  }
  rows.sort((a, b) => (a.title || "").localeCompare(b.title || ""));

  const render = (q) => {
    ul.innerHTML = "";
    const needle = (q || "").trim().toLowerCase();
    let shown = 0;
    for (const r of rows) {
      const hay = `${r.title || ""} ${r.username || ""} ${r.url || ""} ${r.scope_summary || ""}`.toLowerCase();
      if (needle && !hay.includes(needle)) continue;
      const li = document.createElement("li");
      const link = document.createElement("a");
      link.href = `/${VAULT.tenantSlug}/apps/vault/reveal/${r.id}`;
      link.className = "entry-link";
      const title = document.createElement("span");
      title.className = "entry-title";
      title.textContent = r.title || "(untitled)";
      link.appendChild(title);
      const meta = document.createElement("span");
      meta.className = "entry-meta";
      const parts = [];
      if (r.username) parts.push(r.username);
      if (r.scope_summary) parts.push(r.scope_summary);
      meta.textContent = parts.join(" — ");
      link.appendChild(meta);
      li.appendChild(link);
      ul.appendChild(li);
      shown++;
    }
    empty.hidden = shown !== 0;
  };
  render("");
  filter.addEventListener("input", () => render(filter.value));
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
  wirePasswordHelpers(form, "toggle-password", "generate-password");

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
    const enc = await workerCall("encrypt", {
      plaintext: new TextEncoder().encode(JSON.stringify(value)),
    });
    let created;
    try {
      created = await api("POST", "/entries", {
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
    const revealLink = document.getElementById("saved-reveal-link");
    if (revealLink && created && created.id) {
      const revealURL = `/${VAULT.tenantSlug}/apps/vault/reveal/${created.id}`;
      revealLink.href = revealURL;
      revealLink.textContent = revealURL;
    }
    const listLink = document.getElementById("saved-list-link");
    if (listLink) {
      listLink.href = `/${VAULT.tenantSlug}/apps/vault`;
    }
    showSection("saved-message");
  });
}

async function wireReveal() {
  await ensureUnlocked();
  showSection("reveal-area");
  let entry;
  try {
    entry = await api("GET", `/entries/${VAULT.entryId}`);
  } catch (err) {
    setStatus(`Couldn't load entry: ${err.message || err}`, "error");
    return;
  }
  const ct = b64ToBytes(entry.ValueCiphertext || entry.value_ciphertext);
  const nonce = b64ToBytes(entry.ValueNonce || entry.value_nonce);
  let decoded;
  try {
    const pt = await workerCall("decrypt", { ciphertext: ct, nonce });
    decoded = JSON.parse(new TextDecoder().decode(new Uint8Array(pt)));
  } catch (err) {
    setStatus(`Couldn't decrypt: ${err.message || err}`, "error");
    return;
  }
  renderRevealedEntry(entry, decoded);
  wireVisibilityEditor(entry);
}

// wireVisibilityEditor hooks up the "Who can see this" section on the
// reveal page: it shows the entry's current owning role and lets the
// user re-scope it to another role via PUT /entries/{id}/role. The
// server enforces who may pick which role (members of the target role,
// or any admin) and step-up auth for cross-role moves; this just
// surfaces those errors in friendly copy.
function wireVisibilityEditor(entry) {
  const display = document.getElementById("visibility-display");
  const editBtn = document.getElementById("edit-visibility-button");
  const form = document.getElementById("visibility-form");
  const selector = document.getElementById("visibility-role-selector");
  const cancelBtn = document.getElementById("cancel-visibility-edit");
  const status = document.getElementById("visibility-status");
  if (!display || !editBtn || !form || !selector) return;

  const friendly = (name) =>
    !name || name === "member" ? "Everyone (members)" : name;
  let currentRoleID = entry.role_id || entry.RoleID || null;
  display.textContent = friendly(entry.role_name || entry.RoleName);

  const setVisStatus = (text, kind) => {
    if (!status) return;
    status.textContent = text;
    status.className = kind || "";
  };
  const closeForm = () => {
    form.hidden = true;
    editBtn.hidden = false;
  };

  editBtn.addEventListener("click", async () => {
    setVisStatus("");
    await populateRoleSelector(selector, currentRoleID);
    form.hidden = false;
    editBtn.hidden = true;
  });
  if (cancelBtn) cancelBtn.addEventListener("click", closeForm);

  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const roleID = new FormData(form).get("role_id");
    if (!roleID) {
      setVisStatus("Pick a role.", "error");
      return;
    }
    setVisStatus("Saving…");
    try {
      await api("PUT", `/entries/${VAULT.entryId}/role`, { role_id: roleID });
    } catch (err) {
      const msg = String(err.message || err);
      let text = `Couldn't update: ${msg}`;
      if (msg.includes("HTTP 401")) {
        text = "Unlock the vault again to change who can see this.";
      } else if (msg.includes("HTTP 403")) {
        text = "You can only move this to a role you're in (admins can pick any role).";
      }
      setVisStatus(text, "error");
      return;
    }
    currentRoleID = roleID;
    const opt = selector.options[selector.selectedIndex];
    display.textContent = opt ? opt.textContent : friendly(null);
    setVisStatus("Saved.");
    closeForm();
  });
}

function renderRevealedEntry(entry, decoded) {
  const display = document.getElementById("entry-display");
  if (!display) return;
  display.innerHTML = "";
  const title = document.createElement("h2");
  title.textContent = entry.Title || entry.title || "(untitled)";
  display.appendChild(title);

  const list = document.createElement("dl");
  list.className = "entry-fields";
  const addRow = (label, value, kind) => {
    const dt = document.createElement("dt");
    dt.textContent = label;
    list.appendChild(dt);
    const dd = document.createElement("dd");
    if (kind === "secret") {
      const row = document.createElement("div");
      row.className = "secret-row";
      const span = document.createElement("span");
      span.className = "secret-value";
      span.textContent = "•••••";
      const btn = document.createElement("button");
      btn.type = "button";
      btn.className = "secret-action";
      btn.textContent = "Show";
      btn.addEventListener("click", () => {
        const showing = span.textContent !== "•••••";
        span.textContent = showing ? "•••••" : value;
        btn.textContent = showing ? "Show" : "Hide";
      });
      const copy = document.createElement("button");
      copy.type = "button";
      copy.className = "secret-action";
      copy.textContent = "Copy";
      copy.addEventListener("click", () => navigator.clipboard.writeText(value));
      row.appendChild(span);
      row.appendChild(btn);
      row.appendChild(copy);
      dd.appendChild(row);
    } else if (kind === "link") {
      const a = document.createElement("a");
      a.href = value;
      a.textContent = value;
      a.target = "_blank";
      a.rel = "noopener noreferrer";
      dd.appendChild(a);
    } else {
      dd.textContent = value;
    }
    list.appendChild(dd);
  };

  const username = entry.Username || entry.username;
  if (username) addRow("Username", username);
  if (decoded.password) addRow("Password", decoded.password, "secret");
  const url = entry.URL || entry.url;
  if (url) addRow("URL", url, "link");
  if (decoded.notes) addRow("Notes", decoded.notes);
  display.appendChild(list);

  if (decoded.totp) {
    startTOTPRender(expandTOTP(decoded.totp));
  }
}

let totpTimer = null;
async function startTOTPRender(params) {
  const el = document.getElementById("totp-display");
  if (!el) return;
  const tick = async () => {
    try {
      const code = await generateTOTP(params);
      el.textContent = code;
    } catch (err) {
      el.textContent = "(TOTP error)";
    }
  };
  await tick();
  if (totpTimer) clearInterval(totpTimer);
  totpTimer = setInterval(tick, 1000);
}

// ===== UI helpers =====

function normalizeURL(raw) {
  const v = raw.trim();
  if (!v) return "";
  if (/^[a-zA-Z][a-zA-Z0-9+.-]*:/.test(v)) return v;
  return `https://${v}`;
}

function wirePasswordHelpers(form, toggleID, generateID) {
  const pwInput = form.querySelector('input[name="password"]');
  if (!pwInput) return;
  const toggle = document.getElementById(toggleID);
  if (toggle) {
    toggle.addEventListener("click", () => {
      pwInput.type = pwInput.type === "password" ? "text" : "password";
    });
  }
  const gen = document.getElementById(generateID);
  if (gen) {
    gen.addEventListener("click", () => {
      pwInput.value = generatePassword(20);
      pwInput.type = "text";
    });
  }
}

function generatePassword(length) {
  const charset = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789!@#$%^&*";
  const buf = new Uint32Array(length);
  crypto.getRandomValues(buf);
  let out = "";
  for (let i = 0; i < length; i++) out += charset[buf[i] % charset.length];
  return out;
}

// wirePassphraseHelpers wires the "suggest passphrase" and "copy" buttons
// on setup.html and rotate.html. The suggest button generates a 6-word
// diceware passphrase from a small embedded wordlist and writes it into
// both the new-password and confirm fields.
async function wirePassphraseHelpers(form) {
  const target = form.querySelector("#master_password");
  if (!target) return;
  // Pre-fill with a suggestion on first render so admins don't even have
  // to click — matches the plan's "default to diceware" UX.
  const setSuggested = () => {
    const pp = diceware(6);
    target.value = pp;
    const confirm = form.querySelector('input[name="master_password_confirm"], input[name="new_password_confirm"]');
    if (confirm) confirm.value = pp;
  };
  if (!target.value) setSuggested();
  const suggestBtn = document.getElementById("suggest-passphrase");
  if (suggestBtn) suggestBtn.addEventListener("click", (ev) => { ev.preventDefault(); setSuggested(); });
  const copyBtn = document.getElementById("copy-passphrase");
  if (copyBtn) {
    copyBtn.addEventListener("click", (ev) => {
      ev.preventDefault();
      navigator.clipboard.writeText(target.value || "").then(
        () => setStatus("Copied.", ""),
        () => setStatus("Couldn't copy. Select manually.", "error"),
      );
    });
  }
}

// diceware returns a hyphen-joined random passphrase. Wordlist is a
// compact subset — ~256 short common English words — chosen for
// readability rather than the full 7,776-word EFF list. With 256 words
// per slot, 6 words ≈ 48 bits of entropy; we accept that as "OK for a
// memorable team-shared password," matching the small-org familiarity
// model. Admins who want more entropy can type their own.
const DICEWARE_WORDS = [
  "able","acid","acorn","actor","add","admit","after","again","age","agent","agree","ahead","aim","air","alarm","album",
  "alert","alien","alive","all","alley","ally","alone","along","also","alter","amber","amend","among","amount","amuse","anchor",
  "ancient","angel","anger","angle","angry","ankle","annoy","answer","ant","apple","april","arch","arctic","area","arena","argue",
  "arm","army","arrow","art","ash","ask","aspen","aster","atlas","atom","attic","auburn","august","aunt","author","auto",
  "autumn","avenue","awake","awful","baby","back","bacon","badge","bagel","baker","balance","ball","ballad","banana","band","banjo",
  "bank","barn","basic","basil","basin","basket","bass","bat","batch","bath","battle","bay","beach","bean","bear","beard",
  "beat","beauty","beaver","bee","beech","beef","before","begin","beige","bell","belt","bench","bend","berry","best","bicycle",
  "big","bike","bill","bind","birch","bird","biscuit","bishop","bit","black","blank","blaze","blend","bless","blink","blizzard",
  "block","bloom","blue","blush","board","boat","body","boil","bold","bolt","bond","bone","book","boot","border","born",
  "borrow","both","bottle","bottom","bow","box","boy","brain","brake","branch","brass","brave","bread","break","breeze","brick",
  "bridge","brief","bright","bring","brisk","broad","bronze","brook","brown","brush","bubble","bucket","buddy","budget","buffalo","bug",
  "build","bulb","bull","bunny","burst","bus","busy","butter","button","buzz","cable","cactus","cake","calm","camel","camp",
  "canal","candle","candy","cane","canyon","cap","cape","car","card","care","cargo","carry","cart","case","cash","castle",
  "casual","cat","catch","cedar","cell","cello","cement","center","chain","chair","chalk","champ","change","chapel","charm","chart",
  "chase","cheap","check","cheer","cherry","chess","chest","chief","chili","chime","chin","chip","chirp","choice","chord","chorus",
  "chrome","cider","cinder","circle","city","civic","claim","clam","clarity","clean","clear","cliff","climb","clip","clock","cloth",
  "cloud","clover","club","clue","coach","coast","coconut","code","coffee","coil","coin","cold","color","comet","comic","comma",
];

function diceware(n) {
  const out = new Uint32Array(n);
  crypto.getRandomValues(out);
  const parts = [];
  for (let i = 0; i < n; i++) parts.push(DICEWARE_WORDS[out[i] % DICEWARE_WORDS.length]);
  return parts.join("-");
}

async function populateRoleSelector(selectEl, selectedID) {
  if (!selectEl) return;
  let data;
  try {
    data = await api("GET", "/principals");
  } catch (err) {
    setStatus(`Couldn't load roles: ${err.message || err}`, "error");
    return;
  }
  selectEl.innerHTML = "";
  for (const role of data.roles || []) {
    const opt = document.createElement("option");
    opt.value = role.id;
    opt.textContent = role.name === "member" ? "Everyone (members)" : role.name;
    if (role.id === selectedID) opt.selected = true;
    selectEl.appendChild(opt);
  }
  if (!selectedID && data.default_role_id) {
    selectEl.value = data.default_role_id;
  }
}

function sameBytes(a, b) {
  if (a.length !== b.length) return false;
  let diff = 0;
  for (let i = 0; i < a.length; i++) diff |= a[i] ^ b[i];
  return diff === 0;
}

function setStatus(text, kind) {
  const el = document.getElementById("status") || document.getElementById("unlock-status");
  if (!el) return;
  el.textContent = text;
  el.className = kind || "";
}

function showSection(id) { const el = document.getElementById(id); if (el) el.hidden = false; }
function hideSection(id) { const el = document.getElementById(id); if (el) el.hidden = true; }

main().catch((err) => setStatus(`Error: ${err.message || err}`, "error"));
