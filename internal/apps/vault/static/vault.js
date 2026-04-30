// Vault browser-side crypto. v1 implementation.
//
// This module implements the client side of the Bitwarden-Org / 1Password-
// Shared-Vault model documented in the plan. All encryption + decryption
// happens here; the server only ever sees ciphertext and pubkeys.
//
// Crypto pinned for v1:
//   - KDF:        PBKDF2-SHA256, 600,000 iterations, 32-byte output, per-user random salt
//   - Master key splitting:
//                 master_key   = PBKDF2(password, salt, iters)
//                 enc_key      = HKDF(master_key, salt, info="kit-vault-v1-enc")
//                 auth_hash    = HKDF(master_key, salt, info="kit-vault-v1-auth")
//   - Keypair:    RSA-OAEP-2048 with SHA-256, generated client-side
//   - Symmetric:  AES-GCM, 12-byte (96-bit) random nonce per encryption
//
// v1 deliberately defers (these are tracked as v1.1 / v2 work):
//   - Argon2id (vendored @noble/hashes) instead of PBKDF2
//   - SharedWorker holding non-extractable vault_key + BroadcastChannel
//   - IndexedDB persistence of wrapped private key + cross-session unlock
//   - Idle-timer auto-lock + clipboard auto-clear
// In v1 all crypto runs on the main thread and "lock" means "reload the page";
// the captive vault_key lives in this module's closure for the tab's lifetime.

const VAULT = (() => {
  const root = document.getElementById("vault-app");
  if (!root) return null;

  return {
    page: root.dataset.page,
    apiBase: root.dataset.apiBase,
    tenantSlug: root.dataset.tenantSlug,
    entryId: root.dataset.entryId || "",
    targetUserId: root.dataset.targetUserId || "",
  };
})();

if (VAULT) {
  main().catch((err) => {
    console.error("vault: unhandled error", err);
    setStatus(`Error: ${err.message || err}`, "error");
  });
}

async function main() {
  if (!window.isSecureContext) {
    setStatus("This page requires HTTPS (or localhost).", "error");
    return;
  }
  switch (VAULT.page) {
    case "register":
      await wireRegister();
      break;
    case "add":
      await wireAdd();
      break;
    case "reveal":
      await wireReveal();
      break;
    case "grant":
      await wireGrant();
      break;
    default:
      setStatus(`Unknown vault page: ${VAULT.page}`, "error");
  }
}

// ===== KDF + key derivation =====

const KDF_ITERATIONS = 600_000;
const KDF_HASH = "SHA-256";

// pbkdf2(password, salt, iterations) -> 32 bytes (master key).
async function pbkdf2(password, salt) {
  const enc = new TextEncoder();
  const baseKey = await crypto.subtle.importKey(
    "raw",
    enc.encode(password),
    { name: "PBKDF2" },
    false,
    ["deriveBits"],
  );
  const bits = await crypto.subtle.deriveBits(
    { name: "PBKDF2", hash: KDF_HASH, salt, iterations: KDF_ITERATIONS },
    baseKey,
    256,
  );
  return new Uint8Array(bits);
}

// HKDF expand on a 32-byte master key, with a domain-separation `info`
// string. Returns 32 bytes. Implemented here (rather than via WebCrypto's
// HKDF) so we can produce raw bytes for auth_hash and a CryptoKey for
// enc_key in two clean steps.
async function hkdf(masterKey, salt, info) {
  const baseKey = await crypto.subtle.importKey(
    "raw",
    masterKey,
    { name: "HKDF" },
    false,
    ["deriveBits"],
  );
  const bits = await crypto.subtle.deriveBits(
    {
      name: "HKDF",
      hash: KDF_HASH,
      salt,
      info: new TextEncoder().encode(info),
    },
    baseKey,
    256,
  );
  return new Uint8Array(bits);
}

async function deriveKeys(password, salt) {
  const masterKey = await pbkdf2(password, salt);
  const encKeyBytes = await hkdf(masterKey, salt, "kit-vault-v1-enc");
  const authHash = await hkdf(masterKey, salt, "kit-vault-v1-auth");
  const encKey = await crypto.subtle.importKey(
    "raw",
    encKeyBytes,
    { name: "AES-GCM" },
    false,
    ["encrypt", "decrypt"],
  );
  return { encKey, authHash };
}

// ===== AES-GCM helpers =====

async function aesGcmEncrypt(key, plaintext) {
  const nonce = crypto.getRandomValues(new Uint8Array(12));
  const ciphertext = await crypto.subtle.encrypt(
    { name: "AES-GCM", iv: nonce },
    key,
    plaintext,
  );
  return { ciphertext: new Uint8Array(ciphertext), nonce };
}

async function aesGcmDecrypt(key, ciphertext, nonce) {
  const plaintext = await crypto.subtle.decrypt(
    { name: "AES-GCM", iv: nonce },
    key,
    ciphertext,
  );
  return new Uint8Array(plaintext);
}

// ===== RSA-OAEP helpers =====

