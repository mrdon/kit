import { createContext, useContext } from 'react';
import type { Me } from './api';

// MeContext carries the signed-in user + workspace chrome data, fetched
// once in Shell and shared with the top bar and every page.
export const MeContext = createContext<Me | null>(null);

export const useMe = (): Me | null => useContext(MeContext);

// MeRefreshContext exposes a re-fetch of /me so a page that changes workspace
// state (e.g. toggling apps on the Apps settings page) can refresh the shared
// `me` — updating nav/launcher live without a full page reload.
export const MeRefreshContext = createContext<() => void>(() => {});

export const useRefreshMe = (): (() => void) => useContext(MeRefreshContext);
