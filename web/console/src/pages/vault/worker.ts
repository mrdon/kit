// SharedWorker proxy + unlock manager for the console vault. The worker
// (web/console/public/vault-worker.js) holds the unwrapped vault_key and
// exposes encrypt/decrypt/lock — copied verbatim from the proven vanilla
// implementation. This module mirrors vault.js's page-side glue: the
// worker RPC, IndexedDB cache, and the unlock/setup/rotate flows.

import {
  aesGcmDecryptWithAAD,
  aesGcmEncryptWithAAD,
  b64ToBytes,
  bytesToB64,
  deriveKeys,
  hexToBytes,
  KDF_ITERATIONS,
  sameBytes,
} from './crypto';
import { vaultApi, type VaultStatus } from './api';

// ---- SharedWorker RPC ----

let workerPort: MessagePort | null = null;
let nextMsgID = 1;
const pending = new Map<number, { resolve: (v: any) => void; reject: (e: Error) => void }>();

export function connectWorker(): void {
  if (workerPort) return;
  const sw = new SharedWorker('/console/vault-worker.js', {
    name: 'kit-vault-console',
    type: 'module',
  });
  workerPort = sw.port;
  workerPort.onmessage = (ev: MessageEvent) => {
    const m = ev.data;
    const p = pending.get(m.id);
    if (!p) return;
    pending.delete(m.id);
    if (m.ok) p.resolve(m.result);
    else p.reject(new Error(m.error || 'worker error'));
  };
  workerPort.start();
}

function workerCall<T = any>(type: string, payload?: Record<string, unknown>): Promise<T> {
  const id = nextMsgID++;
  return new Promise<T>((resolve, reject) => {
    pending.set(id, { resolve, reject });
    workerPort!.postMessage({ id, type, ...(payload || {}) });
  });
}

export const hasKey = () => workerCall<boolean>('has_key');
export const lock = () => workerCall('lock');

// ---- IndexedDB cache (mirrors vault.js) ----

const DB_NAME = 'kit-vault';
const DB_STORE = 'self';

interface CacheRow {
  kdfParams: { algo: string; iterations: number; salt: string };
  wrappedVaultKey: string;
  wrappedVaultKeyNonce: string;
  tenantIDBytes: string;
  vaultGeneration: number;
}

function openDB(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open(DB_NAME, 2);
    req.onupgradeneeded = () => {
      const db = req.result;
      if (!db.objectStoreNames.contains(DB_STORE)) db.createObjectStore(DB_STORE);
    };
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
}

async function dbPut(value: CacheRow): Promise<void> {
  const db = await openDB();
  await new Promise<void>((resolve, reject) => {
    const tx = db.transaction(DB_STORE, 'readwrite');
    tx.objectStore(DB_STORE).put(value, 'current');
    tx.oncomplete = () => resolve();
    tx.onerror = () => reject(tx.error);
  });
}

async function dbGet(): Promise<CacheRow | null> {
  const db = await openDB();
  return new Promise((resolve, reject) => {
    const tx = db.transaction(DB_STORE, 'readonly');
    const req = tx.objectStore(DB_STORE).get('current');
    req.onsuccess = () => resolve(req.result || null);
    req.onerror = () => reject(req.error);
  });
}

export function dbWipe(): Promise<void> {
  return new Promise((resolve) => {
    const req = indexedDB.deleteDatabase(DB_NAME);
    req.onsuccess = () => resolve();
    req.onerror = () => resolve();
    req.onblocked = () => resolve();
  });
}

// ---- unlock / setup / rotate ----

export async function unlock(password: string): Promise<void> {
  if (await hasKey()) return;

  let kdfParams;
  const cached = await dbGet();
  if (cached && cached.kdfParams) {
    kdfParams = cached.kdfParams;
  } else {
    const st = await vaultApi<VaultStatus>('GET', '/status');
    if (!st || !st.set_up) {
      throw new Error('Vault is not set up yet. Ask an admin to set it up.');
    }
    kdfParams = st.kdf_params!;
  }

  const salt = b64ToBytes(kdfParams.salt);
  const { encKey, authHash } = await deriveKeys(password, salt);

  const resp = await vaultApi<any>('POST', '/unlock', { auth_hash: bytesToB64(authHash) });
  const wrappedVK = b64ToBytes(resp.wrapped_vault_key);
  const wrappedVKNonce = b64ToBytes(resp.wrapped_vault_key_nonce);
  const aad = hexToBytes(resp.tenant_id_bytes);
  const vaultGeneration = resp.vault_generation | 0;

  const rawVaultKey = await aesGcmDecryptWithAAD(encKey, wrappedVK, wrappedVKNonce, aad);
  await workerCall('set_key', { rawKey: rawVaultKey.buffer, vaultGeneration });
  rawVaultKey.fill(0);

  await dbPut({
    kdfParams,
    wrappedVaultKey: bytesToB64(wrappedVK),
    wrappedVaultKeyNonce: bytesToB64(wrappedVKNonce),
    tenantIDBytes: resp.tenant_id_bytes,
    vaultGeneration,
  });
}