async function generateRSAKeypair() {
  return crypto.subtle.generateKey(
    {
      name: "RSA-OAEP",
      modulusLength: 2048,
      publicExponent: new Uint8Array([0x01, 0x00, 0x01]), // 65537
      hash: "SHA-256",
    },
    true,
    ["wrapKey", "unwrapKey", "encrypt", "decrypt"],
  );
}

async function exportSpki(pubKey) {
  const spki = await crypto.subtle.exportKey("spki", pubKey);
  return new Uint8Array(spki);
}

async function exportPkcs8(privKey) {
  const pkcs8 = await crypto.subtle.exportKey("pkcs8", privKey);
  return new Uint8Array(pkcs8);
}

async function importSpki(spki) {
  return crypto.subtle.importKey(
    "spki",
    spki,
    { name: "RSA-OAEP", hash: "SHA-256" },
    true,
    ["wrapKey", "encrypt"],
  );
}

async function importPkcs8(pkcs8) {
  return crypto.subtle.importKey(
    "pkcs8",
    pkcs8,
    { name: "RSA-OAEP", hash: "SHA-256" },
    true,
    ["unwrapKey", "decrypt"],
  );
}

// rsaWrapAesKey returns the ciphertext of `aesKey` encrypted under the
// recipient's RSA-OAEP public key. Used to wrap vault_key for a member.
async function rsaWrapAesKey(rsaPubKey, aesKey) {
  const raw = await crypto.subtle.exportKey("raw", aesKey);
  const wrapped = await crypto.subtle.encrypt({ name: "RSA-OAEP" }, rsaPubKey, raw);
  return new Uint8Array(wrapped);
}

async function rsaUnwrapAesKey(rsaPrivKey, wrapped) {
  const raw = await crypto.subtle.decrypt({ name: "RSA-OAEP" }, rsaPrivKey, wrapped);
  return crypto.subtle.importKey(
    "raw",
    raw,
    { name: "AES-GCM" },
    true,
    ["encrypt", "decrypt"],
  );
}

// ===== fetch / base64 helpers =====

