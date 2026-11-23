// The Hoppers phone's wire types and fetch helpers.
//
// Every path is derived from the URL the phone landed on, because that URL IS
// the game: /{slug}/hoppers/{game}.
import type { LiveFrame } from '../live/liveStream';
import { HOP } from './consts.js';

export const base = window.location.pathname.replace(/\/+$/, '');

export type Phase = (typeof HOP.phase)[keyof typeof HOP.phase];
export type Sub = (typeof HOP.sub)[keyof typeof HOP.sub];
export type HeatStatus = (typeof HOP.heat)[keyof typeof HOP.heat];

export interface WirePlayer {
  id: string;
  name: string;
  color: string;
  hat: string;
  bot: boolean;
  score: number;
  connected: boolean;
  heat: number;
  wins: number;
}

export interface WireResult {
  player: string;
  place: number;
  points: number;
}

export interface WireHeat {
  num: number;
  name: string;
  final: boolean;
  frogs: string[];
  status: HeatStatus;
  results: WireResult[] | null;
}

export interface WireStanding {
  player: string;
  rank: number;
  points: number;
}

export interface WireHop {
  seq: number;
  frog: string;
  roll: number;
  moved: string[];
  back: boolean;
  perfect: boolean;
}

// The phone's own bits; absent for a spectator.
export interface WireYou {
  id: string;
  connected: boolean;
  heat: number;
  racing: boolean;
  myTurn: boolean;
  onDeck: boolean;
  place: number;
  score: number;
  rank: number;
  wins: number;
}

export interface Frame extends LiveFrame {
  game: string;
  build: string;
  phase: Phase;
  sub: Sub;
  round: number;
  rounds: number;
  heat: number;
  turn: number;
  current: string;
  onDeck: string;
  deadline: number;
  flickBand: number;
  flickSpot?: number;
  players: WirePlayer[];
  ranking: string[] | null;
  backward: string[] | null;
  heats: WireHeat[] | null;
  standings: WireStanding[] | null;
  champion: string;
  roundOver: boolean;
  lastRace: boolean;
  lastHop: WireHop | null;
  events: { seq: number; text: string }[] | null;
  you?: WireYou;
}

export interface FrogDraft {
  name: string;
  color: string;
  hat: string;
}

export interface Me {
  playerId: string;
  name: string;
}

export interface Posted<T> {
  ok: boolean;
  status: number;
  data: T | null;
  error: string;
}

// post sends one command. Failures come back as values, never throws: every
// caller shows a toast, and an unhandled rejection on a phone is a dead button.
export async function post<T>(path: string, body: unknown): Promise<Posted<T>> {
  try {
    const r = await fetch(base + path, {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    if (!r.ok) return { ok: false, status: r.status, data: null, error: (await r.text()).trim() };
    return { ok: true, status: r.status, data: (await r.json()) as T, error: '' };
  } catch {
    return { ok: false, status: 0, data: null, error: '' };
  }
}

export const join = (f: FrogDraft) => post<Me>('/join', f);
export const reclaim = (playerId: string, code: string) => post<Me>('/reclaim', { playerId, code });
export const flick = (power: number, perfect: boolean) => post<Frame>('/flick', { power, perfect });
