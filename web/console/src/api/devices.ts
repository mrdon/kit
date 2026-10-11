import { apiDelete, apiGet, apiPatch, apiPost } from './client';

// --- Devices (paired shared machines) ---

export interface Device {
  id: string;
  label: string;
  capabilities: string[];
  created_at: string;
  last_seen_at: string | null;
  revoked_at: string | null;
  // A kiosk start link exists for this device. The link itself is shown
  // once, when made.
  has_start_link: boolean;
}

// A start link: the URL that signs a browser in as a device for the
// session, for kiosks that wipe their cookies. Short enough to type.
export interface StartLink {
  start_url: string;
}

// A browser waiting on /{slug}/pair. The approver sees three pictures, one
// of which is on the device's screen; the code is typed instead.
export interface PendingPairing {
  id: string;
  pictures: string[];
  expires_at: string;
}

export interface DevicePreset {
  key: string;
  label: string;
  capabilities: string[];
}

export interface CapabilityInfo {
  name: string;
  label: string;
}

export interface DevicesView {
  devices: Device[];
  pending: PendingPairing[];
  presets: DevicePreset[];
  capabilities: CapabilityInfo[];
  pair_url: string;
}

export interface ApprovePairingBody {
  pairing_id?: string;
  picture?: string;
  code?: string;
  label: string;
  capabilities: string[];
}

export const devicesApi = {
  devices: () => apiGet<DevicesView>('/devices'),
  approvePairing: (body: ApprovePairingBody) =>
    apiPost<{ device: Device }>('/devices/pairings/approve', body),
  cancelPairing: (id: string) =>
    apiPost<void>(`/devices/pairings/${encodeURIComponent(id)}/cancel`),
  updateDevice: (id: string, body: { label: string; capabilities: string[] }) =>
    apiPatch<{ device: Device }>(`/devices/${encodeURIComponent(id)}`, body),
  revokeDevice: (id: string) => apiDelete<void>(`/devices/${encodeURIComponent(id)}`),
  createKiosk: (body: { label: string; capabilities: string[] }) =>
    apiPost<StartLink & { device: Device }>('/devices', body),
  makeStartLink: (id: string) =>
    apiPost<StartLink>(`/devices/${encodeURIComponent(id)}/start-link`),
};
