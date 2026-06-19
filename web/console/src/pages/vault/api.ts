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
  opts?: { noAuthRedirect?: boolean },
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
  // A 401 normally means the web session lapsed, so we bounce to login.
  // Some vault endpoints also return 401 for step-up ("recent unlock
  // required") — callers that can handle that inline pass noAuthRedirect
  // so they get the error instead of a surprise navigation.
  if (resp.status === 401 && !opts?.noAuthRedirect) {
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
  Tags?: string[];
  tags?: string[];
  ValueCiphertext?: string;
  value_ciphertext?: string;
  ValueNonce?: string;
  value_nonce?: string;
  role_id?: string;
  role_name?: string;
}

export interface Principal {
  id: string;
  name: string;
}
