// Shared types and formatters for the trivia console pages, mirroring
// pages/tasks/common.ts.

export type Phase =
  | 'setup' | 'lobby' | 'board' | 'question'
  | 'reveal' | 'betting' | 'scoring' | 'podium';

export interface TriviaSettings {
  title: string;
  board_rows: number;
  board_columns: number;
  cell_values: number[];
  token_values: number[];
  final_wager: boolean;
  answer_seconds: number;
  reveal_seconds: number;
  bet_seconds: number;
}

export interface TriviaGame {
  id: string;
  name: string;
  title: string;
  phase: Phase;
  join_url: string;
  tv_url: string;
  teams: number;
  cells: number;
  played: number;
  leader: string;
  created_at: string;
  settings: TriviaSettings;
}

export interface TopicCount {
  key: string;
  label: string;
  total: number;
  unused: number;
}

export interface HostTeam {
  id: string;
  name: string;
  score: number;
  eligible: boolean;
  answered: boolean;
  stakeLocked: boolean;
  chipsPlaced: number;
}

export interface HostCell {
  id: string;
  col: number;
  row: number;
  topic: string;
  points: number;
  played: boolean;
}

export interface HostSlot {
  id: string;
  pos: number;
  value: number | null;
  label: string;
  teams: string[];
  pot: number;
  chips: { team: string; amount: number }[];
}

export interface HostRound {
  id: string;
  isFinal: boolean;
  ordinal: number;
  points: number;
  text: string;
  answered: number;
  eligible: number;
}

export interface HostScoring {
  correctValue: number;
  correctText: string;
  winningSlot: string;
  deltas: Record<string, number>;
  boardPoints: Record<string, number>;
  betDeltas: Record<string, number>;
}

export interface HostFrame {
  version: number;
  game: string;
  title: string;
  phase: Phase;
  serverNow: number;
  deadlineMs: number;
  finalWager: boolean;
  teams: HostTeam[];
  board: HostCell[];
  round: HostRound | null;
  slots: HostSlot[];
  scoring: HostScoring | null;
  answer: { value: number; text: string } | null;
  tokens: number[];
  progress: { cellsPlayed: number; cellsTotal: number; finalPlayed: boolean };
}

export interface ImportReport {
  imported: number;
  updated: number;
  skipped_duplicates: number;
  errors: { line: number; message: string }[];
  truncated: boolean;
  topics: TopicCount[];
}

export type Action =
  | 'open_lobby' | 'start' | 'pick_cell' | 'reveal' | 'open_betting'
  | 'score' | 'next' | 'final' | 'extend' | 'finish';

export const PHASE_LABEL: Record<Phase, string> = {
  setup: 'Setting up',
  lobby: 'Teams joining',
  board: 'On the board',
  question: 'Answering',
  reveal: 'Answers revealed',
  betting: 'Placing bets',
  scoring: 'Scored',
  podium: 'Finished',
};

// The label on the one big primary button, per phase. Naming it by what
// happens next is what lets the host drive the night without reading the
// screen — the button is always in the same place and always says the thing
// they are about to do.
export function primaryAction(phase: Phase, boardEmpty: boolean, finalWager: boolean, finalPlayed: boolean):
  { action: Action; label: string } | null {
  switch (phase) {
    case 'setup': return { action: 'open_lobby', label: 'Open the lobby' };
    case 'lobby': return { action: 'start', label: 'Start the game' };
    case 'board':
      if (boardEmpty && finalWager && !finalPlayed) return { action: 'final', label: 'Final question' };
      if (boardEmpty) return { action: 'finish', label: 'Go to the podium' };
      return null; // waiting for the host to pick a cell
    case 'question': return { action: 'reveal', label: 'Reveal answers' };
    case 'reveal': return { action: 'open_betting', label: 'Open betting' };
    case 'betting': return { action: 'score', label: 'Score the round' };
    case 'scoring': return { action: 'next', label: 'Next' };
    case 'podium': return null;
  }
}

export function money(n: number): string {
  const neg = n < 0;
  return (neg ? '-$' : '$') + Math.abs(n).toLocaleString('en-US');
}

// defaultSettings is the shipped game, mirrored from the server's
// DefaultSettings so a new game is created with the same shape the docs and
// the host's card describe: 5 categories x 2 rows at $500/$1000, ten
// questions, two chips at $100/$200, and a final.
export function defaultSettings(): TriviaSettings {
  return {
    title: '',
    board_rows: 2,
    board_columns: 5,
    cell_values: [500, 1000],
    token_values: [100, 200],
    final_wager: true,
    answer_seconds: 60,
    reveal_seconds: 15,
    bet_seconds: 45,
  };
}

// balanceWarning mirrors the server's advice about cell values against chip
// values. It used to be a hard 400 from the server, which was the wrong call:
// it is an opinion about balance, not a correctness rule, and refusing a
// host's own settings with an error they could do nothing about is overreach.
// Now it is said once, here, and the save goes through either way.
export function balanceWarning(s: TriviaSettings): string | null {
  if (!s.cell_values.length || !s.token_values.length) return null;
  const cell = Math.min(...s.cell_values);
  const chip = Math.max(...s.token_values);
  if (cell >= 2 * chip) return null;
  return (
    `The cheapest cell (${money(cell)}) is worth less than twice the biggest chip (${money(chip)}). ` +
    'With a full room, only the tables that wrote the winning answer take a cell, so betting becomes ' +
    'the main way to score and the questions matter less. Fine for a small room — worth raising if you ' +
    'expect a crowd.'
  );
}
