import { SLUG } from '../../workspace';

// The vault JSON API is unchanged from the vanilla client: it lives at
// /{slug}/apps/vault/api/* and gates on the X-Kit-Vault CSRF header (NOT
// X-Kit-Web — these are the existing vault endpoints). The console reuses
// them as-is; only the rendering moved to React.
const API_BASE = `/${SLUG}/apps/vault/api`;

export async function vaultApi<T = any>(
  method: string,
  path: string,
  body?: unknown,
): Promise<T> {
  const resp = await fetch(API_BASE + path, {
    method,
    credentials: 'same-origin',
    headers: {
      'X-Kit-Vault': '1',
      ...(body !== undefined ? { 'Content-Type': 'application/json' } : {}),
    },
    body: body !== undefined ? JSON.stringify(body) : null,
  });
  if (resp.status === 401) {
    window.location.href = `/${SLUG}/login?return_to=${encodeURIComponent(location.pathname)}`;
    throw new Error('redirecting to login');
  }
  if (!resp.ok) {
    const text = await resp.text().catch(() => '');
    throw new Error(`HTTP ${resp.status}: ${text || resp.statusText}`);
  }
  if (resp.status === 204) return null as T;
  return resp.json() as Promise<T>;
}

export interface VaultStatus {
  set_up: boolean;
  kdf_params?: { algo: string; iterations: number; salt: string };
  tenant_id_bytes?: string;
}

export interface VaultEntrySummary {
  id: string;
  title: string;
  username?: string;
  url?: string;
  scope_summary?: string;
}

export interface VaultEntryFull {
  Title?: string;
  title?: string;
  Username?: string;
  username?: string;
  URL?: string;
  url?: string;
  ValueCiphertext?: string;
  value_ciphertext?: string;
  ValueNonce?: string;
  value_nonce?: string;
}

export interface Principal {
  id: string;
  name: string;
}
