import { createContext, useContext } from 'react';
import type { ActionInput, BrewState, BrewTile } from '../../api';

// What can be open over the brewer's screens. Every form opens as a sheet
// over the tiles rather than as its own page: the brewer is mid-task, and
// closing the sheet should put them back exactly where they were.
export type FormSpec =
  | { kind: 'tank'; vesselId: string }
  | { kind: 'brew'; vesselId?: string; planEntryId?: string }
  | { kind: 'move'; vesselId?: string; toVesselId?: string }
  | { kind: 'package'; vesselId: string }
  | { kind: 'spill'; vesselId: string }
  | { kind: 'level'; vesselId: string }
  | { kind: 'confirm'; action: 'dry_hop' | 'clean' | 'on_tap' | 'kick'; vesselId: string }
  | { kind: 'variant'; vesselId: string }
  | { kind: 'repack' }
  | { kind: 'out' }
  | { kind: 'count' }
  | { kind: 'plan'; planEntryId: string };

export interface BrewCtx {
  state: BrewState;
  isAdmin: boolean;
  open: (f: FormSpec) => void;
  close: () => void;
  // record saves an action. It resolves to an error message for the form to
  // show, or null when it was saved (the sheet closes and the banner shows
  // what was recorded, with Undo).
  record: (input: ActionInput) => Promise<string | null>;
  undo: (actionId: string) => Promise<void>;
  reload: () => void;
  notify: (text: string) => void;
}

export const BrewContext = createContext<BrewCtx | null>(null);

export function useBrew(): BrewCtx {
  const c = useContext(BrewContext);
  if (!c) throw new Error('useBrew outside the brewing shell');
  return c;
}

export function tileById(state: BrewState, id: string | undefined): BrewTile | undefined {
  return state.vessels.find((v) => v.id === id);
}

export function beerName(state: BrewState, id: string | null | undefined): string {
  return state.beers.find((b) => b.id === id)?.name ?? '';
}

// bbl prints a volume the way the server does: one decimal, two when small.
export function bbl(v: number): string {
  return Math.abs(v) < 1 ? v.toFixed(2) : v.toFixed(1);
}

export function shortDate(iso: string): string {
  const d = new Date(iso);
  return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
}
