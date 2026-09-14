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
  // Empty during `wager` — the prompt does not leave the server until the
  // blind bets are in. `category` is what the room bets against.
  text: string;
  category: string;
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
  // Whether this table is in the round in flight, and which question it IS in
  // from when it is not. inFromQuestion is omitted by the server for a table
  // that is already in, so it is optional here rather than 0.
  eligible: boolean;
  inFromQuestion?: number;
}

// Who picks the next category. Public — every phone sees the same name, and
// only the table whose teamId matches is told it is their turn. The reason
// rides alongside so the phone can say why without knowing the rule.
export interface WirePicker {
  teamId: string;
  name: string;
}

export type PickerReason = '' | 'drawn' | 'wrote_winner' | 'lowest';

export type Phase =
  | 'setup' | 'lobby' | 'board' | 'wager' | 'question'
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
  picker: WirePicker | null;
  pickerReason: PickerReason;
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

export function submitAnswer(answer: string) {
  return post<PlayerFrame>('/answer', { answer });
}

// A PUT for the same reason /bets is one: a statement of the desired wager
// rather than an event, so a retry over flaky bar wifi is idempotent and a
// double-tap cannot stack two bets. The server clamps to the table's bank.
export async function setWager(amount: number) {
  const res = await fetch(base + '/wager', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    credentials: 'same-origin',
    body: JSON.stringify({ amount }),
  });
  if (!res.ok) throw new Error((await res.text()).trim() || res.statusText);
  return (await res.json()) as PlayerFrame;
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
