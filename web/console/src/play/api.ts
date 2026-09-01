// The player's wire types and fetch helpers.
//
// Every path is derived from the URL the phone actually landed on, because
// that URL IS the game: /{slug}/trivia/{game}. Nothing here needs to know the
// workspace slug as a separate concept.

export interface WireTeam {
  id: string;
  name: string;
  score: number;
  eligible: boolean;
  answered: boolean;
  stakeLocked: boolean;
  chipsPlaced: number;
}

export interface WireCell {
  id: string;
  col: number;
  row: number;
  topic: string;
  points: number;
  played: boolean;
}

export interface WireSlot {
  id: string;
  pos: number;
  value: number | null;
  label: string;
  teams: string[];
  pot: number;
  chips: { team: string; amount: number }[];
}

export interface WireRound {
  id: string;
  isFinal: boolean;
  ordinal: number;
  points: number;
  text: string;
  answered: number;
  eligible: number;
}

export interface WireScoring {
  correctValue: number;
  correctText: string;
  winningSlot: string;
  deltas: Record<string, number>;
  boardPoints: Record<string, number>;
  betDeltas: Record<string, number>;
}

export interface WireOwnChip {
  tokenIndex: number;
  amount: number;
  slotId: string;
}

export interface WireYou {
  teamId: string;
  name: string;
  score: number;
  answered: boolean;
  chips: WireOwnChip[];
  stake: number | null;
  delta: number | null;
  wroteWinner: boolean;
}

export type Phase =
  | 'setup' | 'lobby' | 'board' | 'question'
  | 'reveal' | 'betting' | 'scoring' | 'podium';

export interface PlayerFrame {
  version: number;
  game: string;
  title: string;
  phase: Phase;
  serverNow: number;
  deadlineMs: number;
  finalWager: boolean;
  teams: WireTeam[];
  board: WireCell[];
  round: WireRound | null;
  slots: WireSlot[];
  scoring: WireScoring | null;
  tokens: number[];
  you: WireYou | null;
  // Served by the server, not written here, so the phone and the TV cannot
  // tell a room different games.
  rules: string[];
}

// base is the game's own URL, with any trailing slash removed.
export const base = window.location.pathname.replace(/\/+$/, '');

async function post<T>(path: string, body: unknown): Promise<T> {
  const res = await fetch(base + path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    credentials: 'same-origin',
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new Error((await res.text()).trim() || res.statusText);
  return res.status === 204 ? (null as T) : ((await res.json()) as T);
}

export function join(name: string) {
  return post<{ teamId: string; name: string }>('/join', { name });
}

export function reclaim(teamId: string, code: string) {
  return post<{ teamId: string }>('/reclaim', { teamId, code });
}

export function submitAnswer(answer: string, stake: number | null) {
  return post<PlayerFrame>('/answer', { answer, stake });
}

// A PUT of the desired placement for ONE chip, so every retry over flaky bar
// wifi is idempotent. slotId null lifts the chip back off the board.
export async function placeChip(chip: number, slotId: string | null) {
  const res = await fetch(base + '/bets', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    credentials: 'same-origin',
    body: JSON.stringify({ chip, slotId }),
  });
  if (!res.ok) throw new Error((await res.text()).trim() || res.statusText);
  return (await res.json()) as PlayerFrame;
}

export async function me(): Promise<{ teamId: string; name: string } | null> {
  const res = await fetch(base + '/me', { credentials: 'same-origin' });
  if (res.status === 204 || !res.ok) return null;
  return res.json();
}

// parseAnswer mirrors the server's ParseAnswer exactly: strip currency,
// grouping, percent and underscores, then require the WHOLE string to be a
// number. A partial parse is what turns "12 feet" into 12 and produces an
// argument at the bar.
export function parseAnswer(raw: string): number | null {
  const cleaned = raw.trim().replace(/[$,%_\s ]/g, '');
  if (cleaned === '') return null;
  if (!/^[-+]?(\d+\.?\d*|\.\d+)([eE][-+]?\d+)?$/.test(cleaned)) return null;
  const v = Number(cleaned);
  return Number.isFinite(v) ? v : null;
}

export function money(n: number): string {
  const neg = n < 0;
  const s = Math.abs(n).toLocaleString('en-US');
  return (neg ? '-$' : '$') + s;
}
