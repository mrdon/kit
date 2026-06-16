// Vault page-side crypto — a faithful TS port of the proven vault.js
// primitives. Byte-for-byte identical to the vanilla client so vaults set
// up or rotated here decrypt in the old UI and vice-versa.
//
//   KDF:   PBKDF2-SHA256, 600,000 iters, 32-byte output
//   Split: enc_key   = HKDF(master_key, salt, "kit-vault-v1-enc")
//          auth_hash = HKDF(master_key, salt, "kit-vault-v1-auth")
//   Wrap:  wrapped_vault_key = AES-GCM(vault_key, enc_key, nonce,
//                                      aad=tenant_id_bytes)

export const KDF_ITERATIONS = 600_000;
const KDF_HASH = 'SHA-256';

async function pbkdf2(password: string, salt: Uint8Array): Promise<Uint8Array> {
  const baseKey = await crypto.subtle.importKey(
    'raw',
    new TextEncoder().encode(password) as BufferSource,
    { name: 'PBKDF2' },
    false,
    ['deriveBits'],
  );
  const bits = await crypto.subtle.deriveBits(
    { name: 'PBKDF2', hash: KDF_HASH, salt: salt as BufferSource, iterations: KDF_ITERATIONS },
    baseKey,
    256,
  );
  return new Uint8Array(bits);
}

async function hkdf(
  masterKey: Uint8Array,
  salt: Uint8Array,
  info: string,
): Promise<Uint8Array> {
  const baseKey = await crypto.subtle.importKey(
    'raw',
    masterKey as BufferSource,
    { name: 'HKDF' },
    false,
    ['deriveBits'],
  );
  const bits = await crypto.subtle.deriveBits(
    {
      name: 'HKDF',
      hash: KDF_HASH,
      salt: salt as BufferSource,
      info: new TextEncoder().encode(info) as BufferSource,
    },
    baseKey,
    256,
  );
  return new Uint8Array(bits);
}

export interface DerivedKeys {
  encKey: CryptoKey;
  authHash: Uint8Array;
}

export async function deriveKeys(password: string, salt: Uint8Array): Promise<DerivedKeys> {
  const masterKey = await pbkdf2(password, salt);
  const encKeyBytes = await hkdf(masterKey, salt, 'kit-vault-v1-enc');
  const authHash = await hkdf(masterKey, salt, 'kit-vault-v1-auth');
  const encKey = await crypto.subtle.importKey(
    'raw',
    encKeyBytes as BufferSource,
    { name: 'AES-GCM' },
    false,
    ['encrypt', 'decrypt'],
  );
  return { encKey, authHash };
}

export async function aesGcmEncryptWithAAD(
  key: CryptoKey,
  plaintext: Uint8Array,
  aad: Uint8Array,
): Promise<{ ciphertext: Uint8Array; nonce: Uint8Array }> {
  const nonce = crypto.getRandomValues(new Uint8Array(12));
  const ct = await crypto.subtle.encrypt(
    { name: 'AES-GCM', iv: nonce, additionalData: aad as BufferSource },
    key,
    plaintext as BufferSource,
  );
  return { ciphertext: new Uint8Array(ct), nonce };
}

export async function aesGcmDecryptWithAAD(
  key: CryptoKey,
  ciphertext: Uint8Array,
  nonce: Uint8Array,
  aad: Uint8Array,
): Promise<Uint8Array> {
  const pt = await crypto.subtle.decrypt(
    { name: 'AES-GCM', iv: nonce as BufferSource, additionalData: aad as BufferSource },
    key,
    ciphertext as BufferSource,
  );
  return new Uint8Array(pt);
}

// ---- encoding helpers ----

export function bytesToB64(bytes: Uint8Array): string {
  let bin = '';
  for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
  return btoa(bin);
}

export function b64ToBytes(s: string): Uint8Array {
  const bin = atob(s);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

export function hexToBytes(hex: string): Uint8Array {
  if (typeof hex !== 'string' || hex.length % 2 !== 0) {
    throw new Error('invalid hex string');
  }
  const out = new Uint8Array(hex.length / 2);
  for (let i = 0; i < out.length; i++) {
    out[i] = parseInt(hex.substr(i * 2, 2), 16);
  }
  return out;
}

export function sameBytes(a: Uint8Array, b: Uint8Array): boolean {
  if (a.length !== b.length) return false;
  let diff = 0;
  for (let i = 0; i < a.length; i++) diff |= a[i] ^ b[i];
  return diff === 0;
}

// ---- password + passphrase generation (faithful copies) ----

export function generatePassword(length: number): string {
  const charset = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789!@#$%^&*';
  const buf = new Uint32Array(length);
  crypto.getRandomValues(buf);
  let out = '';
  for (let i = 0; i < length; i++) out += charset[buf[i] % charset.length];
  return out;
}

const DICEWARE_WORDS = [
  'able','acid','acorn','actor','add','admit','after','again','age','agent','agree','ahead','aim','air','alarm','album',
  'alert','alien','alive','all','alley','ally','alone','along','also','alter','amber','amend','among','amount','amuse','anchor',
  'ancient','angel','anger','angle','angry','ankle','annoy','answer','ant','apple','april','arch','arctic','area','arena','argue',
  'arm','army','arrow','art','ash','ask','aspen','aster','atlas','atom','attic','auburn','august','aunt','author','auto',
  'autumn','avenue','awake','awful','baby','back','bacon','badge','bagel','baker','balance','ball','ballad','banana','band','banjo',
  'bank','barn','basic','basil','basin','basket','bass','bat','batch','bath','battle','bay','beach','bean','bear','beard',
  'beat','beauty','beaver','bee','beech','beef','before','begin','beige','bell','belt','bench','bend','berry','best','bicycle',
  'big','bike','bill','bind','birch','bird','biscuit','bishop','bit','black','blank','blaze','blend','bless','blink','blizzard',
  'block','bloom','blue','blush','board','boat','body','boil','bold','bolt','bond','bone','book','boot','border','born',
  'borrow','both','bottle','bottom','bow','box','boy','brain','brake','branch','brass','brave','bread','break','breeze','brick',
  'bridge','brief','bright','bring','brisk','broad','bronze','brook','brown','brush','bubble','bucket','buddy','budget','buffalo','bug',
  'build','bulb','bull','bunny','burst','bus','busy','butter','button','buzz','cable','cactus','cake','calm','camel','camp',
  'canal','candle','candy','cane','canyon','cap','cape','car','card','care','cargo','carry','cart','case','cash','castle',
  'casual','cat','catch','cedar','cell','cello','cement','center','chain','chair','chalk','champ','change','chapel','charm','chart',
  'chase','cheap','check','cheer','cherry','chess','chest','chief','chili','chime','chin','chip','chirp','choice','chord','chorus',
  'chrome','cider','cinder','circle','city','civic','claim','clam','clarity','clean','clear','cliff','climb','clip','clock','cloth',
  'cloud','clover','club','clue','coach','coast','coconut','code','coffee','coil','coin','cold','color','comet','comic','comma',
];

export function diceware(n: number): string {
  const out = new Uint32Array(n);
  crypto.getRandomValues(out);
  const parts: string[] = [];
  for (let i = 0; i < n; i++) parts.push(DICEWARE_WORDS[out[i] % DICEWARE_WORDS.length]);
  return parts.join('-');
}

export function normalizeURL(raw: string): string {
  const v = raw.trim();
  if (!v) return '';
  if (/^[a-zA-Z][a-zA-Z0-9+.-]*:/.test(v)) return v;
  return `https://${v}`;
}