export async function setupVault(password: string): Promise<void> {
  if (await hasKey()) await lock();

  const salt = crypto.getRandomValues(new Uint8Array(16));
  const kdfParams = {
    algo: 'pbkdf2-sha256',
    iterations: KDF_ITERATIONS,
    salt: bytesToB64(salt),
  };
  const { encKey, authHash } = await deriveKeys(password, salt);
  const rawVaultKey = crypto.getRandomValues(new Uint8Array(32));

  const st = await vaultApi<VaultStatus>('GET', '/status');
  const tenantIDHex = st.tenant_id_bytes;
  if (!tenantIDHex) throw new Error("server didn't return tenant_id_bytes");
  const aad = hexToBytes(tenantIDHex);

  const { ciphertext: wrappedVK, nonce } = await aesGcmEncryptWithAAD(encKey, rawVaultKey, aad);

  // Local round-trip self-check before sending (matches vanilla).
  const roundtrip = await aesGcmDecryptWithAAD(encKey, wrappedVK, nonce, aad);
  if (!sameBytes(roundtrip, rawVaultKey)) throw new Error('local round-trip mismatch');

  await vaultApi('POST', '/setup', {
    kdf_params: kdfParams,
    auth_hash: bytesToB64(authHash),
    wrapped_vault_key: bytesToB64(wrappedVK),
    wrapped_vault_key_nonce: bytesToB64(nonce),
  });

  await workerCall('set_key', { rawKey: rawVaultKey.buffer, vaultGeneration: 1 });
  rawVaultKey.fill(0);
  await dbPut({
    kdfParams,
    wrappedVaultKey: bytesToB64(wrappedVK),
    wrappedVaultKeyNonce: bytesToB64(nonce),
    tenantIDBytes: tenantIDHex,
    vaultGeneration: 1,
  });
}

export async function rotate(oldPassword: string, newPassword: string): Promise<void> {
  if (await hasKey()) await lock();
  await unlock(oldPassword);

  const cached = await dbGet();
  if (!cached) throw new Error('missing cached vault state after unlock');
  const oldSalt = b64ToBytes(cached.kdfParams.salt);
  const oldKeys = await deriveKeys(oldPassword, oldSalt);
  const wrappedVK = b64ToBytes(cached.wrappedVaultKey);
  const wrappedVKNonce = b64ToBytes(cached.wrappedVaultKeyNonce);
  const aad = hexToBytes(cached.tenantIDBytes);
  const rawVaultKey = await aesGcmDecryptWithAAD(oldKeys.encKey, wrappedVK, wrappedVKNonce, aad);

  const newSalt = crypto.getRandomValues(new Uint8Array(16));
  const newKDF = { algo: 'pbkdf2-sha256', iterations: KDF_ITERATIONS, salt: bytesToB64(newSalt) };
  const newKeys = await deriveKeys(newPassword, newSalt);
  const { ciphertext: newWrappedVK, nonce: newNonce } = await aesGcmEncryptWithAAD(
    newKeys.encKey,
    rawVaultKey,
    aad,
  );
  rawVaultKey.fill(0);

  const resp = await vaultApi<any>('POST', '/rotate', {
    kdf_params: newKDF,
    auth_hash: bytesToB64(newKeys.authHash),
    wrapped_vault_key: bytesToB64(newWrappedVK),
    wrapped_vault_key_nonce: bytesToB64(newNonce),
  });
  await dbPut({
    kdfParams: newKDF,
    wrappedVaultKey: bytesToB64(newWrappedVK),
    wrappedVaultKeyNonce: bytesToB64(newNonce),
    tenantIDBytes: cached.tenantIDBytes,
    vaultGeneration: resp.vault_generation | 0,
  });
}

// ---- entry encrypt/decrypt via the worker ----

export async function encryptEntry(plaintext: Uint8Array): Promise<{ ciphertext: Uint8Array; nonce: Uint8Array }> {
  const out = await workerCall<{ ciphertext: Uint8Array; nonce: Uint8Array }>('encrypt', {
    plaintext,
  });
  return { ciphertext: new Uint8Array(out.ciphertext), nonce: new Uint8Array(out.nonce) };
}

export async function decryptEntry(ciphertext: Uint8Array, nonce: Uint8Array): Promise<Uint8Array> {
  const out = await workerCall<ArrayBuffer | Uint8Array>('decrypt', { ciphertext, nonce });
  return new Uint8Array(out as ArrayBuffer);
}
