// Vault browser-side crypto. v1 stub.
//
// This module will eventually implement:
//  - Argon2id KDF (via vendored @noble/hashes — to be added under static/vendor/)
//  - HKDF-SHA256 split into enc_key / auth_hash
//  - WebCrypto RSA-OAEP-2048 keypair gen + wrap/unwrap of vault_key
//  - WebCrypto AES-GCM with 12-byte random nonce per entry
//  - SharedWorker holding unwrapped vault_key as non-extractable CryptoKey
//  - BroadcastChannel('kit-vault-lock') for cross-tab unlock/lock
//  - IndexedDB cache of wrapped private key + wrapped vault key
//  - Lock-on-idle (10 min idle / 30 min absolute / tab close)
//
// For now the page renders, the form submits go nowhere, and the user
// sees a "not yet implemented" status. Server-side endpoints are wired
// and ready; this file just needs the crypto layer fleshed out.

const app = document.getElementById('vault-app');
const status = document.getElementById('status');
function setStatus(text, kind) {
  if (!status) return;
  status.textContent = text;
  status.className = kind || '';
}

if (app) {
  setStatus('Vault browser-side crypto is not yet implemented in v1. The server is ready; the JS layer is the next implementation step.', 'error');
}