async function api(method, path, body) {
  const res = await fetch(`${VAULT.apiBase}${path}`, {
    method,
    credentials: "same-origin",
    headers: body
      ? { "Content-Type": "application/json", "X-Kit-Vault": "1" }
      : { "X-Kit-Vault": "1" },
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

// JSON.stringify for the wire protocol expects []byte fields encoded as
// base64. The Go JSON decoder unmarshals base64 strings into []byte
// automatically, so the encoding is the same in both directions.
function bytesField(bytes) {
  return bytesToB64(bytes);
}

// ===== captive in-memory state =====
//
// In v1 the unwrapped vault_key lives in this module's closure for the
// tab's lifetime. v1.1 will move this into a SharedWorker as a non-
// extractable CryptoKey + IndexedDB-persisted wrapped form.

const STATE = {
  vaultKey: null,        // CryptoKey, AES-GCM, capable of encrypt/decrypt
  unlockedAt: 0,
};

function isUnlocked() {
  return STATE.vaultKey !== null;
}

function lockClient() {
  STATE.vaultKey = null;
  STATE.unlockedAt = 0;
}

// ===== UI helpers =====

function setStatus(text, kind) {
  const el = document.getElementById("status");
  if (!el) return;
  el.textContent = text || "";
  el.className = kind || "";
}

function showSection(id) {
  const el = document.getElementById(id);
  if (el) el.hidden = false;
}
function hideSection(id) {
  const el = document.getElementById(id);
  if (el) el.hidden = true;
}

// ===== unlock flow (used inline by add / reveal / grant pages) =====

async function unlock(password) {
  // Two-step unlock so PBKDF2 has the right salt without trusting any
  // local cache:
  //   1. GET /api/vault/me → kdf_params (salt + iterations).
  //   2. Derive auth_hash from password + salt.
  //   3. POST /api/vault/unlock with auth_hash → server returns the
  //      wrapped private key + wrapped vault key on match.
  const me = await api("GET", "/me");
  if (!me || !me.kdf_params || !me.kdf_params.salt) {
    throw new Error(
      "No vault registration found on this account. Open /register first.",
    );
  }
  const salt = b64ToBytes(me.kdf_params.salt);
  const { encKey, authHash } = await deriveKeys(password, salt);

  const resp = await api("POST", "/unlock", {
    auth_hash: bytesField(authHash),
  });

  // Decrypt the user's RSA private key with the enc_key.
  const wrappedPriv = b64ToBytes(resp.user_private_key_ciphertext);
  const privNonce = b64ToBytes(resp.user_private_key_nonce);
  const pkcs8 = await aesGcmDecrypt(encKey, wrappedPriv, privNonce);
  const rsaPriv = await importPkcs8(pkcs8);

  // RSA-unwrap the vault_key.
  const wrappedVault = b64ToBytes(resp.wrapped_vault_key);
  const vaultKey = await rsaUnwrapAesKey(rsaPriv, wrappedVault);

  STATE.vaultKey = vaultKey;
  STATE.unlockedAt = Date.now();
  return vaultKey;
}

// ensureUnlocked shows the inline unlock prompt if we're not yet unlocked,
// then resolves once the user has entered their master password.
async function ensureUnlocked() {
  if (isUnlocked()) return;
  showSection("unlock-prompt");
  hideSection("add-form");
  hideSection("reveal-area");
  hideSection("grant-area");

  return new Promise((resolve, reject) => {
    const form = document.getElementById("unlock-form");
    if (!form) {
      reject(new Error("no unlock form on this page"));
      return;
    }
    form.addEventListener(
      "submit",
      async (ev) => {
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
      },
      { once: true },
    );
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
    const pwConfirm = data.get("master_password_confirm");
    if (pw !== pwConfirm) {
      setStatus("Passwords don't match.", "error");
      return;
    }
    if (typeof pw !== "string" || pw.length < 14) {
      setStatus("Master password must be at least 14 characters.", "error");
      return;
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

    // Wrap the private key with the enc_key.
    const wrappedPriv = await aesGcmEncrypt(encKey, pkcs8);

    // Decide tenant-init vs post-init by checking the server's existing
    // state. Easier to detect from the server's response than to encode
    // here: we attempt registration WITHOUT a wrapped_vault_key first;
    // if the server says "first user must self-grant" we retry with one.
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
      const msg = err.message || "";
      if (msg.includes("first user in tenant must self-grant")) {
        // Generate vault_key, wrap with our own pubkey, retry.
        const vaultKey = await crypto.subtle.generateKey(
          { name: "AES-GCM", length: 256 },
          true,
          ["encrypt", "decrypt"],
        );
        wrappedVaultKey = await rsaWrapAesKey(rsa.publicKey, vaultKey);
        body.wrapped_vault_key = bytesField(wrappedVaultKey);
        res = await api("POST", "/register", body);
      } else {
        throw err;
      }
    }

    // Self-unlock canary: server marks the row pending=false only after
    // we re-prove we can reproduce auth_hash from the password.
    await api("POST", "/self_unlock_test", { auth_hash: bytesField(authHash) });

    setStatus(
      wrappedVaultKey
        ? "Vault initialized. You're now the workspace's first vault member."
        : "Registered. Waiting for an admin to grant you access.",
      "success",
    );
  });
}

async function wireAdd() {
  await ensureUnlocked();
  const form = document.getElementById("add-form");
  if (!form) return;
  showSection("add-form");

  // Optional pre-fill from query string (?title=&url=).
  const params = new URLSearchParams(window.location.search);
  if (params.get("title")) form.elements.title.value = params.get("title");
  if (params.get("url")) form.elements.url.value = params.get("url");

  form.addEventListener("submit", async (ev) => {
    ev.preventDefault();
    setStatus("Encrypting…");

    const fd = new FormData(form);
    const valueJSON = JSON.stringify({
      password: fd.get("password") || "",
      notes: fd.get("notes") || "",
    });
    const valueBytes = new TextEncoder().encode(valueJSON);
    const enc = await aesGcmEncrypt(STATE.vaultKey, valueBytes);

    const tagsRaw = fd.get("tags") || "";
    const tags = tagsRaw
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean);

    await api("POST", "/entries", {
      title: fd.get("title") || "",
      username: fd.get("username") || "",
      url: fd.get("url") || "",
      tags,
      value_ciphertext: bytesField(enc.ciphertext),
      value_nonce: bytesField(enc.nonce),
    });
    setStatus("Saved.", "success");
    form.reset();
  });
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
  const plain = await aesGcmDecrypt(STATE.vaultKey, ct, nonce);
  const decoded = JSON.parse(new TextDecoder().decode(plain));

  const pwEl = document.getElementById("entry-password");
  pwEl.dataset.value = decoded.password || "";
  document.getElementById("entry-notes").textContent = decoded.notes || "";

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
        try {
          await navigator.clipboard.writeText("");
        } catch {}
        const s2 = document.getElementById("copy-status");
        if (s2) s2.textContent = "Cleared.";
      }, 90_000);
    } catch (err) {
      setStatus(`Copy failed: ${err.message || err}`, "error");
    }
  });

  document.getElementById("lock-button").addEventListener("click", () => {
    lockClient();
    window.location.reload();
  });
}

async function wireGrant() {
  await ensureUnlocked();
  showSection("grant-area");

  // Fetch the target user's pubkey + fingerprint.
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

    // The captive STATE.vaultKey is exportable (v1; v2 makes it
    // non-extractable in a Worker). Wrap it under the target's pubkey.
    const targetPub = await importSpki(b64ToBytes(target.public_key));
    const wrapped = await rsaWrapAesKey(targetPub, STATE.vaultKey);

    await api("POST", `/grants/${VAULT.targetUserId}`, {
      wrapped_vault_key: bytesField(wrapped),
    });
    setStatus("Granted. The target user can now unlock the vault.", "success");
  });

  document.getElementById("decline-button").addEventListener("click", async () => {
    if (!confirm("Decline this registration? The user will need to re-register from scratch.")) {
      return;
    }
    setStatus("Declining…");
    await api("DELETE", `/users/${VAULT.targetUserId}`);
    setStatus("Declined. The user's pending registration was removed.", "success");
  });
}
