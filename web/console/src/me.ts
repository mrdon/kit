import { createContext, useContext } from 'react';
import type { Me } from './api';

// MeContext carries the signed-in user + workspace chrome data, fetched
// once in Shell and shared with the top bar and every page.
export const MeContext = createContext<Me | null>(null);

export const useMe = (): Me | null => useContext(MeContext);
